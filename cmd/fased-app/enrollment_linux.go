//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

func enrollmentCommand(args []string, in io.Reader, out, errOut io.Writer) error {
	f := flag.NewFlagSet("custody enroll", flag.ContinueOnError)
	f.SetOutput(errOut)
	mode := f.String("source", "", "owned installation: local or remote")
	expected := f.String("expected-instance", "", "exact current instance identity")
	network := f.String("network", "", "disposable key network: devnet or mainnet-beta")
	signerBinary := f.String("signer-binary", "", "matching engineering signer binary")
	prepare := f.Bool("prepare-host", false, "authorize protected ownership cutover")
	disposable := f.Bool("disposable-fixture", false, "disposable engineering enrollment only")
	defaults, e := localinstall.DefaultPaths()
	if e != nil {
		return e
	}
	prefix := f.String("prefix", defaults.Root, "existing Local installation directory")
	data := f.String("data-dir", defaults.Data, "existing Local instance directory")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || !*prepare || !*disposable || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || (*mode != "local" && *mode != "remote") || *expected == "" || (*network != "devnet" && *network != "mainnet-beta") || *signerBinary == "" {
		return errors.New("choose an installed source, exact instance, network and signer; explicit disposable/host consent required")
	}
	if *mode == "remote" && (*prefix != defaults.Root || *data != defaults.Data) {
		return errors.New("Local paths cannot select a Remote source")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	system := &custodyinstall.System{In: in, Out: errOut}
	source, lease, e := enrollmentSource(ctx, *mode, *expected, localinstall.Paths{Root: *prefix, Data: *data}, system)
	if e != nil {
		return e
	}
	defer lease.Close()
	binary, e := os.Executable()
	if e != nil {
		return e
	}
	key, e := filepath.Abs(*signerBinary)
	if e != nil {
		return e
	}
	result, e := enrollment.Run(ctx, source, enrollment.Options{Core: binary, Signer: key, Network: *network, PrepareHost: *prepare, Disposable: *disposable}, system)
	if e != nil {
		return e
	}
	origin := result.PublicOrigin
	if origin == "" {
		origin = "http://" + result.Listen
	}
	return json.NewEncoder(out).Encode(struct {
		InstanceID         string `json:"instanceId"`
		URL                string `json:"url"`
		FixtureOnly        bool   `json:"fixtureOnly"`
		WalletSigner       bool   `json:"walletSigner"`
		FinancialExecution bool   `json:"financialExecution"`
	}{*expected, origin, true, false, false})
}

func enrollmentSource(ctx context.Context, mode, expected string, paths localinstall.Paths, system *custodyinstall.System) (enrollment.Source, io.Closer, error) {
	var source enrollment.Source
	if mode == "local" {
		// Discovery never creates a missing source installation or instance.
		if _, e := os.Lstat(filepath.Join(paths.Root, "install.json")); e != nil {
			return source, nil, e
		}
		m, lease, e := localinstall.EnrollmentLease(paths)
		if e != nil {
			return source, nil, e
		}
		status, e := localinstall.Status(ctx, paths)
		if e != nil {
			lease.Close()
			return source, nil, e
		}
		profile := m.Location
		if profile == "" {
			profile = "local"
		}
		source = enrollment.Source{Profile: profile, Data: m.Data, UID: os.Getuid(), GID: os.Getgid(), Listen: m.Listen, PublicOrigin: m.PublicOrigin, ExpectedInstanceID: expected, Running: status.Active, Autostart: status.Autostart}
		source.Validate = func(context.Context) error { return localinstall.ValidateEnrollment(m) }
		source.Suspend = func(c context.Context) error { return localinstall.SuspendEnrollment(c, m) }
		source.Restore = func(c context.Context, running, autostart bool) error {
			return localinstall.RestoreEnrollment(c, m, running, autostart)
		}
		return source, lease, nil
	}
	m, e := vpsinstall.Load(os.Getuid())
	if e != nil {
		return source, nil, e
	}
	lease, e := vpsinstall.EnrollmentLease(ctx, m, system)
	if e != nil {
		return source, nil, e
	}
	status, e := vpsinstall.EnrollmentStatus(ctx, m, system)
	if e != nil {
		lease.Close()
		return source, nil, e
	}

	autostart, e := vpsinstall.EnrollmentAutostart(m)
	if e != nil {
		lease.Close()
		return source, nil, e
	}
	source = enrollment.Source{Profile: "remote", Data: m.Data, UID: m.AppUID, GID: m.AppGID, Listen: m.Listen, PublicOrigin: m.PublicOrigin, ExpectedInstanceID: expected, Running: status.Active, Autostart: autostart}
	source.Validate = func(c context.Context) error { return vpsinstall.ValidateEnrollment(c, m, system) }
	source.Suspend = func(c context.Context) error { return vpsinstall.SuspendEnrollment(c, m, system) }
	source.Restore = func(c context.Context, running, autostart bool) error {
		return vpsinstall.RestoreEnrollment(c, m, running, autostart, system)
	}
	return source, lease, nil
}
