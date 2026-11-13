//go:build linux

package signer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestInstallationStartGuardRequiresHoldOrExistingStoppedRecovery(t *testing.T) {
	v, _, _ := configuredFixture(t, fixtureDir(t))
	defer v.Close()
	intent, binding := installationFixture(t, v)
	held := &update.Installation{Version: 1, Binding: binding, Digest: update.InstallDigest(binding), Phase: "active"}
	c := Config{OwnerUID: 1800, SignerID: binding.InstallationID}
	marker, _ := json.Marshal(InstallationGuardMarker{Version: 1, Owner: c.OwnerUID, SignerID: c.SignerID, IntentID: intent.ID, Digest: held.Digest, Direction: "forward"})
	validStatus := Response{Installation: held, Status: Status{Locked: true}}
	for _, tc := range []struct {
		name       string
		marker     []byte
		markerErr  error
		stoppedErr error
		status     Response
		statusErr  error
		want       bool
		wantStatus int
	}{
		{"no hold", nil, os.ErrNotExist, nil, Response{}, ErrNoInstallationHold, false, 1},
		{"unavailable signer", nil, os.ErrNotExist, nil, Response{}, errors.New("unavailable"), false, 1},
		{"valid locked hold", nil, os.ErrNotExist, nil, validStatus, nil, true, 1},
		{"unlocked signer", nil, os.ErrNotExist, nil, Response{Installation: held}, nil, false, 1},
		{"existing stopped marker", marker, nil, nil, Response{}, nil, true, 0},
		{"existing marker with active signer", marker, nil, errors.New("active"), validStatus, nil, true, 1},
		{"active signer without hold", marker, nil, errors.New("active"), Response{}, ErrNoInstallationHold, false, 1},
		{"foreign marker", []byte(`{"version":1}`), nil, nil, validStatus, nil, false, 0},
		{"unreadable marker", nil, errors.New("denied"), nil, validStatus, nil, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := validateInstallationStartGuard(c, intent.ID, held.Digest, "forward", func() ([]byte, error) {
				return tc.marker, tc.markerErr
			}, func() error { return tc.stoppedErr }, func() (Response, error) {
				calls++
				return tc.status, tc.statusErr
			})
			if (err == nil) != tc.want || calls != tc.wantStatus {
				t.Fatalf("unexpected start decision: error=%v status calls=%d", err, calls)
			}
			if errors.Is(tc.statusErr, ErrNoInstallationHold) && !errors.Is(err, ErrNoInstallationHold) {
				t.Fatalf("authenticated absence was not preserved: %v", err)
			}
		})
	}
	healthy := *held
	healthy.Phase = "healthy-held"
	if err := validateInstallationStartGuard(c, intent.ID, held.Digest, "forward", func() ([]byte, error) { return nil, os.ErrNotExist }, func() error { return nil }, func() (Response, error) {
		return Response{Installation: &healthy, Status: Status{Locked: true}}, nil
	}); err == nil {
		t.Fatal("forward restart accepted after the signer advanced")
	}
	foreign := c
	foreign.SignerID = strings.Repeat("f", 32)
	if err := validateInstallationStartGuard(foreign, intent.ID, held.Digest, "forward", func() ([]byte, error) { return nil, os.ErrNotExist }, func() error { return nil }, func() (Response, error) {
		return validStatus, nil
	}); err == nil {
		t.Fatal("another signer's hold was accepted")
	}
}

func TestInstallationGuardMarkerRequiresExactCanonicalIdentityAndPhase(t *testing.T) {
	c := Config{OwnerUID: 1800, SignerID: strings.Repeat("a", 32)}
	intent, digest := strings.Repeat("b", 32), strings.Repeat("c", 64)
	base := InstallationGuardMarker{Version: 1, Owner: c.OwnerUID, SignerID: c.SignerID, IntentID: intent, Digest: digest, Direction: "forward"}
	for _, direction := range []string{"forward", "rollback", "completion", "cleanup-rollback", "cleanup-complete"} {
		base.Direction = direction
		b, _ := json.Marshal(base)
		if err := validateInstallationGuardMarker(c, intent, digest, b); err != nil {
			t.Fatal(err)
		}
		if err := validateInstallationGuardMarker(c, intent, digest, append(b, '\n')); err == nil {
			t.Fatal("noncanonical marker accepted")
		}
	}
	for _, change := range []func(*InstallationGuardMarker){func(m *InstallationGuardMarker) { m.Owner++ }, func(m *InstallationGuardMarker) { m.SignerID = strings.Repeat("d", 32) }, func(m *InstallationGuardMarker) { m.IntentID = strings.Repeat("e", 32) }, func(m *InstallationGuardMarker) { m.Digest = strings.Repeat("f", 64) }, func(m *InstallationGuardMarker) { m.Direction = "unknown" }} {
		m := base
		change(&m)
		b, _ := json.Marshal(m)
		if err := validateInstallationGuardMarker(c, intent, digest, b); err == nil {
			t.Fatal("foreign marker accepted")
		}
	}
}

