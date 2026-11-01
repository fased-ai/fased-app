//go:build linux

package main

// This dispatcher exists ONLY in an explicitly compiled Go test executable.
// Ordinary product builds have no environment-driven interruption or consent.
import (
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

type acceptanceBoundaryWriter struct{}

func (acceptanceBoundaryWriter) Write(b []byte) (int, error) {
	n, e := os.Stderr.Write(b)
	phase := os.Getenv("FASED_TEST_BRIDGE_STOP")
	if phase != "" && strings.TrimSpace(string(b)) == "Inspection bridge boundary: "+phase {
		// Stop the actual immutable controller, not its terminal/delegating parent.
		fmt.Fprintln(os.Stderr, "Fixture acceptance stopped:", strconv.Itoa(os.Getpid()), phase)
		if err := syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
			return n, err
		}
		// The caller must not exit or execute another operation before stop.
		for {
			_ = syscall.Pause()
		}
	}
	return n, e
}
func TestMain(m *testing.M) {
	configureOrdinaryEnrollmentFixture()
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-test.") {
		if os.Getuid() == 0 || os.Geteuid() == 0 {
			fmt.Fprintln(os.Stderr, "test fixture refuses root")
			os.Exit(1)
		}
		var diagnostic io.Writer = os.Stderr
		self, _ := os.Executable()
		if handled, e := applicationUpdateVMRuntime(self, os.Args[1:]); handled || e != nil {
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				if errors.Is(e, core.ErrCoreUnavailable) {
					os.Exit(75)
				}
				os.Exit(1)
			}
			return
		}
		if handled, e := maintenanceAuthorityActorFixture(self, os.Args[1:]); handled {
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				if errors.Is(e, core.ErrCoreUnavailable) {
					os.Exit(75)
				}
				os.Exit(1)
			}
			return
		}
		// Delegating parents must never stop while forwarding a child's diagnostic.
		// Only the immutable owner runner that executes the controller has the barrier.
		root := fmt.Sprintf("/var/lib/fased-app-custody-%d/", os.Getuid())
		if strings.HasPrefix(filepath.Clean(self), root) {
			diagnostic = acceptanceBoundaryWriter{}
		}
		if e := run(os.Args[1:], os.Stdin, os.Stdout, diagnostic); e != nil {
			fmt.Fprintln(os.Stderr, "fased-app:", e)
			if errors.Is(e, core.ErrCoreUnavailable) {
				os.Exit(75)
			}
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}
