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
		denyAt          map[int]bool
		untrustedProbe  bool
		wantErr         bool
		want            [][]string
	}{
		{"root entry", "1", nil, false, false, [][]string{{"-n", "--", "/usr/bin/true"}, {"-n", "--", "/usr/bin/cat", "--", "/dev/null"}}},
		{"root entry without grant", "1", map[int]bool{1: true}, false, true, [][]string{{"-n", "--", "/usr/bin/true"}}},
		{"operation denied after probe", "1", map[int]bool{2: true}, false, true, [][]string{{"-n", "--", "/usr/bin/true"}, {"-n", "--", "/usr/bin/cat", "--", "/dev/null"}}},
		{"untrusted probe", "1", nil, true, true, nil},
		{"untrusted ordinary probe", "", nil, true, true, nil},
		{"ordinary passwordless user", "", nil, false, false, [][]string{{"-n", "--", "/usr/bin/true"}, {"-n", "--", "/usr/bin/cat", "--", "/dev/null"}}},
		{"ordinary passworded user", "", map[int]bool{1: true}, false, false, [][]string{{"-n", "--", "/usr/bin/true"}, {"-v"}, {"-n", "--", "/usr/bin/cat", "--", "/dev/null"}}},
		{"ordinary passworded user cancelled", "", map[int]bool{1: true, 2: true}, false, true, [][]string{{"-n", "--", "/usr/bin/true"}, {"-v"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FASED_INSTALL_ROOT_ENTRY", tc.rootEntry)
			var calls [][]string
			s := &System{Out: io.Discard, sudoCommand: func(ctx context.Context, args ...string) *exec.Cmd {
				calls = append(calls, append([]string(nil), args...))
				if tc.denyAt[len(calls)] {
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
			if (err != nil) != tc.wantErr || !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("authorization calls=%q, err=%v; want %q, error=%t", calls, err, tc.want, tc.wantErr)
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
