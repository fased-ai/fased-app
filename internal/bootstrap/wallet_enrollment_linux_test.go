//go:build linux

package bootstrap

import (
	"bytes"
	"encoding/base64"
	"github.com/fased-ai/fased-app/internal/bundle"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWalletEnrollmentUsesAuthenticatedPairAndExistingController(t *testing.T) {
	instance := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	for _, kind := range []string{"complete", "missing-signer", "changed-signer", "wrong-instance", "wrong-profile", "wrong-payload-source", "wrong-payload-version"} {
		t.Run(kind, func(t *testing.T) {
			mode := "wallets"
			if kind == "missing-signer" {
				mode = "no-wallets"
			}
			f := newFirstFixture(t, mode)
			p, err := Prepare(t.Context(), f.options())
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			payload := &WalletPayload{Data: []byte("inert disposable signer"), Version: f.descriptor.ProductVersion, Source: f.descriptor.Source}
			payload.SHA256 = digest(payload.Data)
			opts := InstallOptions{WalletSigner: payload, Start: true, Profile: "remote", WalletInstance: instance, WalletNetwork: "devnet"}
			switch kind {
			case "missing-signer":
				opts.WalletSigner = nil
			case "changed-signer":
				payload.Data = []byte("substituted")
			case "wrong-payload-source":
				payload.Source = "foreign"
			case "wrong-payload-version":
				payload.Version = "foreign"
			case "wrong-instance":
				opts.WalletInstance = "foreign"
			case "wrong-profile":
				opts.Profile = "local"
			}
			err = p.Run(t.Context(), opts, nil, io.Discard, io.Discard)
			if kind != "complete" {
				if err == nil {
					t.Fatal("invalid pair/intent executed")
				}
				if _, e := os.Stat(f.marker); !os.IsNotExist(e) {
					t.Fatal("refused setup executed code")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(f.arguments)
			if err != nil {
				t.Fatal(err)
			}
			args := string(b)
			for _, want := range []string{"custody\nenroll\n--ordinary-custody\n--source\nremote\n", "--expected-instance\n" + instance + "\n", "--network\ndevnet\n", "--signer-binary\n" + filepath.Join(p.options.Directory, "wallet-signer") + "\n", "--disposable-fixture\n--prepare-host\n--start-existing\n"} {
				if !strings.Contains(args, want) {
					t.Fatal("handoff lost reviewed binding", want, args)
				}
			}
			// The normal archive and its extracted inventory remain unchanged, so
			// older UI/CLI updaters never need to parse a new signer inventory entry.
			if digest(f.archive) != p.report.ArchiveSHA256 {
				t.Fatal("ordinary archive changed")
			}
			m, e := bundle.Verify(p.report.BundleDirectory)
			if e != nil || m.Files["fased-signer"].SHA256 != "" {
				t.Fatal("signer leaked into ordinary update archive", e)
			}
			if strings.Contains(args, "--interactive") || strings.Contains(args, "--start=false") {
				t.Fatal("wallet setup became installation/update wizard", args)
			}
			if err = p.Run(t.Context(), opts, nil, io.Discard, io.Discard); err == nil {
				t.Fatal("same prepared execution replayed")
			}
		})
	}
}
