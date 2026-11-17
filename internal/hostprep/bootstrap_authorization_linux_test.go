//go:build linux

package hostprep

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBootstrapAuthorizationDoesNotDependOnSudoDefaults(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("authorization runs as an ordinary user")
	}
	for _, tc := range []struct {
		name, rootEntry string
		denyAt          int
		untrustedProbe  bool
		want            [][]string
	}{
		{"root entry", "1", 0, false, [][]string{{"-n", "--", "/usr/bin/true"}, {"-n", "--", "/usr/bin/cat", "--", "/dev/null"}}},
		{"root entry without grant", "1", 1, false, [][]string{{"-n", "--", "/usr/bin/true"}}},
		{"operation denied after probe", "1", 2, false, [][]string{{"-n", "--", "/usr/bin/true"}, {"-n", "--", "/usr/bin/cat", "--", "/dev/null"}}},
		{"untrusted probe", "1", 0, true, nil},
		{"ordinary user", "", 0, false, [][]string{{"-v"}, {"-n", "--", "/usr/bin/cat", "--", "/dev/null"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FASED_INSTALL_ROOT_ENTRY", tc.rootEntry)
			var calls [][]string
			s := &System{Out: io.Discard, sudoCommand: func(ctx context.Context, args ...string) *exec.Cmd {
				calls = append(calls, append([]string(nil), args...))
				if len(calls) == tc.denyAt {
					return exec.CommandContext(ctx, "/usr/bin/false")
				}
				return exec.CommandContext(ctx, "/usr/bin/true")
			}}
			if tc.untrustedProbe {
				s.toolCheck = func(path string) error {
					if path == "/usr/bin/true" {
						return errors.New("foreign probe")
					}
					return nil
				}
			}
			_, err := s.Run(t.Context(), nil, "/usr/bin/cat", "--", "/dev/null")
			wantError := tc.denyAt != 0 || tc.untrustedProbe
			if (err != nil) != wantError || !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("authorization calls=%q, err=%v; want %q, error=%t", calls, err, tc.want, wantError)
			}
		})
	}
}

func TestTrustedHostToolRefusesOwnerControlledExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foreign")
	if err := os.WriteFile(path, []byte("foreign"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := trustedHostTool(path); err == nil {
		t.Fatal("owner-controlled executable accepted as an OS tool")
	}
}
