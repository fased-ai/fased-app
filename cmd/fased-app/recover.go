package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"golang.org/x/term"
	"io"
	"os"
)

func recoverCommand(args []string, in *os.File, out, errOut io.Writer) error {
	d, e := core.DefaultDataDir()
	if e != nil {
		return e
	}
	f := flag.NewFlagSet("recover", flag.ContinueOnError)
	f.SetOutput(errOut)
	dir := f.String("data-dir", d, "existing instance state")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || !term.IsTerminal(int(in.Fd())) {
		return errors.New("recovery requires the OS owner's terminal; passwords are never command arguments")
	}
	fmt.Fprintln(errOut, "The engine must be stopped. Recovery revokes all app sessions and preserves instance/settings. It grants no wallet authority.")
	fmt.Fprint(errOut, "New owner password: ")
	a, e := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(errOut)
	if e != nil {
		return e
	}
	fmt.Fprint(errOut, "Confirm new password: ")
	b, e := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(errOut)
	if e != nil || string(a) != string(b) {
		return errors.New("password confirmation differs")
	}
	if e = core.RecoverPassword(context.Background(), *dir, string(a)); e != nil {
		return e
	}
	fmt.Fprintln(out, "Owner password replaced; all app sessions revoked. Restart the engine and sign in.")
	return nil
}
