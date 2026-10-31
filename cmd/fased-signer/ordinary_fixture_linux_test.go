//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
)

// Only this compiled test executable binds controlled owner provenance. The
// ordinary product binary has no runtime switch for this server constructor.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-test.") {
		if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
			os.Exit(1)
		}
		if err := ordinaryFixtureCommand(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}
func ordinaryFixtureCommand(args []string) error {
	if len(args) == 1 && args[0] == "ordinary-custody-version" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"protocol": 3, "ownerFlow": 1, "mode": signer.OrdinaryCustodyMode, "profile": signer.OwnerCustodyProfile, "provenance": signer.ControlledCustodyProfile, "walletSigner": false})
	}
	if len(args) > 0 && args[0] == "serve" {
		f := flag.NewFlagSet("controlled ordinary signer", flag.ContinueOnError)
		config := f.String("config", "", "")
		fixture := f.Bool("disposable-fixture", false, "")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if !*fixture || f.NArg() != 0 {
			return errors.New("explicit controlled config required")
		}
		c, err := signer.LoadConfig(*config)
		if err != nil {
			return err
		}
		if c.Version == 3 {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			server, err := signer.ServeControlledOwnerCustody(ctx, c)
			if err != nil {
				return err
			}
			defer server.Close()
			fmt.Fprintf(os.Stdout, "Disposable signer ready; pid=%d uid=%d; real wallets and signing unavailable.\n", os.Getpid(), os.Geteuid())
			<-ctx.Done()
			return nil
		}
	}
	return run(args, os.Stdin, os.Stdout, os.Stderr)
}
