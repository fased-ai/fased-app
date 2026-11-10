//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestScopedUpdateMetadataUsesApprovedSource(t *testing.T) {
	owner := os.Getuid()
	id := strings.Repeat("a", 32)
	intent := strings.Repeat("b", 32)
	data := []byte(`{"version":1,"phase":"active"}`)
	root := Root(owner)
	source := ApprovedExecutablePath(owner, data)
	for _, target := range []string{root + "/update-active.json", root + "/updates/" + intent + "/transaction.json", root + "/updates/" + intent + "/healthy-held.json", root + "/bootstraps/" + intent + "/repair-source.json", root + "/.replace-" + id + "-install.json-" + sum(data)} {
		h := &memoryHost{files: map[string]hostFile{source: {Data: bytes.Clone(data), UID: 0, Mode: 0444, Regular: true}}}
		used := false
		run := func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
			if args[0] == "/usr/bin/install" {
				used = len(input) == 0 && args[len(args)-2] == source
			}
			return h.run(ctx, input, args...)
		}
		if err := publish(t.Context(), target, id, data, 0644, h.read, run); err != nil || !used || !known(h.files[target], data, 0644) {
			t.Fatalf("metadata source not bound for %s: %v", target, err)
		}
		h = &memoryHost{files: map[string]hostFile{}}
		reject := func(string, string, int64) error { return errors.New("approved metadata changed") }
		if err := publish(t.Context(), target, id, data, 0644, h.read, h.run, reject); err == nil || h.calls != 0 {
			t.Fatal("changed metadata reached privileged write", target)
		}
	}
	for _, foreign := range []string{Root(owner+1) + "/update-active.json", root + "/updates/" + intent + "/../held.json", root + "/.replace-" + id + "-install.json-" + strings.Repeat("0", 64), root + "/identity.json"} {
		if scopedUpdateMetadata(foreign, id, owner, data) {
			t.Fatal("foreign metadata destination accepted", foreign)
		}
	}
}
