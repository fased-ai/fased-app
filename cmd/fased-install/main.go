// Independent initial verifier. No public seed, endpoint, key or activation is
// compiled in; first-install trust must be supplied independently.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/update"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "fased-install:", err)
		os.Exit(1)
	}
}

type firstInstallBinding struct {
	Seed   []byte
	SHA256 string
}

// Public verifier/root material only; nil keeps publication/activation unapplied.
// Publisher review supplies this Go binding, never a customer form or API key.
var compiledFirstInstallBinding *firstInstallBinding

func retailInstallerOptions(binding *firstInstallBinding, cache string) (bootstrap.Options, error) {
	if binding == nil || len(binding.Seed) == 0 {
		return bootstrap.Options{}, errors.New("No trusted installer release is published. Check the official Fased downloads.")
	}
	parent := filepath.Join(cache, "fased-app", "install")
	if e := core.EnsurePrivateDirectory(parent); e != nil {
		return bootstrap.Options{}, e
	}
	return bootstrap.Options{Seed: append([]byte(nil), binding.Seed...), SeedSHA256: binding.SHA256, Directory: filepath.Join(parent, "verified-"+update.NewProbe())}, nil
}
func run(args []string, in io.Reader, out, errOut io.Writer) error {
	f := flag.NewFlagSet("fased-install", flag.ContinueOnError)
	f.SetOutput(errOut)
	development := f.Bool("development", false, "explicit disposable-trust development bootstrap")
	seed := f.String("seed-file", "", "independently obtained first-install seed")
	pin := f.String("seed-sha256", "", "independently authenticated seed SHA-256")
	directory := f.String("output", "", "absent absolute output under private0700 parent")
	ca := f.String("tls-ca-file", "", "optional controlled HTTPS fixture CA")
	install := f.Bool("install", false, "after verification, explicitly open the existing installation wizard")
	upgrade := f.Bool("upgrade", false, "verify then upgrade an existing installation through protected maintenance")
	remote := f.Bool("remote", false, "upgrade the existing managed Remote installation")
	prefix := f.String("prefix", "", "optional explicit installation directory")
	data := f.String("data-dir", "", "optional explicit instance data directory")
	start := f.Bool("start", true, "start the user service after owner-reviewed installation")
	profile := f.String("profile", "", "preselected Local/Remote/Connect terminal setup")
	listen := f.String("listen", "", "optional fixed loopback listener for installation")
	receipt := f.String("receipt-file", "", "private bootstrap acquisition receipt")
	entryAction := f.String("entry-action", "", "fixed installed-instance action for the entry launcher")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected installer arguments")
	}
	if *install && *upgrade || *remote && !*upgrade || *upgrade && !*start {
		return errors.New("choose installation or an existing Local/Remote upgrade")
	}
	if *profile != "" && *profile != "remote" && *profile != "local" && *profile != "connect" || (*profile != "" || *listen != "") && *upgrade {
		return errors.New("profile/listen require a fresh Remote installation")
	}
	if *receipt != "" && (!*install || *profile == "" || *upgrade) {
		return errors.New("private entry receipt requires an installation profile")
	}
	entryProfile := *profile
	retailEntry := *entryAction != "" || *profile != ""
	if *entryAction != "" {
		if *profile == "" || !*install || *upgrade {
			return errors.New("entry actions require an installation profile")
		}
		handled, e := installedEntry(context.Background(), *profile, *entryAction, in, out, errOut)
		if errors.Is(e, errEntryUpgradeRequired) {
			if e = signInForEntryUpgrade(context.Background(), entryProfile, in, out, errOut); e != nil {
				return e
			}
			*install = false
			*upgrade = true
			*remote = entryProfile == "remote"
			*profile = ""
			*listen = ""
		} else if e != nil {
			return e
		}
		if handled {
			return nil
		}
	}
	var options bootstrap.Options
	if *seed == "" && *pin == "" && *directory == "" && !*development && *ca == "" {
		cache, e := os.UserCacheDir()
		if e != nil {
			return e
		}
		options, e = retailInstallerOptions(compiledFirstInstallBinding, cache)
		if e != nil {
			return e
		}
		if !*upgrade {
			*install = true
		}
	} else {
		if *seed == "" || *pin == "" || *directory == "" {
			return errors.New("advanced verification requires a complete independent seed/pin/output tuple")
		}
		b, e := update.ReadDocument(*seed)
		if e != nil {
			return e
		}
		var cert []byte
		if *ca != "" {
			cert, e = update.ReadDocument(*ca)
			if e != nil {
				return e
			}
			if len(cert) == 0 {
				return errors.New("empty fixture CA")
			}
		}
		options = bootstrap.Options{Development: *development, Seed: b, SeedSHA256: *pin, Directory: *directory, TLSCA: cert}
	}
	if !*install && !*upgrade && (*prefix != "" || *data != "" || !*start || *profile != "" || *listen != "") {
		return errors.New("installation options require explicit --install")
	}
	p, err := bootstrap.Prepare(context.Background(), options)
	if err != nil {
		return err
	}
	defer p.Close()
	if *receipt != "" {
		if err = core.WritePrivateJSON(*receipt, p.Report()); err != nil {
			return err
		}
	}
	if *upgrade {
		if err = p.RunUpgrade(context.Background(), bootstrap.UpgradeOptions{Prefix: *prefix, DataDirectory: *data, Remote: *remote}, in, out, errOut); err != nil {
			return err
		}
		if retailEntry {
			if err = completeInstalledEntry(entryProfile, in, out, errOut); err != nil {
				return err
			}
		}
	}

	if *install {
		if err = p.Run(context.Background(), bootstrap.InstallOptions{Prefix: *prefix, DataDirectory: *data, Start: *start, Profile: *profile, Listen: *listen}, in, out, errOut); err != nil {
			return err
		}
	}
	if *receipt != "" {
		if err = core.WritePrivateJSON(*receipt, p.Report()); err != nil {
			return err
		}
	}
	if retailEntry {
		return nil
	}
	return json.NewEncoder(out).Encode(p.Report())
}
