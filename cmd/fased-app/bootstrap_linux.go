//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/term"
)

func bootstrapCommand(args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("custody bootstrap", flag.ContinueOnError)
	f.SetOutput(errOut)
	id := f.String("intent-id", "", "exact reader handoff identity")
	bundlePath := f.String("bundle", "", "signed predecessor/target development bundle")
	trustPath := f.String("trust-file", "", "explicit development public trust")
	artifacts := f.String("artifact-dir", "", "verified current pair")
	disposable := f.Bool("disposable-fixture", false, "unfunded test keys only")
	prepare := f.Bool("prepare-host", false, "fixed OS utilities only")
	automation := f.Bool("approval-stdin", false, "disposable fixture automation only")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || !*disposable || !*prepare || !update.IDOK(*id) || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		return errors.New("ordinary owner, exact id and explicit disposable bootstrap required")
	}
	runner, e := custodyinstall.BootstrapRunner(os.Getuid(), *id)
	if errors.Is(e, os.ErrNotExist) {
		runner, e = prepareBootstrapCommand(*id, *bundlePath, *trustPath, *artifacts, *automation, in, errOut)
	}
	if e != nil {
		return e
	}
	c := exec.Command(runner, "owner-bootstrap", "--intent-id", *id, "--disposable-fixture", "--prepare-host")
	c.Stdin = in
	c.Stdout = out
	c.Stderr = errOut
	return c.Run()
}
func prepareBootstrapCommand(id, bundlePath, trustPath, artifacts string, automation bool, in *os.File, review io.Writer) (string, error) {
	var bundle update.BootstrapBundle
	b, e := update.ReadDocument(bundlePath)
	if e != nil {
		return "", e
	}
	if update.StrictJSON(b, &bundle) != nil {
		return "", errors.New("invalid bounded bootstrap bundle")
	}
	trust, e := update.ReadDocument(trustPath)
	if e != nil {
		return "", e
	}
	now := time.Now().Unix()
	plan, e := update.VerifyBootstrap(bundle, trust, now)
	if e != nil {
		return "", e
	}
	m, e := custodyinstall.Load(os.Getuid())
	if e != nil {
		return "", e
	}
	if e = custodyinstall.Verify(m); e != nil {
		return "", e
	}
	path, e := os.Executable()
	if e != nil {
		return "", e
	}
	self, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}

	// StreamArtifact verifies fixed signed names, sizes and complete hashes.
	for _, c := range plan.Target.Components {
		if e = update.StreamArtifact(artifacts, c.Artifact, func(_ int64, _ []byte) error { return nil }); e != nil {
			return "", e
		}
	}
	// The caller is the new owner-selected executable; privileged code is never
	// sourced from it. Only this exact signed executable becomes the fixed helper.
	var targetCoreHash string
	for _, c := range plan.Target.Components {
		if c.Contract.Component == "core" {
			targetCoreHash = c.Artifact.SHA256
		}
	}
	if fileSHA(self) != targetCoreHash {
		return "", errors.New("run the exact signed target core to bootstrap an older helper")
	}

	// An owner-downloaded verifier cannot use the signer socket. The installed,
	// immutable predecessor owns that read; its executable/UID checks stay intact.
	var output bytes.Buffer
	command := exec.Command(m.Root+"/fased-app", "owner-updates", "--action", "inventory", "--config", m.Root+"/identity.json", "--disposable-fixture", "--prepare-host")
	command.Stdin = in
	command.Stdout = &output
	command.Stderr = review
	if e = command.Run(); e != nil {
		return "", e
	}
	core, snapshot, e := decodeBootstrapInventory(output.Bytes())
	if e != nil {
		return "", e
	}
	lock, e := custodyinstall.OwnerLock()
	if e != nil {
		return "", e
	}
	defer lock.Close()
	current, e := custodyinstall.Load(os.Getuid())
	if e != nil {
		return "", e
	}
	if current != m || custodyinstall.Verify(m) != nil {
		return "", errors.New("predecessor changed before bootstrap review")
	}
	system := &custodyinstall.System{In: in, Out: review}
	ctx := context.Background()
	tx := custodyinstall.BootstrapTransaction{Version: 1, ID: id, Base: m, Target: m, Bundle: bundle, Trust: trust, ReviewedAt: now, Core: core, Signer: snapshot}
	for _, c := range plan.Target.Components {
		if c.Contract.Component == "core" {
			tx.Target.CoreHash = c.Artifact.SHA256
		} else {
			tx.Target.SignerHash = c.Artifact.SHA256
		}
	}
	if _, e = tx.Plan(); e != nil {
		return "", e
	}
	pretty, _ := json.MarshalIndent(tx, "", "  ")
	want := "BOOTSTRAP " + tx.Digest()
	fmt.Fprintf(review, "DISPOSABLE READER BOOTSTRAP ONLY\nPreserve all financial state. Reserve a predecessor-readable maintenance sequence; install only the signed reader pair. On failed health, restore compatible readers and abort only the maintenance intent. No installed release, automatic unlock or resume.\n%s\nType %s, or anything else to cancel: ", pretty, want)
	if !automation && !term.IsTerminal(int(in.Fd())) {
		return "", errors.New("protected owner terminal required")
	}
	line, e := bufio.NewReader(in).ReadString('\n')
	if e != nil || line != want+"\n" {
		return "", errors.New("bootstrap review cancelled")
	}
	if e = custodyinstall.PrepareBootstrap(ctx, tx, artifacts, system); e != nil {
		return "", e
	}
	return custodyinstall.BootstrapRunner(os.Getuid(), id)
}

