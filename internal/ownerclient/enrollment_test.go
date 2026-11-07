package ownerclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

func enrollmentPair(t *testing.T) Package {
	t.Helper()
	root := t.TempDir()
	p := Package{Core: filepath.Join(root, "fased-app"), Signer: filepath.Join(root, "fased-signer"), LocalPaths: localinstall.Paths{Root: root, Data: filepath.Join(t.TempDir(), "state")}}
	for path, text := range map[string]string{p.Core: "inert matched core", p.Signer: "inert matched signer"} {
		if err := os.WriteFile(path, []byte(text), 0700); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := bundle.Inspect(p.Core)
	p.CoreSHA256 = f.SHA256
	f, _ = bundle.Inspect(p.Signer)
	p.SignerSHA256 = f.SHA256
	return p
}

func TestNativeEnrollmentLocalPlanRetainsConsentAndExactPair(t *testing.T) {
	p := enrollmentPair(t)
	i := intent()
	ready := false
	c := &Client{Intent: i, Peer: Peer{Version: 1, InstanceID: i.InstanceID, OwnerUID: 1000, Network: i.Network}}
	c.run = func(_ context.Context, path string, args []string, input []byte) ([]byte, error) {
		if len(input) != 0 {
			t.Fatal("enrollment received wallet secrets")
		}
		if path == p.Core {
			return []byte(`{"version":1,"supported":true,"walletSigner":false}`), nil
		}
		if path == "/usr/bin/test" {
			return nil, &commandExit{1}
		}
		if !ready {
			return nil, &commandExit{127}
		}
		return json.Marshal(Peer{Version: 1, InstanceID: i.InstanceID, SignerID: strings.Repeat("b", 32), Network: i.Network, OwnerUID: 1000, CoreSHA256: p.CoreSHA256, SignerSHA256: p.SignerSHA256})
	}
	plan, err := prepareEnrollment(t.Context(), c, p)
	if err != nil {
		t.Fatal(err)
	}
	path, args := plan.Terminal()
	if strings.Contains(strings.Join(args, " "), "--recover-suspended") || path != p.Core || !strings.Contains(strings.Join(args, " "), "--native-owner") || !strings.Contains(strings.Join(args, " "), "--ordinary-custody") || !strings.Contains(strings.Join(args, " "), "--prepare-host") {
		t.Fatal("enrollment lost explicit consent", args)
	}
	// Once a synchronous terminal has exited, an unfinished enrollment must
	// fail now; it cannot become success by waiting for an unrelated process.
	deadline, cancel := context.WithTimeout(t.Context(), time.Second)
	if _, err := plan.Wait(deadline); err == nil || deadline.Err() != nil {
		cancel()
		t.Fatal("unfinished setup was polled instead of refused", err)
	}
	cancel()
	ready = true
	bound, err := plan.Wait(t.Context())
	if err != nil || bound.Peer.SignerSHA256 != p.SignerSHA256 {
		t.Fatal("pair not bound", err)
	}
}

func TestNativeEnrollmentRemoteStagesPairBeforePTYAndResumes(t *testing.T) {
	for _, state := range []string{"fresh", "retained-stage", "suspended-recovery"} {
		existing := state != "fresh"
		recovery := state == "suspended-recovery"
		t.Run(state, func(t *testing.T) {
			p := enrollmentPair(t)
			i := intent()
			ready := false
			staged := map[string]string{}
			copies := 0
			reconciled := false
			c := &Client{Intent: i, Peer: Peer{Version: 1, InstanceID: i.InstanceID, Network: i.Network, OwnerUID: 1000}, host: "test.ts.net", user: "alice", knownHosts: "/private/pins"}
			c.run = func(_ context.Context, path string, args []string, input []byte) ([]byte, error) {
				if len(input) != 0 {
					t.Fatal("secrets in preparation")
				}
				if path == p.Core {
					return []byte(`{"version":1,"supported":true,"walletSigner":false}`), nil
				}
				if path == "/usr/bin/scp" {
					copies++
					parts := strings.SplitN(args[len(args)-1], ":", 2)
					f, _ := bundle.Inspect(args[len(args)-2])
					staged[parts[1]] = f.SHA256
					return nil, nil
				}
				if path != "/usr/bin/ssh" {
					t.Fatal("unexpected tool", path)
				}
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "-tt") {
					t.Fatal("preflight opened terminal")
				}
				n := 0
				for args[n] != "--" {
					n++
				}
				command, rest := args[n+2], args[n+3:]
				switch command {
				case "/usr/bin/test":
					if existing {
						return nil, nil
					}
					return nil, &commandExit{1}
				case "/usr/bin/sudo":
					if strings.Join(rest, " ") != "-n -l -- /usr/sbin/useradd" {
						t.Fatal("unexpected administrator operation", rest)
					}
					return nil, nil
				case "/usr/bin/uname":
					return []byte(map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]), nil
				case "/usr/bin/mkdir", "/usr/bin/chmod":
					return nil, nil
				case "/usr/bin/stat":
					return []byte("1000:700:directory"), nil
				case "/usr/bin/sha256sum":
					file := rest[len(rest)-1]
					if h := staged[file]; h != "" {
						return []byte(h + "  " + file + "\n"), nil
					}
					return nil, &commandExit{1}
				case "/usr/bin/mv":
					from, to := rest[len(rest)-2], rest[len(rest)-1]
					if staged[to] == "" {
						staged[to] = staged[from]
					}
					return nil, nil
				default:
					if strings.Contains(joined, "enrollment-resume") {
						reconciled = true
						return json.Marshal(struct {
							Version  int  `json:"version"`
							Resume   bool `json:"resume"`
							Recovery bool `json:"recoverSuspended,omitempty"`
						}{1, true, recovery})
					}
					if !ready {
						return nil, &commandExit{127}
					}
					return json.Marshal(Peer{Version: 1, InstanceID: i.InstanceID, SignerID: strings.Repeat("b", 32), Network: i.Network, OwnerUID: 1000, CoreSHA256: p.CoreSHA256, SignerSHA256: p.SignerSHA256})
				}
			}
			plan, err := prepareEnrollment(t.Context(), c, p)
			if err != nil {
				t.Fatal(err)
			}
			if copies != 2 || reconciled != existing || strings.Contains(strings.Join(plan.arguments, " "), "--start-existing") != (existing && !recovery) || strings.Contains(strings.Join(plan.arguments, " "), "--recover-suspended") != recovery {
				t.Fatal("matching pair/resume was not checked")
			}
			path, args := plan.Terminal()
			joined := strings.Join(args, " ")
			if path != "/usr/bin/ssh" || !strings.Contains(joined, "-tt") || !strings.Contains(joined, "StrictHostKeyChecking=yes") || !strings.Contains(joined, "--source remote") || strings.Contains(joined, "--native-owner") {
				t.Fatal("remote consent scope", args)
			}
			ready = true
			if _, err = plan.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeEnrollmentHeldAdmissionAndForeignStateRefuse(t *testing.T) {
	p := enrollmentPair(t)
	i := intent()
	c := &Client{Intent: i, Peer: Peer{Version: 1, InstanceID: i.InstanceID, OwnerUID: 1000, Network: i.Network}}
	c.run = func(_ context.Context, path string, args []string, _ []byte) ([]byte, error) {
		if path == p.Core {
			return []byte(`{"version":1,"supported":false,"walletSigner":false}`), nil
		}
		t.Fatal("held admission started setup")
		return nil, nil
	}
	if _, err := prepareEnrollment(t.Context(), c, p); err == nil {
		t.Fatal("ordinary admission bypassed")
	}
	c.run = func(_ context.Context, path string, args []string, _ []byte) ([]byte, error) {
		if path == p.Core && strings.Contains(strings.Join(args, " "), "enrollment-capability") {
			return []byte(`{"version":1,"supported":true,"walletSigner":false}`), nil
		}
		if path == "/usr/bin/test" {
			return nil, nil
		}
		return nil, &commandExit{1}
	}
	if _, err := prepareEnrollment(t.Context(), c, p); err == nil {
		t.Fatal("foreign target adopted")
	}
}

func TestNativeEnrollmentRemoteExpiredAdminStopsBeforeTransfer(t *testing.T) {
	p := enrollmentPair(t)
	i := intent()
	c := &Client{Intent: i, Peer: Peer{Version: 1, InstanceID: i.InstanceID, Network: i.Network, OwnerUID: 1000}, host: "test.ts.net", user: "alice", knownHosts: "/private/pins"}
	c.run = func(_ context.Context, path string, args []string, _ []byte) ([]byte, error) {
		if path == p.Core {
			return []byte(`{"version":1,"supported":true,"walletSigner":false}`), nil
		}
		joined := strings.Join(args, " ")
		if path != "/usr/bin/ssh" {
			t.Fatal("expired authority transferred payload", path)
		}
		if strings.Contains(joined, "/usr/bin/test") {
			return nil, &commandExit{1}
		}
		if strings.Contains(joined, "/usr/bin/sudo -n -l -- /usr/sbin/useradd") {
			return nil, &commandExit{1}
		}
		if strings.Contains(joined, "custody window") {
			return nil, &commandExit{127}
		}
		t.Fatal("expired authority started setup", args)
		return nil, nil
	}
	_, err := prepareEnrollment(t.Context(), c, p)
	var approval *RemoteAdminConsent
	if !errors.As(err, &approval) || !strings.Contains(approval.Command, "--operator alice --wallets --wallet-instance "+i.InstanceID+" --wallet-network "+i.Network) || strings.Contains(approval.Command, "root@") || strings.Contains(approval.Command, "authorized_keys") {
		t.Fatal("missing bounded official-installer handoff", err)
	}
}
