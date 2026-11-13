//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/term"
)

func delegateInstallation(args []string, in *os.File, out, errOut io.Writer) error {
	id := ""
	for n := 1; n < len(args); n++ {
		value := ""
		if args[n] == "--intent-id" && n+1 < len(args) {
			n++
			value = args[n]
		} else if strings.HasPrefix(args[n], "--intent-id=") {
			value = strings.TrimPrefix(args[n], "--intent-id=")
		}
		if value != "" {
			if id != "" {
				return errors.New("duplicate installation identity")
			}
			id = value
		}
	}
	if !update.IDOK(id) {
		return errors.New("exact installation intent required")
	}
	tx, e := custodyinstall.LoadController(os.Getuid(), id)
	published := e == nil
	var runner string
	if errors.Is(e, os.ErrNotExist) {
		runner, e = custodyinstall.InitialMigrationRunner(os.Getuid(), id)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if errors.Is(e, os.ErrNotExist) {
			m, loadErr := custodyinstall.Load(os.Getuid())
			if loadErr != nil {
				return loadErr
			}
			if loadErr = custodyinstall.Verify(m); loadErr != nil {
				return loadErr
			}
			runner = m.Root + "/fased-app"
		}
	} else if e != nil {
		return e
	} else {
		runner, e = custodyinstall.ControllerRunner(os.Getuid(), id)
		if e != nil {
			return e
		}
	}
	// Prepare the app-owned session before the child acquires OwnerLock. The
	// same ordering is needed whether the predecessor transaction is already
	// published or this is the initial pinned installation.
	if runner == custodyinstall.Root(os.Getuid())+"/maintenance/runner" {
		m, loadErr := custodyinstall.Load(os.Getuid())
		if loadErr != nil {
			return loadErr
		}
		if !needsPinnedInitialSession(m, runner) {
			return invokeOwnerInstallation(runner, args, in, out, errOut)
		}
		system := &custodyinstall.System{In: in, Out: errOut}
		if e = system.UseScopedMaintenance(m.Owner); e != nil {
			return e
		}
		digest := ""
		if published {
			digest = update.InstallDigest(tx.Binding)
		}
		e = preparePinnedInstallationSession(m, runner, published, func() (bool, error) {
			return custodyinstall.UpdateFencePresent(m, id, digest)
		}, func() (bool, error) {
			return custodyinstall.UpdateSessionRuntime(m)
		}, func() error {
			return system.PrepareNativeUpdateSession(context.Background(), m, runner)
		})
		if e != nil {
			return e
		}
	}
	return invokeOwnerInstallation(runner, args, in, out, errOut)
}

func preparePinnedInstallationSession(m custodyinstall.Manifest, runner string, published bool, check func() (bool, error), runtime func() (bool, error), prepare func() error) error {
	if !needsPinnedInitialSession(m, runner) {
		return nil
	}
	if check == nil || runtime == nil || prepare == nil {
		return errors.New("pinned installation session checks required")
	}
	fenced, err := check()
	if err != nil {
		return err
	}
	if fenced {
		return nil // The locked controller owns all fence recovery.
	}
	if published {
		ready, runtimeErr := runtime()
		if runtimeErr != nil {
			return runtimeErr
		}
		if !ready {
			// Let the locked controller inspect a verified inactive pair; a
			// live-session helper cannot run while either service is down.
			return nil
		}
	}
	return prepare()
}

func invokeOwnerInstallation(runner string, args []string, in *os.File, out, errOut io.Writer) error {
	command := append([]string{"owner-updates", "--action", args[0], "--config", custodyinstall.Root(os.Getuid()) + "/identity.json"}, args[1:]...)
	c := exec.Command(runner, command...)
	c.Stdin = in
	c.Stdout = out
	c.Stderr = errOut
	return c.Run()
}

func verifyOwnerInstallationHold(ctx context.Context, id string, expected *update.Installation, call updateRPC) error {
	r, err := call(ctx, signer.Request{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "status", IntentID: id}})
	if expected == nil && r.Installation == nil && errors.Is(err, signer.ErrNoInstallationHold) {
		return nil
	}
	if expected == nil || err != nil || r.Installation == nil || !update.ValidInstallation(*r.Installation) || r.Installation.Phase != "active" || r.Installation.Digest != expected.Digest {
		return errors.New("installation hold changed; preserve the original update")
	}
	return nil
}

