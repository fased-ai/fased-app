//go:build linux

package custodyinstall

import (
	"bytes"
	"io"
	"reflect"
	"strconv"
	"testing"
)

func TestRemoteAgentsHelperFixedCommand(t *testing.T) {
	tx, _, _, _ := ordinaryBootstrapModel(t, true)
	m := tx.Base
	input, output, errorsOut := bytes.NewBufferString("original typed command"), &bytes.Buffer{}, &bytes.Buffer{}
	flags, err := agentsHelperFlags(m, "", true)
	if err != nil {
		t.Fatal(err)
	}
	cmd := appClientCommand(t.Context(), m, "agents", flags, input, output, errorsOut)
	want := []string{"/usr/bin/sudo", "-n", "--", "/usr/bin/setpriv", "--reuid", strconv.Itoa(m.App.UID), "--regid", strconv.Itoa(m.App.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", m.Root + "/fased-app", "agents", "--data-dir", m.Root + "/app-state", "--custody-onchain", "--request-stdin"}
	if !reflect.DeepEqual(cmd.Args, want) || cmd.Stdin != input || cmd.Stdout != output || cmd.Stderr != errorsOut {
		t.Fatal("owner command or input changed", cmd.Args)
	}
	readFlags, err := agentsHelperFlags(m, "trading-analyst", false)
	if err != nil || !reflect.DeepEqual(readFlags, []string{"--custody-onchain", "--profile", "trading-analyst"}) {
		t.Fatal("read authority or profile changed", readFlags, err)
	}
	s := &System{In: input, Out: errorsOut}
	if err := s.appClientInput(t.Context(), m, "foreign-action", nil, input, io.Discard); err == nil || err.Error() != "unsupported app helper action" {
		t.Fatal("foreign helper admitted", err)
	}
	if err := s.AgentsHelper(t.Context(), m, "profile", true, io.Discard); err == nil {
		t.Fatal("conflicting read/write admitted")
	}
	if err := s.AgentsHelper(t.Context(), Manifest{Location: "remote"}, "", false, io.Discard); err == nil {
		t.Fatal("invalid installation admitted")
	}
	if err := s.AgentsHelper(t.Context(), Manifest{Location: "local"}, "", false, io.Discard); err == nil {
		t.Fatal("Local installation admitted")
	}
}

func TestRemotePrivateProofUsesFixedAppUIDStatus(t *testing.T) {
	tx, _, _, _ := ordinaryBootstrapModel(t, true)
	m := tx.Base
	input := bytes.NewBufferString(`{"action":"begin","token":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`)
	cmd := appClientCommand(t.Context(), m, "status", []string{"--private-proof-stdin"}, input, io.Discard, io.Discard)
	want := append([]string{"/usr/bin/sudo", "-n", "--"}, appArgs(m, "status")...)
	want = append(want, "--private-proof-stdin")
	if !reflect.DeepEqual(cmd.Args, want) || cmd.Stdin != input {
		t.Fatal("private proof changed app-UID boundary", cmd.Args)
	}
}
