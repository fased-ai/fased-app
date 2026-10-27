//go:build linux

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/signer"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
	"io"
	"os"
)

// Offline maintainer surface, signer UID only; not an app/owner RPC route.
// Only encrypted bytes use private files. Stdout contains a public historical
// receipt, never key material, passwords, archive bytes or raw journal records.
func portableCommand(args []string, input *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(errOut)
	fixture := f.Bool("disposable-fixture", false, "")
	config := f.String("config", "", "")
	file := f.String("file", "", "")
	directory := f.String("directory", "", "")
	pin := f.String("snapshot-sha256", "", "")
	stdin := f.Bool("password-stdin", false, "disposable fixture automation only")
	if f.Parse(args[1:]) != nil || f.NArg() != 0 || !*fixture || *config == "" || *file == "" {
		return errors.New("explicit disposable offline recovery configuration and private archive file required")
	}
	if args[0] == "fixture-portable-export" && (*pin != "" || *directory != "") || args[0] == "fixture-portable-recover" && (*pin == "" || *directory == "") {
		return errors.New("export has no recovery options; recovery requires an independent fingerprint and absent quarantine destination")
	}
	c, e := signer.LoadConfig(*config)
	if e != nil {
		return e
	}
	if os.Getuid() != c.SignerUID || os.Geteuid() != c.SignerUID || os.Geteuid() == 0 {
		return errors.New("offline recovery requires the configured signer UID; owner/app channels cannot invoke it")
	}
	if e = signer.VerifyExecutable(); e != nil {
		return e
	}
	if e = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); e != nil {
		return e
	}
	if e = unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); e != nil {
		return e
	}
	if input == nil {
		return errors.New("protected password input required")
	}
	reader := bufio.NewReader(io.LimitReader(input, 4096))
	readPassword := func(label string, confirm bool) ([]byte, error) {
		read := func() ([]byte, error) {
			if *stdin {
				b, e := reader.ReadBytes('\n')
				if e != nil || len(b) < 2 || len(b) > 1025 {
					clear(b)
					return nil, errors.New("bounded disposable password line required")
				}
				return b[:len(b)-1], nil
			}
			if !term.IsTerminal(int(input.Fd())) {
				return nil, errors.New("offline recovery needs a protected terminal or explicit fixture stdin")
			}
			fmt.Fprint(errOut, label+": ")
			p, e := term.ReadPassword(int(input.Fd()))
			fmt.Fprintln(errOut)
			return p, e
		}
		p, e := read()
		if e != nil {
			clear(p)
			return nil, e
		}
		if confirm && !*stdin {
			other, e := read()
			defer clear(other)
			if e != nil || string(p) != string(other) {
				clear(p)
				return nil, errors.New("password confirmation differs")
			}
		}
		return p, nil
	}
	var sourcePassword []byte
	if args[0] == "fixture-portable-export" {
		sourcePassword, e = readPassword("Source vault password (verification only)", false)
		if e != nil {
			return e
		}
		defer clear(sourcePassword)
	}
	password, e := readPassword("Encrypted history password", args[0] == "fixture-portable-export")
	if e != nil {
		return e
	}
	defer clear(password)
	if args[0] == "fixture-portable-export" {
		if e = preflightOwnerFile(*file); e != nil {
			return e
		}
		b, receipt, e := signer.PackPortableSnapshot(c.StateDir, c.SignerID, sourcePassword, password)
		if e != nil {
			return e
		}
		defer clear(b)
		if e = writeOwnerFile(*file, b); e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(receipt)
	}
	b, e := readPrivateBoundedFile(*file, signer.MaxPortableSnapshot)
	if e != nil {
		return e
	}
	defer clear(b)
	receipt, e := signer.RestorePortableSnapshot(*directory, b, password, *pin)
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(receipt)
}