func TestCustodyBoundaryRequiresBothServicesStopped(t *testing.T) {
	marker := "/var/lib/fased-app-custody-1800/update-start-blocked.json"
	for _, activeRole := range []string{"", "app", "signer"} {
		t.Run(activeRole, func(t *testing.T) {
			seen := map[string]bool{}
			property := func(_ context.Context, path, _, name string) ([]byte, error) {
				role := ""
				for _, candidate := range []string{"app", "signer"} {
					if strings.HasSuffix(path, "_2d"+candidate+"_2eservice") {
						role = candidate
					}
				}
				if role == "" {
					return nil, fmt.Errorf("unexpected service")
				}
				seen[role] = true
				switch name {
				case "Asserts":
					return []byte(`{"type":"a(sbbsi)","data":[["AssertPathExists",false,true,"` + marker + `",0]]}`), nil
				case "ActiveState":
					if role == activeRole {
						return []byte(`{"type":"s","data":"active"}`), nil
					}
					return []byte(`{"type":"s","data":"inactive"}`), nil
				case "MainPID":
					return []byte(`{"type":"u","data":0}`), nil
				case "KillMode":
					return []byte(`{"type":"s","data":"control-group"}`), nil
				}
				return nil, fmt.Errorf("unexpected property")
			}
			err := verifyStoppedCustodyServices(context.Background(), 1800, marker, property)
			if activeRole == "" {
				if err != nil || !seen["app"] || !seen["signer"] {
					t.Fatal("both stopped services were not checked", err)
				}
			} else if err == nil {
				t.Fatal("active service accepted", activeRole)
			}
		})
	}
}

func TestSignerSystemdGuardRequiresLoadedAssertionAndStoppedService(t *testing.T) {
	marker := "/var/lib/fased-app-custody-1800/update-start-blocked.json"
	assertions := `{"type":"a(sbbsi)","data":[["AssertPathExists",false,true,"` + marker + `",0]]}`
	active := `{"type":"s","data":"inactive"}`
	pid := `{"type":"u","data":0}`
	kill := `{"type":"s","data":"control-group"}`
	if err := validateSignerSystemdGuard([]byte(assertions), []byte(active), []byte(pid), []byte(kill), marker); err != nil {
		t.Fatal(err)
	}
	for name, fields := range map[string][4]string{
		"missing assertion": {`{"type":"a(sbbsi)","data":[]}`, active, pid, kill},
		"wrong marker":      {`{"type":"a(sbbsi)","data":[["AssertPathExists",false,true,"/foreign",0]]}`, active, pid, kill},
		"not negated":       {`{"type":"a(sbbsi)","data":[["AssertPathExists",false,false,"` + marker + `",0]]}`, active, pid, kill},
		"trigger only":      {`{"type":"a(sbbsi)","data":[["AssertPathExists",true,true,"` + marker + `",0]]}`, active, pid, kill},
		"active signer":     {assertions, `{"type":"s","data":"active"}`, pid, kill},
		"running process":   {assertions, active, `{"type":"u","data":123}`, kill},
		"unsafe kill mode":  {assertions, active, pid, `{"type":"s","data":"process"}`},
		"malformed result":  {`{"type":"a(sbbsi)","data":[["AssertPathExists",false,true,"` + marker + `","bad"]]}`, active, pid, kill},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateSignerSystemdGuard([]byte(fields[0]), []byte(fields[1]), []byte(fields[2]), []byte(fields[3]), marker); err == nil {
				t.Fatal("unsafe systemd state accepted")
			}
		})
	}
}
