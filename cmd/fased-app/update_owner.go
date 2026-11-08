package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/term"
)

type coreProbe func(context.Context, string) (update.CoreSnapshot, error)
type updateRPC func(context.Context, signer.Request) (signer.Response, error)

func conductUpdate(ctx context.Context, id string, manifest, trust []byte, artifacts string, probe coreProbe, call updateRPC, confirm func(string) error, review, out io.Writer) error {
	if !update.IDOK(id) {
		return errors.New("one immutable intent ID required")
	}
	challenge := update.NewProbe()
	c, e := probe(ctx, challenge)
	if e != nil {
		return e
	}
	r, e := call(ctx, signer.Request{Action: "fixture-update-inventory", ProbeID: challenge})
	if e != nil {
		return e
	}
	if r.UpdateInventory == nil {
		return errors.New("signer inventory unavailable")
	}
	s := *r.UpdateInventory
	if s.ActiveIntentID != "" {
		return errors.New("another update intent owns the exclusive slot")
	}
	inv, e := update.JoinSnapshots(c, s, update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH})
	if e != nil {
		return e
	}
	plan, e := update.Inspect(manifest, trust, mustJSON(inv), artifacts, time.Now().Unix())
	if e != nil {
		return e
	}
	var envelope update.Envelope
	_ = update.StrictJSON(manifest, &envelope)
	var m update.Manifest
	_ = update.StrictJSON(envelope.Payload, &m)
	binding := struct {
		ID       string
		Manifest update.Manifest
		Core     update.CoreSnapshot
		Signer   update.SignerSnapshot
	}{id, m, c, s}
	digest := update.SnapshotDigest("owner-intent-review", binding)
	b, _ := json.MarshalIndent(binding, "", "  ")
	fmt.Fprintf(review, "DISPOSABLE DEVELOPMENT UPDATE INTENT ONLY\nNo installation or resume is authorized. Preparation locks the fixture wallet and reserves a non-rewinding sequence. Review artifact identities, state anchors, spent/held units and recovery holds.\n%s\nManifest digest: %s\nReview digest: %s\n", b, plan.ManifestSHA256, digest)
	if e = confirm("PREPARE " + digest); e != nil {
		return e
	}
	// Metadata may change during review. Later installation must additionally
	// pause and revalidate it in an exclusive staging transaction.
	c2, e := probe(ctx, challenge)
	if e != nil {
		return e
	}
	if update.SnapshotDigest("core", c2) != update.SnapshotDigest("core", c) {
		return errors.New("core metadata changed during review; start a fresh review")
	}
	request := update.IntentRequest{ID: id, Core: c, Signer: s, Manifest: manifest, Trust: trust}
	r, e = call(ctx, signer.Request{Action: "fixture-update-prepare", Update: &request})
	if e != nil {
		return e
	}
	if r.UpdateIntent == nil {
		return errors.New("missing durable intent receipt")
	}
	return json.NewEncoder(out).Encode(struct {
		Intent             *update.Intent `json:"intent"`
		InstallAllowed     bool           `json:"installAllowed"`
		ResumeAllowed      bool           `json:"resumeAllowed"`
		FinancialExecution bool           `json:"financialExecution"`
	}{Intent: r.UpdateIntent})
}

func mustJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}

type ownerUpdateOptions struct {
	Action, Config, ID, Manifest, Trust, Artifacts string
	Disposable, Prepare, Approval                  bool
}

func parseOwnerUpdateOptions(args []string, errOut io.Writer) (ownerUpdateOptions, error) {
	var o ownerUpdateOptions
	f := flag.NewFlagSet("owner-updates", flag.ContinueOnError)
	f.SetOutput(errOut)
	f.StringVar(&o.Action, "action", "inventory", "owner update action")
	f.StringVar(&o.Config, "config", "", "root-owned managed identity")
	f.BoolVar(&o.Disposable, "disposable-fixture", false, "test-only custody")
	f.BoolVar(&o.Prepare, "prepare-host", false, "fixed OS identity switch")
	f.BoolVar(&o.Approval, "approval-stdin", false, "disposable fixture automation")
	f.StringVar(&o.ID, "intent-id", "", "immutable update identity")
	f.StringVar(&o.Manifest, "manifest", "", "signed development manifest")
	f.StringVar(&o.Trust, "trust-file", "", "public development trust")
	f.StringVar(&o.Artifacts, "artifact-dir", "", "matching binaries")
	if e := f.Parse(args); e != nil {
		return o, e
	}
	if f.NArg() != 0 || !o.Disposable || !o.Prepare {
		return o, errors.New("explicit disposable fixture and OS helper consent required")
	}
	switch o.Action {
	case "inventory", "prepare", "stage", "preflight", "backup", "abort", "install", "install-status", "rollback", "complete":
	default:
		return o, errors.New("unsupported owner update action")
	}
	return o, nil
}