func ownerBootstrapCommand(args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("owner-bootstrap", flag.ContinueOnError)
	f.SetOutput(errOut)
	id := f.String("intent-id", "", "exact bootstrap id")
	disposable := f.Bool("disposable-fixture", false, "test-only")
	prepare := f.Bool("prepare-host", false, "fixed OS consent")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || !*disposable || !*prepare {
		return errors.New("explicit disposable bootstrap required")
	}
	tx, e := custodyinstall.LoadBootstrap(os.Getuid(), *id)
	if e != nil {
		return e
	}
	lock, e := custodyinstall.OwnerLock()
	if e != nil {
		return e
	}
	defer lock.Close()
	cfg, e := signer.LoadConfig(tx.Base.Root + "/identity.json")
	if e != nil {
		return e
	}
	if e = signer.CheckOwner(cfg); e != nil {
		return e
	}
	system := &custodyinstall.System{In: in, Out: errOut}
	ctx := context.Background()
	call := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		return signer.Call(ctx, cfg, true, r)
	}
	control := func(ctx context.Context, c update.CorePauseCommand) (update.CorePauseReceipt, error) {
		m, e := custodyinstall.Load(os.Getuid())
		if e != nil {
			return update.CorePauseReceipt{}, e
		}
		var b bytes.Buffer
		if e = system.UpdatePause(ctx, m, c, &b); e != nil {
			return update.CorePauseReceipt{}, e
		}
		var p update.CorePauseReceipt
		e = update.StrictJSON(b.Bytes(), &p)
		return p, e
	}
	probe := func(ctx context.Context, id string) (update.CoreSnapshot, error) {
		m, e := custodyinstall.Load(os.Getuid())
		if e != nil {
			return update.CoreSnapshot{}, e
		}
		var b bytes.Buffer
		if e = system.UpdateInventory(ctx, m, id, &b); e != nil {
			return update.CoreSnapshot{}, e
		}
		var c update.CoreSnapshot
		e = update.StrictJSON(b.Bytes(), &c)
		return c, e
	}
	readState := func(ctx context.Context) (signer.Response, error) {
		r, e := call(ctx, signer.Request{Action: "fixture-update-status", IntentID: tx.ID})
		if e == nil {
			s, se := call(ctx, signer.Request{Action: "fixture-update-inventory", ProbeID: update.NewProbe()})
			if se == nil {
				r.UpdateInventory = s.UpdateInventory
				r.Status = s.Status
				return r, nil
			}
			e = se
		}
		b, se := system.Run(ctx, nil, "/usr/bin/systemctl", "show", custodyinstall.Unit(tx.Base.Owner, "signer"), "--property=ActiveState,MainPID")
		if se != nil {
			return r, se
		}
		if string(b) != "ActiveState=inactive\nMainPID=0\n" && string(b) != "MainPID=0\nActiveState=inactive\n" {
			return r, e
		}
		identity := tx.Base.Signer
		b, se = system.Run(ctx, nil, "/usr/bin/setpriv", "--reuid", strconv.Itoa(identity.UID), "--regid", strconv.Itoa(identity.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", tx.Dir()+"/candidate/fased-signer", "fixture-update-inspect", "--config", tx.Base.Root+"/identity.json", "--disposable-fixture", "--intent-id", tx.ID)
		if se != nil {
			return r, se
		}
		if update.StrictJSON(b, &r) != nil {
			return r, errors.New("invalid actual cold bootstrap state")
		}
		return r, nil
	}
	validateIntent := func(i *update.Intent) error {
		p, e := tx.Plan()
		if e != nil {
			return e
		}
		if i == nil || i.ID != tx.ID || i.Sequence != p.Maintenance.Sequence || i.ManifestSHA256 != p.MaintenanceSHA256 || !update.SameCoreMetadata(i.Core, tx.Core) {
			return errors.New("bootstrap maintenance identity changed")
		}
		a, b := i.Signer, tx.Signer
		a.BootID, a.ProbeID, b.BootID, b.ProbeID = "", "", "", ""
		if update.SnapshotDigest("bootstrap-review", a) != update.SnapshotDigest("bootstrap-review", b) {
			return errors.New("bootstrap reviewed signer state changed")
		}
		return nil
	}
	callbacks := custodyinstall.BootstrapCallbacks{}
	callbacks.Maintain = func(ctx context.Context, t custodyinstall.BootstrapTransaction) (update.RecoveryIndex, error) {
		r, e := call(ctx, signer.Request{Action: "fixture-update-status", IntentID: t.ID})
		if e != nil {
			if !strings.Contains(e.Error(), "unknown update intent") {
				return update.RecoveryIndex{}, e
			}
			if !t.AdmissionCurrent() {
				return update.RecoveryIndex{}, errors.New("expired bootstrap admission")
			}
			p := update.NewProbe()
			c, e := probe(ctx, p)
			if e != nil {
				return update.RecoveryIndex{}, e
			}
			s, e := call(ctx, signer.Request{Action: "fixture-update-inventory", ProbeID: p})
			if e != nil {
				return update.RecoveryIndex{}, e
			}
			if !update.SameCoreMetadata(c, t.Core) || s.UpdateInventory == nil {
				return update.RecoveryIndex{}, errors.New("predecessor changed before hold")
			}
			a, b := *s.UpdateInventory, t.Signer
			a.BootID, a.ProbeID, b.BootID, b.ProbeID = "", "", "", ""
			if update.SnapshotDigest("bootstrap-review", a) != update.SnapshotDigest("bootstrap-review", b) {
				return update.RecoveryIndex{}, errors.New("signer changed after bootstrap review")
			}
			r, e = call(ctx, signer.Request{Action: "fixture-update-prepare", Update: &update.IntentRequest{ID: t.ID, Core: c, Signer: *s.UpdateInventory, Manifest: t.Bundle.Maintenance, Trust: t.Trust}})
			if e != nil {
				return update.RecoveryIndex{}, e
			}
		}
		if e = validateIntent(r.UpdateIntent); e != nil {
			return update.RecoveryIndex{}, e
		}
		i := *r.UpdateIntent
		if i.State != "prepared" && i.State != "staged" {
			return update.RecoveryIndex{}, errors.New("inactive bootstrap maintenance intent")
		}
		q := update.PauseFor(i)
		if _, e = control(ctx, update.CorePauseCommand{Action: "pause", ProbeID: update.NewProbe(), Request: &q}); e != nil {
			return update.RecoveryIndex{}, e
		}
		// The predecessor stage-finish wire format binds the original boot. The
		// actual current durable pause/metadata is checked before using that value.
		stageProbe := func(ctx context.Context, _ string) (update.CoreSnapshot, error) {
			c, e := probe(ctx, update.NewProbe())
			if e != nil {
				return c, e
			}
			if !update.SameCoreMetadata(c, i.Core) {
				return c, errors.New("paused predecessor metadata changed")
			}
			return i.Core, nil
		}
		if e = conductStage(ctx, t.ID, t.Dir()+"/previous", stageProbe, call, io.Discard); e != nil {
			return update.RecoveryIndex{}, e
		}
		backup := func(ctx context.Context, p update.Preflight) (update.RecoveryIndex, error) {
			m, e := custodyinstall.Load(os.Getuid())
			if e != nil {
				return update.RecoveryIndex{}, e
			}
			var b bytes.Buffer
			if e = system.UpdateBackup(ctx, m, p, &b); e != nil {
				return update.RecoveryIndex{}, e
			}
			var index update.RecoveryIndex
			e = update.StrictJSON(b.Bytes(), &index)
			return index, e
		}
		var b bytes.Buffer
		e = conductBackup(ctx, t.ID, control, backup, call, &b)
		var index update.RecoveryIndex
		if e == nil {
			e = update.StrictJSON(b.Bytes(), &index)
		}
		return index, e
	}
	callbacks.Observe = func(ctx context.Context, t custodyinstall.BootstrapTransaction, index update.RecoveryIndex, requireCore, allowAborted bool) error {
		r, e := readState(ctx)
		if e != nil {
			return e
		}
		if e = validateIntent(r.UpdateIntent); e != nil {
			return e
		}
		aborted := r.UpdateIntent.State == "aborted"
		if r.UpdateInventory == nil || !r.Status.Locked || (r.UpdateIntent.State != "staged" && !(allowAborted && aborted)) || !update.BootstrapHeld(index.Binding.Signer, *r.UpdateInventory, aborted) {
			return errors.New("bootstrap financial authority, history, hold or floor changed")
		}
		if !requireCore || aborted {
			return nil
		}
		var b bytes.Buffer
		if e = conductPreflight(ctx, t.ID, control, call, &b); e != nil {
			return e
		}
		var p update.Preflight
		if update.StrictJSON(b.Bytes(), &p) != nil {
			return errors.New("actual bootstrap preflight unavailable")
		}
		// Only the declared operations-reader feature changes across this bridge.
		// All financial heads/counters were independently checked above, unchanged.
		for n := range p.Signer.States {
			if p.Signer.States[n].Name == "operations" {
				for _, old := range index.Binding.Signer.States {
					if old.Name == "operations" {
						p.Signer.States[n].Features = append([]string{}, old.Features...)
					}
				}
			}
		}
		p.BindingSHA256 = ""
		p.BindingSHA256 = update.SnapshotDigest("preflight:v1", p)
		m, e := custodyinstall.Load(os.Getuid())
		if e != nil {
			return e
		}
		b.Reset()
		return system.UpdateRecovery(ctx, m, update.RecoveryCheck{Preflight: p, Index: index}, &b)
	}
	callbacks.Finish = func(ctx context.Context, t custodyinstall.BootstrapTransaction, index update.RecoveryIndex) error {
		if e := callbacks.Observe(ctx, t, index, false, true); e != nil {
			return e
		}
		if e := abortAndRelease(ctx, t.ID, control, call, io.Discard); e != nil {
			return e
		}
		return callbacks.Observe(ctx, t, index, false, true)
	}
	receipt, e := custodyinstall.RunBootstrap(ctx, tx, system, callbacks)
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(receipt)
}

func fileSHA(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func decodeBootstrapInventory(b []byte) (update.CoreSnapshot, update.SignerSnapshot, error) {
	var r struct {
		Core               update.CoreSnapshot    `json:"core"`
		Signer             *update.SignerSnapshot `json:"signer"`
		InstallAllowed     bool                   `json:"installAllowed"`
		FinancialExecution bool                   `json:"financialExecution"`
	}
	if update.StrictJSON(b, &r) != nil || r.Signer == nil || r.InstallAllowed || r.FinancialExecution || r.Core.ProbeID != r.Signer.ProbeID {
		return r.Core, update.SignerSnapshot{}, errors.New("invalid immutable predecessor inventory")
	}
	_, e := update.JoinSnapshots(r.Core, *r.Signer, update.Target{OS: "linux", Arch: "amd64"})
	return r.Core, *r.Signer, e
}
