package ownerclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/signer"
)

// Enrollment keeps initial OS authorization separate from wallet secret input.
// Its terminal executes the existing enrollment controller as the ordinary owner;
// only that controller's fixed OS utilities may cross the admin boundary.
type Enrollment struct {
	client       *Client
	pair         Package
	core, signer string
	arguments    []string
}

func PrepareEnrollment(ctx context.Context, intent signer.OwnerIntent, origin, user string, pair Package) (*Enrollment, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	c, err := locate(ctx, intent, origin, user, filepath.Join(home, ".ssh", "known_hosts"), os.Getuid(), run)
	if err != nil {
		return nil, err
	}
	return prepareEnrollment(ctx, c, pair)
}

func prepareEnrollment(ctx context.Context, c *Client, pair Package) (*Enrollment, error) {
	if err := pair.verify(); err != nil {
		return nil, err
	}
	// The controlled compile-time admission has no runtime switch. A normal
	// held binary cannot be turned into an owner signer by clicking Enable.
	b, err := c.run(ctx, pair.Core, []string{"custody", "enrollment-capability"}, nil)
	var capability struct {
		Version      int  `json:"version"`
		Supported    bool `json:"supported"`
		WalletSigner bool `json:"walletSigner"`
	}
	if err != nil || decode(b, &capability) != nil || capability.Version != 1 || !capability.Supported || capability.WalletSigner {
		return nil, errors.New("protected wallet enrollment is not enabled in this package")
	}
	// An existing or foreign target is inspected/reconciled, never overwritten.
	if c.inspect(ctx) == nil {
		return nil, errors.New("wallets are already enrolled; reconnect")
	}
	_, err = c.invoke(ctx, "/usr/bin/test", []string{"-e", "/var/lib/fased-app-custody-" + strconv.Itoa(c.Peer.OwnerUID) + "/install.json"}, nil)
	var exited *commandExit
	existing := err == nil
	if err != nil && (!errors.As(err, &exited) || exited.code != 1) {
		return nil, errors.New("existing or unavailable custody state must be recovered before enrollment")
	}
	e := &Enrollment{client: c, pair: pair, core: pair.Core, signer: pair.Signer}
	if c.host != "" {
		// The bootstrap grant expires and is removed after installation. Inspect
		// existing OS authority before transferring code or opening a terminal;
		// never manufacture another grant from browser/app authentication.
		if _, err := c.invoke(ctx, "/usr/bin/sudo", []string{"-n", "-l", "--", "/usr/sbin/useradd"}, nil); err != nil {
			return nil, remoteAdminHandoff(c)
		}
		arch, err := c.invoke(ctx, "/usr/bin/uname", []string{"-m"}, nil)
		want := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
		if err != nil || want == "" || strings.TrimSpace(string(arch)) != want {
			return nil, errors.New("native client and Remote wallet architecture must match")
		}
		stage := "/tmp/fased-wallet-" + strconv.Itoa(c.Peer.OwnerUID) + "-" + c.Intent.OperationID
		_, _ = c.invoke(ctx, "/usr/bin/mkdir", []string{"--mode=0700", "--", stage}, nil)
		owned, err := c.invoke(ctx, "/usr/bin/stat", []string{"--printf", "%u:%a:%F", "--", stage}, nil)
		if err != nil || string(owned) != strconv.Itoa(c.Peer.OwnerUID)+":700:directory" {
			return nil, errors.New("Remote wallet staging must be private and owner-owned")
		}
		if e.core, err = c.stage(ctx, stage, pair.Core, pair.CoreSHA256, "fased-app"); err != nil {
			return nil, err
		}
		if e.signer, err = c.stage(ctx, stage, pair.Signer, pair.SignerSHA256, "fased-signer"); err != nil {
			return nil, err
		}
	}
	mode := "local"
	if c.host != "" {
		mode = "remote"
	}
	if existing {
		args := []string{"custody", "enrollment-resume", "--expected-instance", c.Intent.InstanceID, "--source", mode, "--network", c.Intent.Network, "--core-sha256", pair.CoreSHA256, "--signer-sha256", pair.SignerSHA256}
		if mode == "local" {
			args = append(args, "--native-owner")
		}
		b, err := c.invoke(ctx, e.core, args, nil)
		var resume struct {
			Version int  `json:"version"`
			Resume  bool `json:"resume"`
		}
		if err != nil || decode(b, &resume) != nil || resume.Version != 1 || !resume.Resume {
			return nil, errors.New("existing custody state does not match this retained owner enrollment")
		}
	}
	e.arguments = []string{"custody", "enroll", "--ordinary-custody", "--source", mode, "--expected-instance", c.Intent.InstanceID, "--network", c.Intent.Network, "--signer-binary", e.signer, "--disposable-fixture", "--prepare-host"}
	if existing {
		e.arguments = append(e.arguments, "--start-existing")
	}
	if mode == "local" {
		e.arguments = append(e.arguments, "--native-owner")
		if pair.LocalPaths.Root == "" || pair.LocalPaths.Data == "" {
			return nil, errors.New("the installed Local source is required")
		}
		e.arguments = append(e.arguments, "--prefix", pair.LocalPaths.Root, "--data-dir", pair.LocalPaths.Data)
	}
	return e, nil
}

