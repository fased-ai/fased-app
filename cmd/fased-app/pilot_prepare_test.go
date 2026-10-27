package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestOpenAIPilotPreparationCLI(t *testing.T) {
	root := filepath.Join(".evidence", "pilot-preparation-cli")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp(root, "f-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := os.RemoveAll(dir); e != nil {
			t.Error(e)
		}
	})
	instance, review := filepath.Join(dir, "instance"), filepath.Join(dir, "review")
	var out bytes.Buffer
	if e = run([]string{"pilot", "prepare", "--instance-dir", instance, "--review-dir", review}, nil, &out, io.Discard); e != nil {
		t.Fatal(e)
	}
	var p core.OpenAIPilotPreparation
	if json.Unmarshal(out.Bytes(), &p) != nil || p.RealAIEnabled || p.CredentialsCreated || p.PermitCreated || p.State != "review-required-not-applied" {
		t.Fatal("CLI activated pilot")
	}
	for _, args := range [][]string{
		{"--instance-dir", instance, "--review-dir", review, "--activate"},
		{"--instance-dir", instance, "--review-dir", review, "--key", "must-not-be-accepted"},
		{"--instance-dir", instance, "--review-dir", review},
	} {
		out.Reset()
		if e = pilotPrepareCommand(args, &out, io.Discard); e == nil || out.Len() != 0 {
			t.Fatal("CLI adopted existing state or accepted activation/secret")
		}
	}
	s, e := core.OpenStore(instance)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v, e := s.AIConnection()
	if e != nil || v.PilotAvailable || v.LiveCallsEnabled || v.Ref != "" {
		t.Fatal("CLI review file enabled runtime")
	}
}
