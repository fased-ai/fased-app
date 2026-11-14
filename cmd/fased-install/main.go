// Independent initial verifier. No public seed, endpoint, key or activation is
// compiled in; first-install trust must be supplied independently.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"os"
	"path/filepath"
	"time"

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

// Tests can substitute a nonprivileged receiver; production keeps the fixed
// installed maintenance entry and has no runtime override.
var candidateMaintenanceStage = stageAttestedCandidate

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
	if len(args) > 0 {
		switch args[0] {
		case "maintenance-successor":
			return maintenanceSuccessorCommand(args[1:])
		case "maintenance-transition-status":
			return maintenanceTransitionStatusCommand(args[1:], out)
		case "maintenance-worker", "maintenance-recover", "maintenance-gate", "maintenance-owner-record":
			return maintenanceCustodyCommand(args[0], args[1:], out)
		case "maintenance-peer", "maintenance-app":
			return maintenancePeerCommand(args[1:], in, out, args[0] == "maintenance-app")
		}
	}
	if len(args) > 0 && args[0] == "maintenance-register" {
		return maintenanceRegisterCommand(args[1:], out)
	}
	if len(args) > 0 && args[0] == "maintenance-bootstrap" {
		return maintenanceBootstrapCommand(args[1:], errOut)
	}
	if len(args) > 0 && args[0] == "maintenance-request" {
		return maintenanceRequestCommand(args[1:], in, out)
	}
	if len(args) == 1 && args[0] == "--emit-embedded-signer" {
		if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
			return errors.New("embedded signer extraction requires an unprivileged process")
		}
		return bootstrap.WriteWalletPayload(out, walletPayload())
	}
	if len(args) == 1 && args[0] == "--emit-embedded-native" {
		if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
			return errors.New("embedded native extraction requires an unprivileged process")
		}
		return bootstrap.WriteWalletPayload(out, walletDesktopPayload())
	}
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
	walletInstance := f.String("wallet-instance", "", "exact existing Remote workspace for optional Wallets enrollment")
	walletNetwork := f.String("wallet-network", "", "network for optional protected Wallets enrollment")
	entryAction := f.String("entry-action", "", "fixed installed-instance action for the entry launcher")
	verifyCandidate := f.String("verify-candidate", "", "verify a retained release candidate without installation")
	installCandidate := f.String("install-candidate", "", "install one independently attested Local candidate")
	stageCandidate := f.String("stage-candidate", "", "admit one independently attested manual update candidate")
	candidatePacket := f.String("candidate-packet-sha256", "", "exact reviewed candidate packet digest")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected installer arguments")
	}
	if *stageCandidate != "" {
		mixed := false
		f.Visit(func(flag *flag.Flag) {
			if flag.Name != "stage-candidate" && flag.Name != "candidate-packet-sha256" {
				mixed = true
			}
		})
		digest, e := hex.DecodeString(*candidatePacket)
		if mixed || !filepath.IsAbs(*stageCandidate) || filepath.Clean(*stageCandidate) != *stageCandidate || len(digest) != 32 || hex.EncodeToString(digest) != *candidatePacket || e != nil || compiledFirstInstallBinding == nil {
			return errors.New("manual candidate update requires its exact packet and compiled installer trust")
		}
		baseline, e := candidateUpdateBaseline()
		if e != nil {
			return e
		}
		cache, e := os.UserCacheDir()
		if e != nil {
			return e
		}
		options, e := retailInstallerOptions(compiledFirstInstallBinding, cache)
		if e != nil {
			return e
		}
		admission := bootstrap.CandidateAdmission{PacketSHA256: *candidatePacket, OperationID: update.NewProbe(), BaselineSHA256: baseline, ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}
		p, e := bootstrap.PrepareAttestedCandidate(context.Background(), options, *stageCandidate, admission)
		if e != nil {
			return e
		}
		defer p.Close()
		if e = candidateMaintenanceStage(context.Background(), p, *candidatePacket, "update", errOut); e != nil {
			return e
		}
		p.RetainAcquisitionEvidence()
		_, e = fmt.Fprintln(out, "Candidate ready for manual Update.")
		return e
	}
	if *installCandidate != "" || *candidatePacket != "" {
		mixed := false
		f.Visit(func(flag *flag.Flag) {
			switch flag.Name {
			case "install-candidate", "candidate-packet-sha256", "profile", "prefix", "data-dir", "start", "listen":
			default:
				mixed = true
			}
		})
		digest, e := hex.DecodeString(*candidatePacket)
		if mixed || !filepath.IsAbs(*installCandidate) || filepath.Clean(*installCandidate) != *installCandidate || len(digest) != 32 || hex.EncodeToString(digest) != *candidatePacket || e != nil || *profile != "local" || compiledFirstInstallBinding == nil {
			return errors.New("fresh Local candidate requires its exact packet and compiled installer trust")
		}
		installOptions := bootstrap.InstallOptions{WalletSigner: walletPayload(), WalletDesktop: walletDesktopPayload(), Prefix: *prefix, DataDirectory: *data, Start: *start, Profile: "local", Listen: *listen}
		paths, e := bootstrap.FreshCandidatePaths(installOptions)
		if e != nil {
			return e
		}
		cache, e := os.UserCacheDir()
		if e != nil {
			return e
		}
		options, e := retailInstallerOptions(compiledFirstInstallBinding, cache)
		if e != nil {
			return e
		}
		admission := bootstrap.CandidateAdmission{PacketSHA256: *candidatePacket, OperationID: update.NewProbe(), FreshProfile: "local", FreshOwner: os.Getuid(), FreshRoot: paths.Root, FreshData: paths.Data, ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}
		p, e := bootstrap.PrepareAttestedCandidate(context.Background(), options, *installCandidate, admission)
		if e != nil {
			return e
		}
		defer p.Close()
		if e = p.VerifyFreshCandidateInstaller(installOptions); e != nil {
			return e
		}
		if e = candidateMaintenanceStage(context.Background(), p, *candidatePacket, "fresh", errOut); e != nil {
			return e
		}
		p.RetainAcquisitionEvidence()
		return p.Run(context.Background(), installOptions, in, out, errOut)
	}
	if *verifyCandidate != "" {
		mixed := false
		f.Visit(func(flag *flag.Flag) {
			if flag.Name != "verify-candidate" {
				mixed = true
			}
		})
		if mixed || compiledFirstInstallBinding == nil {
			return errors.New("candidate preflight requires only compiled installer trust")
		}
		report, err := bootstrap.VerifyAttestedCandidate(bootstrap.Options{Seed: compiledFirstInstallBinding.Seed, SeedSHA256: compiledFirstInstallBinding.SHA256}, *verifyCandidate)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(report)
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
	if *entryAction == "wallets" {
		if !*install || !*start || *upgrade || *profile != "remote" || !bootstrap.ValidWalletEnrollment(*walletInstance, *walletNetwork) || *prefix != "" || *data != "" {
			return errors.New("Wallets setup requires the existing Remote workspace and network")
		}
	} else if *walletInstance != "" || *walletNetwork != "" {
		return errors.New("wallet options require the Wallets setup action")
	}
	entryProfile := *profile
	retailEntry := *entryAction != "" || *profile != ""
	if *entryAction != "" && *entryAction != "wallets" {
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
		if err = p.Run(context.Background(), bootstrap.InstallOptions{WalletSigner: walletPayload(), WalletDesktop: walletDesktopPayload(), WalletInstance: *walletInstance, WalletNetwork: *walletNetwork, Prefix: *prefix, DataDirectory: *data, Start: *start, Profile: *profile, Listen: *listen}, in, out, errOut); err != nil {
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