func (c *Client) stage(ctx context.Context, root, source, want, name string) (string, error) {
	final := root + "/" + want + "-" + name
	if c.hashMatches(ctx, final, want) {
		return final, nil
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	pending := root + "/." + hex.EncodeToString(id[:]) + ".pending"
	args := append([]string{"-F", "/dev/null", "-B", "-q"}, c.sshPolicy()...)
	args = append(args, "--", source, c.user+"@"+c.host+":"+pending)
	if _, err := c.run(ctx, "/usr/bin/scp", args, nil); err != nil {
		return "", errors.New("Remote wallet transfer interrupted; retained staging is safe to resume")
	}
	if !c.hashMatches(ctx, pending, want) {
		return "", errors.New("Remote wallet payload digest does not match the verified native package")
	}
	if _, err := c.invoke(ctx, "/usr/bin/mv", []string{"--no-clobber", "--no-target-directory", "--", pending, final}, nil); err != nil || !c.hashMatches(ctx, final, want) {
		return "", errors.New("existing Remote wallet payload is preserved")
	}
	if _, err := c.invoke(ctx, "/usr/bin/chmod", []string{"0700", "--", final}, nil); err != nil {
		return "", err
	}
	return final, nil
}

func (c *Client) hashMatches(ctx context.Context, path, want string) bool {
	b, err := c.invoke(ctx, "/usr/bin/sha256sum", []string{"--", path}, nil)
	return err == nil && strings.TrimSpace(string(b)) == want+"  "+path
}

// Terminal is a fixed command vector. Remote PTY exists solely for initial OS
// consent; reconnect and every key operation keep the normal no-PTY channel.
func (e *Enrollment) Terminal() (string, []string) {
	if e.client.host == "" {
		return e.core, append([]string(nil), e.arguments...)
	}
	args := append([]string{"-F", "/dev/null", "-tt", "-a"}, e.client.sshPolicy()...)
	args = append(args, "-l", e.client.user, "--", e.client.host, e.core)
	return "/usr/bin/ssh", append(args, e.arguments...)
}

// Wait reconciles the installed target after the synchronous approval terminal
// exits. A failed controller is not a reason to poll for five more minutes.
func (e *Enrollment) Wait(ctx context.Context) (*Client, error) {
	if err := e.client.inspect(ctx); err != nil {
		return nil, errors.New("wallet setup is unfinished; resume the same enrollment")
	}
	if e.client.Peer.CoreSHA256 != e.pair.CoreSHA256 || e.client.Peer.SignerSHA256 != e.pair.SignerSHA256 {
		return nil, errors.New("enrolled images differ from the approved native package")
	}
	return e.client, nil
}
