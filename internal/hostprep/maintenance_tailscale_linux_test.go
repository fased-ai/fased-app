//go:build linux

package hostprep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestProtectedTailscaleLoginPreservesExistingAndResumesFresh(t *testing.T) {
	for _, scenario := range []string{"fresh", "already-running", "existing-request", "resume-original", "stopped", "start-failed", "changed-request"} {
		t.Run(scenario, func(t *testing.T) {
			state, url := "NeedsLogin", ""
			switch scenario {
			case "already-running":
				state = "Running"
			case "existing-request", "resume-original":
				url = "https://login.tailscale.com/a/old"
			case "stopped":
				state = "Stopped"
			}
			var out bytes.Buffer
			starts, polls := 0, 0
			run := func(_ context.Context, args ...string) ([]byte, error) {
				if strings.Join(args, " ") != "/usr/bin/tailscale status --json" {
					t.Fatal("unexpected command", args)
				}
				return json.Marshal(map[string]string{"BackendState": state, "AuthURL": url})
			}
			start := func(context.Context) error {
				starts++
				if scenario == "start-failed" {
					return errors.New("preserved")
				}
				url = "https://login.tailscale.com/a/original"
				return nil
			}
			wait := func(context.Context) error {
				polls++
				if scenario == "changed-request" {
					url = "https://login.tailscale.com/a/other"
				} else {
					state, url = "Running", ""
				}
				return nil
			}
			remember := func(url string) error {
				if url != "https://login.tailscale.com/a/original" {
					return errors.New("different")
				}
				return nil
			}
			resume := func(url string) error {
				if scenario != "resume-original" || url != "https://login.tailscale.com/a/old" {
					return errors.New("foreign")
				}
				return nil
			}
			err := protectedTailscaleLogin(t.Context(), "owner", &out, run, wait, start, remember, resume)
			wantErr := scenario == "existing-request" || scenario == "stopped" || scenario == "start-failed" || scenario == "changed-request"
			if (err != nil) != wantErr {
				t.Fatal("unexpected result", err)
			}
			if (starts == 1) != (scenario == "fresh" || scenario == "start-failed" || scenario == "changed-request") {
				t.Fatal("unexpected enrollment start", starts)
			}
			if scenario == "fresh" && (!strings.Contains(out.String(), "/a/original") || polls != 1) {
				t.Fatal("original sign-in was not followed")
			}
		})
	}
}

func TestFreshTailscaleRefusesPriorOrChangedState(t *testing.T) {
	if err := noPriorTailscaleState(func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }); err != nil {
		t.Fatal(err)
	}
	for _, existing := range []string{"/var/lib/tailscale", "/etc/tailscale", "/etc/default/tailscaled", "/run/tailscale/tailscaled.sock", "/usr/sbin/tailscaled", "/etc/systemd/system/tailscaled.service.d"} {
		if err := noPriorTailscaleState(func(path string) (os.FileInfo, error) {
			if path == existing {
				return nil, nil
			}
			return nil, os.ErrNotExist
		}); err == nil {
			t.Fatal("adopted prior state", existing)
		}
	}
	if err := noPriorTailscaleState(func(string) (os.FileInfo, error) { return nil, os.ErrPermission }); err == nil {
		t.Fatal("accepted unreadable state")
	}
	good := []byte(`{"OperatorUser":"","WantRunning":false}`)
	baseline, err := freshTailscalePrefs(good, []byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := freshTailscalePrefs([]byte(`{"OperatorUser":"","WantRunning":true}`), []byte(`[]`))
	if err != nil || baseline == changed {
		t.Fatal("preference change did not alter the bound identity")
	}
	for _, tc := range []struct{ prefs, profiles string }{
		{`{"OperatorUser":"other"}`, `[]`}, {`{"OperatorUser":""}`, `[{"id":"old"}]`},
		{`{"OperatorUser":""}`, `null`}, {`{`, `[]`},
	} {
		if _, err := freshTailscalePrefs([]byte(tc.prefs), []byte(tc.profiles)); err == nil {
			t.Fatal("accepted ambiguous existing Tailscale state")
		}
	}
	for _, v := range []string{`{"short":"1.104.4"}`, `{"short":"1.105.0"}`, `{}`} {
		if _, err := supportedTailscaleVersion([]byte(v)); err == nil {
			t.Fatal("accepted unverified Tailscale version")
		}
	}
	if _, err := supportedTailscaleVersion([]byte(`{"short":"1.104.7"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestProtectedTailscaleLoginRefusesExistingInstallations(t *testing.T) {
	for _, exists := range []string{"", "/var/lib/fased-app-vps-1100", "/var/lib/fased-app-custody-1100"} {
		err := requireFreshTailscaleSetup(1100, func(path string) (os.FileInfo, error) {
			if path == exists {
				return nil, nil
			}
			return nil, os.ErrNotExist
		})
		if (err != nil) != (exists != "") {
			t.Fatal(exists, err)
		}
	}
	if err := requireFreshTailscaleSetup(1100, func(string) (os.FileInfo, error) { return nil, os.ErrPermission }); err == nil {
		t.Fatal("unreadable installation state was accepted")
	}
}

func TestFreshTailscaleSocketRequiresOriginalRootDaemon(t *testing.T) {
	for _, tc := range []struct {
		peer *unix.Ucred
		pid  int
		want bool
	}{
		{&unix.Ucred{Uid: 0, Pid: 124}, 124, true}, {&unix.Ucred{Uid: 1, Pid: 124}, 124, false},
		{&unix.Ucred{Uid: 0, Pid: 125}, 124, false}, {nil, 124, false}, {&unix.Ucred{Uid: 0, Pid: 1}, 1, false},
	} {
		if got := sameTailscalePeer(tc.peer, tc.pid); got != tc.want {
			t.Fatal("unexpected socket peer admission")
		}
	}
}