func ownerUpdatesCommand(args []string, in *os.File, out, errOut io.Writer) error {
	options, err := parseOwnerUpdateOptions(args, errOut)
	if err != nil {
		return err
	}
	action, config, approvalStdin, id, mp, tp, dir := &options.Action, &options.Config, &options.Approval, &options.ID, &options.Manifest, &options.Trust, &options.Artifacts

	if *action == "install" || *action == "install-status" || *action == "rollback" || *action == "complete" {
		return ownerInstallation(*action, *id, *config, *dir, *approvalStdin, in, out, errOut)
	}
	ownerLock, e := custodyinstall.OwnerLock()
	if e != nil {
		return e
	}
	defer ownerLock.Close()
	cfg, e := signer.LoadConfig(*config)
	if e != nil {
		return e
	}
	if e = signer.CheckOwner(cfg); e != nil {
		return e
	}
	m, e := custodyinstall.Load(cfg.OwnerUID)
	if e != nil {
		return e
	}
	if *config != filepath.Join(m.Root, "identity.json") {
		return errors.New("configured installation mismatch")
	}
	if e = custodyinstall.Verify(m); e != nil {
		return e
	}
	system := &custodyinstall.System{In: in, Out: errOut}
	ctx := context.Background()
	probe := func(ctx context.Context, p string) (update.CoreSnapshot, error) {
		var b bytes.Buffer
		if e := system.UpdateInventory(ctx, m, p, &b); e != nil {
			return update.CoreSnapshot{}, e
		}
		var c update.CoreSnapshot
		e := update.StrictJSON(b.Bytes(), &c)
		if e == nil && c.ProbeID != p {
			e = errors.New("core challenge mismatch")
		}
		return c, e
	}
	control := func(ctx context.Context, command update.CorePauseCommand) (update.CorePauseReceipt, error) {
		var b bytes.Buffer
		if e := system.UpdatePause(ctx, m, command, &b); e != nil {
			return update.CorePauseReceipt{}, e
		}
		var p update.CorePauseReceipt
		e := update.StrictJSON(b.Bytes(), &p)
		if e == nil && p.Core.ProbeID != command.ProbeID {
			e = errors.New("core pause challenge mismatch")
		}
		return p, e
	}
	call := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		return signer.Call(ctx, cfg, true, r)
	}
	reader := bufio.NewReader(in)
	confirm := func(expected string) error {
		if !*approvalStdin && !term.IsTerminal(int(in.Fd())) {
			return errors.New("protected update review requires an owner terminal")
		}
		fmt.Fprintf(errOut, "Type %s, or anything else to cancel: ", expected)
		line, e := reader.ReadString('\n')
		if e != nil || line != expected+"\n" {
			return errors.New("update review cancelled")
		}
		return nil
	}
	switch *action {
	case "inventory":
		p := update.NewProbe()
		c, e := probe(ctx, p)
		if e != nil {
			return e
		}
		r, e := call(ctx, signer.Request{Action: "fixture-update-inventory", ProbeID: p})
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(struct {
			Core               update.CoreSnapshot    `json:"core"`
			Signer             *update.SignerSnapshot `json:"signer"`
			InstallAllowed     bool                   `json:"installAllowed"`
			FinancialExecution bool                   `json:"financialExecution"`
		}{Core: c, Signer: r.UpdateInventory})
	case "prepare":
		mb, e := update.ReadDocument(*mp)
		if e != nil {
			return e
		}
		tb, e := update.ReadDocument(*tp)
		if e != nil {
			return e
		}
		return conductUpdate(ctx, *id, mb, tb, *dir, probe, call, confirm, errOut, out)
	case "stage":
		return conductStage(ctx, *id, *dir, probe, call, out)
	case "preflight":
		return conductPreflight(ctx, *id, control, call, out)
	case "backup":
		backup := func(ctx context.Context, p update.Preflight) (update.RecoveryIndex, error) {
			var b bytes.Buffer
			if e := system.UpdateBackup(ctx, m, p, &b); e != nil {
				return update.RecoveryIndex{}, e
			}
			var i update.RecoveryIndex
			e := update.StrictJSON(b.Bytes(), &i)
			return i, e
		}
		return conductBackup(ctx, *id, control, backup, call, out)
	case "abort":
		if !update.IDOK(*id) {
			return errors.New("exact intent ID required")
		}
		r, e := call(ctx, signer.Request{Action: "fixture-update-status", IntentID: *id})
		if e != nil {
			return e
		}
		if r.UpdateIntent == nil {
			return errors.New("intent receipt unavailable")
		}
		b, _ := json.MarshalIndent(r.UpdateIntent, "", "  ")
		fmt.Fprintf(errOut, "Abort only the update slot. Sequence floor, spent/held units, recovery holds and wallet lock remain.\n%s\n", b)
		if e = confirm("ABORT " + *id + " " + update.SnapshotDigest("abort-review", r.UpdateIntent)); e != nil {
			return e
		}
		return abortAndRelease(ctx, *id, control, call, out)
	default:
		return errors.New("unsupported owner update action")
	}
}

