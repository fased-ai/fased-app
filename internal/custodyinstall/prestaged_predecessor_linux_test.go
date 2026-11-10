//go:build linux

package custodyinstall

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The immutable predecessor may reuse an OS-prepared exact file without
// invoking sudo. This tests only publication; it does not approve a complete
// update policy or prove an installed migration.
func TestPredecessorPublicationAcceptsOnlyExactPrestagedSigner(t *testing.T) {
	id := strings.Repeat("a", 32)
	intent := strings.Repeat("b", 32)
	data := []byte("disposable approved signer")
	target := fmt.Sprintf("/var/lib/fased-app-custody-%d/updates/%s/next-signer", os.Getuid(), intent)
	stage := strings.TrimSuffix(target, "next-signer") + ".next-signer-" + id + ".pending"
	for _, tc := range []struct {
		name    string
		file    hostFile
		stage   *hostFile
		wantErr bool
	}{
		{"exact", hostFile{Data: data, UID: 0, Mode: 0755, Regular: true}, nil, false},
		{"substituted", hostFile{Data: []byte("changed"), UID: 0, Mode: 0755, Regular: true}, nil, true},
		{"owner writable", hostFile{Data: data, UID: os.Getuid(), Mode: 0755, Regular: true}, nil, true},
		{"wrong mode", hostFile{Data: data, UID: 0, Mode: 0777, Regular: true}, nil, true},
		{"foreign pending", hostFile{Data: data, UID: 0, Mode: 0755, Regular: true}, &hostFile{Data: []byte("foreign"), UID: 0, Mode: 0755, Regular: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &memoryHost{files: map[string]hostFile{target: tc.file}}
			if tc.stage != nil {
				h.files[stage] = *tc.stage
			}
			err := publish(context.Background(), target, id, data, 0755, h.read, h.run)
			if (err != nil) != tc.wantErr || h.calls != 0 {
				t.Fatalf("predecessor publication: error=%v, privileged operations=%d", err, h.calls)
			}
		})
	}
}

func TestPredecessorPrestagedSignerReplacementAndRollback(t *testing.T) {
	tx, dir := modelTransaction(t)
	ops := modelOps(t, tx, dir)
	target := tx.Base.Root + "/fased-signer"
	old := []byte("old-signer")
	next := []byte("next-signer")
	for _, data := range [][]byte{old, next} {
		name := ".replace-" + tx.Base.ID + "-fased-signer-" + sum(data)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0755); err != nil {
			t.Fatal(err)
		}
	}
	run := ops.run
	writes := 0
	ops.run = func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if args[0] == "/usr/bin/install" {
			writes++
			return nil, fmt.Errorf("unexpected privileged stdin write")
		}
		return run(ctx, input, args...)
	}
	for _, data := range [][]byte{next, next, old, old} {
		if err := replaceRecord(context.Background(), target, tx.Base.ID, data, ops); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "fased-signer"))
		if err != nil || string(got) != string(data) {
			t.Fatalf("wrong signer after replacement: %v", err)
		}
	}
	if writes != 0 {
		t.Fatal("prestage required an unbound write")
	}
}