// Reuse a signer-owned hold's original recovery index after an interrupted
// preparation. A new backup after the hold would bind a different journal head.
func installationRecoveryIndex(ctx context.Context, id string, m custodyinstall.Manifest, control coreControl, backup coreBackup, call updateRPC, fresh func(context.Context, custodyinstall.Manifest, *update.RecoveryIndex) (update.Preflight, error)) (update.RecoveryIndex, *update.Installation, error) {
	var zero update.RecoveryIndex
	status := signer.Request{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "status", IntentID: id}}
	r, err := call(ctx, status)
	if errors.Is(err, signer.ErrNoInstallationHold) {
		if r.Installation != nil {
			return zero, nil, errors.New("ambiguous installation status; preserve the original update")
		}
		var b bytes.Buffer
		if err = conductBackup(ctx, id, control, backup, call, &b); err != nil {
			return zero, nil, err
		}
		if err = update.StrictJSON(b.Bytes(), &zero); err != nil {
			return zero, nil, err
		}
		return zero, nil, nil
	}
	if err != nil {
		return zero, nil, err
	}
	hold := r.Installation
	previous := sha256.Sum256(mustJSON(m))
	if hold == nil || !update.ValidInstallation(*hold) || hold.Phase != "active" || hold.InstalledSequenceCommitted || hold.Binding.Index.Binding.Pause.Request == nil || hold.Binding.Index.Binding.Pause.Request.IntentID != id || hold.Binding.InstallationID != m.ID || hold.Binding.PreviousCore != m.CoreHash || hold.Binding.PreviousSigner != m.SignerHash || hold.Binding.PreviousManifest != fmt.Sprintf("%x", previous) {
		return zero, nil, errors.New("existing installation hold differs from this predecessor; preserve it")
	}
	if _, err = fresh(ctx, m, &hold.Binding.Index); err != nil {
		return zero, nil, err
	}
	if err = verifyOwnerInstallationHold(ctx, id, hold, call); err != nil {
		return zero, nil, errors.New("installation hold changed during recovery verification; preserve it")
	}
	return hold.Binding.Index, hold, nil
}

// The same owner operation is used for the initial hold and later phases. A
// lost response leaves the signer journal authoritative for the next retry.
func ownerInstallationPhase(ctx context.Context, id, phase string, m custodyinstall.Manifest, b update.InstallBinding, fresh func(context.Context, custodyinstall.Manifest, *update.RecoveryIndex) (update.Preflight, error), call updateRPC) (*update.Installation, error) {
	if !update.IDOK(id) || b.Index.Binding.Pause.Request == nil || b.Index.Binding.Pause.Request.IntentID != id || update.ValidateInstallBinding(b) != nil || fresh == nil || call == nil {
		return nil, errors.New("exact installation binding required")
	}
	p, err := fresh(ctx, m, &b.Index)
	if err != nil {
		return nil, err
	}
	r, err := call(ctx, signer.Request{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: phase, IntentID: id, Binding: &b, Preflight: &update.PreflightRequest{IntentID: id, Sequence: p.Sequence, ManifestSHA256: p.ManifestSHA256, ProbeID: p.Pause.Core.ProbeID, Pause: p.Pause, ExpectedSigner: p.Signer}}})
	if err != nil {
		return nil, err
	}
	if r.Installation == nil || !update.ValidInstallation(*r.Installation) || r.Installation.Digest != update.InstallDigest(b) || (r.Installation.Phase != phase && !(phase == "active" && r.Installation.Phase == "healthy-held")) {
		return nil, errors.New("unbound signer installation receipt")
	}
	return r.Installation, nil
}