// Delegate to the immutable owner binary, retaining its terminal.
func custodyUpdatesCommand(args []string, in *os.File, out, errOut io.Writer) error {
	if runtime.GOOS != "linux" || os.Getuid() <= 0 || len(args) == 0 {
		return errors.New("managed disposable Linux owner required")
	}
	if args[0] == "install" || args[0] == "install-status" || args[0] == "rollback" || args[0] == "complete" {
		return delegateInstallation(args, in, out, errOut)
	}
	command, e := custodyUpdateChildArgs(args, os.Getuid(), errOut)
	if e != nil {
		return e
	}
	m, e := custodyinstall.Load(os.Getuid())
	if e != nil {
		return e
	}
	if e = custodyinstall.Verify(m); e != nil {
		return e
	}
	if m.NativeOwnerAccess && m.Location == "local" {
		image, e := os.Executable()
		if e != nil {
			return e
		}
		system := &custodyinstall.System{In: in, Out: errOut}
		if e = system.PrepareNativeUpdateSession(context.Background(), m, image); e != nil {
			return e
		}
	}
	child := exec.Command(filepath.Join(m.Root, "fased-app"), command...)
	child.Stdin = in
	child.Stdout = out
	child.Stderr = errOut
	return child.Run()
}
func custodyUpdateChildArgs(args []string, owner int, errOut io.Writer) ([]string, error) {
	if len(args) == 0 || owner <= 0 {
		return nil, errors.New("one custody update action required")
	}
	if args[0] == "status" || args[0] == "preferences" {
		f := flag.NewFlagSet("custody updates "+args[0], flag.ContinueOnError)
		f.SetOutput(errOut)
		disposable, prepare, request := f.Bool("disposable-fixture", false, "disposable custody"), f.Bool("prepare-host", false, "OS helper consent"), f.Bool("request-stdin", false, "typed update preferences")
		if err := f.Parse(args[1:]); err != nil {
			return nil, err
		}
		if f.NArg() != 0 || !*disposable || !*prepare || (args[0] == "status" && *request) {
			return nil, errors.New("explicit disposable fixture and OS helper consent required")
		}
		action := "update-status"
		if args[0] == "preferences" {
			action = "update-preferences"
		}
		return append([]string{"custody", action}, args[1:]...), nil
	}
	command := append([]string{"owner-updates", "--action", args[0], "--config", filepath.Join(custodyinstall.Root(owner), "identity.json")}, args[1:]...)
	o, err := parseOwnerUpdateOptions(command[1:], errOut)
	if err != nil {
		return nil, err
	}
	if o.Action != args[0] || o.Config != filepath.Join(custodyinstall.Root(owner), "identity.json") {
		return nil, errors.New("fixed custody update action/config required")
	}
	return command, nil
}