func ownerInstallation(action, id, config, artifacts string, automation bool, in *os.File, out, errOut io.Writer) error {
	if !update.IDOK(id) {
		return errors.New("exact installation intent required")
	}
	cfg, e := signer.LoadConfig(config)
	if e != nil {
		return e
	}
	if e = signer.CheckOwner(cfg); e != nil {
		return e
	}
	if config != custodyinstall.Root(os.Getuid())+"/identity.json" {
		return errors.New("fixed managed identity required")
	}
	lock, e := custodyinstall.OwnerLock()
	if e != nil {
		return e
	}
	defer lock.Close()
	system := &custodyinstall.System{In: in, Out: errOut}
	if e = system.UseScopedMaintenance(os.Getuid()); e != nil {
		return e
	}
	ctx := context.Background()
	t, loadErr := custodyinstall.LoadController(os.Getuid(), id)
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		return loadErr
	}
	call := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		return signer.Call(ctx, cfg, true, r)
	}
	controlFor := func(m custodyinstall.Manifest) coreControl {
		return func(ctx context.Context, c update.CorePauseCommand) (update.CorePauseReceipt, error) {
			var b bytes.Buffer
			if e := system.UpdatePause(ctx, m, c, &b); e != nil {
				return update.CorePauseReceipt{}, e
			}
			var p update.CorePauseReceipt
			e := update.StrictJSON(b.Bytes(), &p)
			return p, e
		}
	}
	fresh := func(ctx context.Context, m custodyinstall.Manifest, index *update.RecoveryIndex) (update.Preflight, error) {
		var b bytes.Buffer
		if e := conductPreflight(ctx, id, controlFor(m), call, true, &b); e != nil {
			return update.Preflight{}, e
		}
		var p update.Preflight
		if e := update.StrictJSON(b.Bytes(), &p); e != nil {
			return p, e
		}
		if index != nil {
			if !update.RecoveryMatches(*index, p) {
				return p, errors.New("current services differ from recovery index")
			}
			b.Reset()
			if e := system.UpdateRecovery(ctx, m, update.RecoveryCheck{Preflight: p, Index: *index}, &b); e != nil {
				return p, e
			}
			var verified update.RecoveryIndex
			if update.StrictJSON(b.Bytes(), &verified) != nil || update.SnapshotDigest("index", verified) != update.SnapshotDigest("index", *index) {
				return p, errors.New("actual recovery snapshot revalidation failed")
			}
		}
		return p, nil
	}
	hold := func(ctx context.Context, phase string, b update.InstallBinding) (*update.Installation, error) {
		m, e := custodyinstall.Load(os.Getuid())
		if e != nil {
			return nil, e
		}
		if phase == "active" {
			cold, coldErr := custodyinstall.PreparationHold(ctx, m, id, update.InstallDigest(b), system)
			if coldErr == nil {
				if cold.Digest != update.InstallDigest(b) {
					return nil, errors.New("cold preparation hold differs; preserve it")
				}
				return cold, nil
			}
			if !errors.Is(coldErr, os.ErrNotExist) && !errors.Is(coldErr, custodyinstall.ErrTransactionRecoveryFence) {
				return nil, coldErr
			}
		}
		return ownerInstallationPhase(ctx, id, phase, m, b, fresh, call)
	}
	if loadErr != nil {
		if action != "install" {
			return errors.New("no installation transaction; begin with reviewed install")
		}
		m, e := custodyinstall.Load(os.Getuid())
		if e != nil {
			return e
		}
		if e = custodyinstall.Verify(m); e != nil {
			return e
		}
		backup := func(ctx context.Context, p update.Preflight) (update.RecoveryIndex, error) {
			var buf bytes.Buffer
			if e := system.UpdateBackup(ctx, m, p, &buf); e != nil {
				return update.RecoveryIndex{}, e
			}
			var i update.RecoveryIndex
			e := update.StrictJSON(buf.Bytes(), &i)
			return i, e
		}
		var index update.RecoveryIndex
		held, coldErr := custodyinstall.PreparationHold(ctx, m, id, "", system)
		cold := coldErr == nil
		if cold {
			index = held.Binding.Index
		} else {
			if !errors.Is(coldErr, os.ErrNotExist) {
				return coldErr
			}
			index, held, e = installationRecoveryIndex(ctx, id, m, controlFor(m), backup, call, fresh)
			if e != nil {
				return e
			}
		}
		// The previous PREPARE review explicitly authorized no installation.
		// A separate complete digest-bound owner review authorizes this trial.
		review := struct {
			Index  update.RecoveryIndex `json:"index"`
			Core   string               `json:"previousCore"`
			Signer string               `json:"previousSigner"`
		}{index, m.CoreHash, m.SignerHash}
		digest := update.SnapshotDigest("install-review:v1", review)
		pretty, _ := json.MarshalIndent(review, "", "  ")
		fmt.Fprintf(errOut, "DISPOSABLE COMPATIBLE TRIAL INSTALLATION\nReplace only the verified core/signer executables and inventory. Financial history is never restored. The wallet stays locked and core metadata stays paused; no installed-sequence commit or automatic resume.\n%s\nType INSTALL %s, or anything else to cancel: ", pretty, digest)
		if !automation && !term.IsTerminal(int(in.Fd())) {
			return errors.New("installation requires the protected owner terminal")
		}
		line, e := bufio.NewReader(in).ReadString('\n')
		if e != nil || line != "INSTALL "+digest+"\n" {
			return errors.New("installation review cancelled")
		}
		var contracts []update.Contract
		if cold {
			contracts = held.Binding.Contracts
		} else {
			if _, e = fresh(ctx, m, &index); e != nil {
				return e
			}
			r, statusErr := call(ctx, signer.Request{Action: "fixture-update-status", IntentID: id})
			if statusErr != nil {
				return statusErr
			}
			if r.UpdateIntent == nil || r.UpdateIntent.State != "staged" || r.UpdateIntent.ManifestSHA256 != index.Binding.Pause.Request.ManifestSHA256 {
				return errors.New("signed staged contracts unavailable")
			}
			if held != nil && update.SnapshotDigest("contracts", r.UpdateIntent.Contracts) != update.SnapshotDigest("contracts", held.Binding.Contracts) {
				return errors.New("signed contracts differ from the held installation; preserve it")
			}
			if e = verifyOwnerInstallationHold(ctx, id, held, call); e != nil {
				return e
			}
			contracts = r.UpdateIntent.Contracts
		}
		t, e = custodyinstall.PrepareController(ctx, m, index, contracts, artifacts, system, hold)
		if e != nil {
			return e
		}
	}
	if loadErr == nil && action == "install" {
		cold, coldErr := custodyinstall.PreparationHold(ctx, t.Base, id, update.InstallDigest(t.Binding), system)
		if coldErr == nil {
			if cold.Digest != update.InstallDigest(t.Binding) {
				return errors.New("published transaction differs from the cold hold; preserve it")
			}
			resumed, resumeErr := custodyinstall.PrepareController(ctx, t.Base, cold.Binding.Index, cold.Binding.Contracts, artifacts, system, hold)
			if resumeErr != nil {
				return resumeErr
			}
			if !bytes.Equal(mustJSON(resumed), mustJSON(t)) {
				return errors.New("resumed preparation changed the original transaction")
			}
		} else if !errors.Is(coldErr, os.ErrNotExist) && !errors.Is(coldErr, custodyinstall.ErrTransactionRecoveryFence) {
			return coldErr
		}
	}
	callbacks := custodyinstall.InstallationCallbacks{
		Release: func(ctx context.Context, i update.Installation) error {
			p, e := controlFor(t.Target)(ctx, update.CorePauseCommand{Action: "complete", ProbeID: update.NewProbe(), Completed: &i})
			if e != nil {
				return e
			}
			if p.State != "released" || p.Request == nil || p.PauseID != i.Binding.Index.Binding.Pause.PauseID || update.SnapshotDigest("pause-request", *p.Request) != update.SnapshotDigest("pause-request", *i.Binding.Index.Binding.Pause.Request) {
				return errors.New("unbound core completion release")
			}
			return nil
		},
		State: func(ctx context.Context) (*update.Installation, error) {
			r, e := call(ctx, signer.Request{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "status", IntentID: id}})
			if e != nil {
				return nil, e
			}
			return r.Installation, nil
		},
		Observe: func(ctx context.Context, m custodyinstall.Manifest, b update.InstallBinding) error {
			_, e := fresh(ctx, m, &b.Index)
			return e
		},
		Hold: hold,
	}
	if action == "complete" {
		state, e := custodyinstall.CompletionState(ctx, t, system, callbacks.State)
		if e != nil {
			return e
		}
		if state == nil || !update.ValidInstallation(*state) || state.Digest != update.InstallDigest(t.Binding) {
			return errors.New("actual bound installation receipt required")
		}
		if !state.InstalledSequenceCommitted {
			if !automation && !term.IsTerminal(int(in.Fd())) {
				return errors.New("completion requires the protected owner terminal")
			}
			want := "COMPLETE " + state.Digest
			pretty, _ := json.MarshalIndent(state, "", "  ")
			fmt.Fprintf(errOut, "Commit this exact installed pair and sequence. Executable rollback closes after commit; wallet stays locked and automation disabled.\n%s\nType %s, or anything else to cancel: ", pretty, want)
			line, e := bufio.NewReader(in).ReadString('\n')
			if e != nil || line != want+"\n" {
				return errors.New("completion review cancelled")
			}
		}
		i, e := custodyinstall.CompleteController(ctx, t, system, callbacks)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(i)
	}
	if action == "install-status" {
		return custodyinstall.InstallationStatus(ctx, t, system, callbacks, out)
	}
	if action == "rollback" {
		if !automation && !term.IsTerminal(int(in.Fd())) {
			return errors.New("rollback requires the protected owner terminal")
		}
		want := "ROLLBACK " + update.InstallDigest(t.Binding)
		fmt.Fprintf(errOut, "Restore only the previous compatible executables. Financial state and holds never rewind. Type %s, or anything else to cancel: ", want)
		line, e := bufio.NewReader(in).ReadString('\n')
		if e != nil || line != want+"\n" {
			return errors.New("rollback review cancelled")
		}
	}
	i, e := custodyinstall.ExecuteController(ctx, t, action == "rollback", system, callbacks)
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(i)
}
