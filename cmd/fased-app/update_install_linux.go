//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
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
	runner, e := custodyinstall.ControllerRunner(os.Getuid(), id)
	if errors.Is(e, os.ErrNotExist) {
		m, le := custodyinstall.Load(os.Getuid())
		if le != nil {
			return le
		}
		if le = custodyinstall.Verify(m); le != nil {
			return le
		}
		runner = m.Root + "/fased-app"
	} else if e != nil {
		return e
	}
	command := append([]string{"owner-updates", "--action", args[0], "--config", custodyinstall.Root(os.Getuid()) + "/identity.json"}, args[1:]...)
	c := exec.Command(runner, command...)
	c.Stdin = in
	c.Stdout = out
	c.Stderr = errOut
	return c.Run()
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
		if e := conductPreflight(ctx, id, controlFor(m), call, &b); e != nil {
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
		var b bytes.Buffer
		backup := func(ctx context.Context, p update.Preflight) (update.RecoveryIndex, error) {
			var buf bytes.Buffer
			if e := system.UpdateBackup(ctx, m, p, &buf); e != nil {
				return update.RecoveryIndex{}, e
			}
			var i update.RecoveryIndex
			e := update.StrictJSON(buf.Bytes(), &i)
			return i, e
		}
		if e = conductBackup(ctx, id, controlFor(m), backup, call, &b); e != nil {
			return e
		}
		var index update.RecoveryIndex
		if e = update.StrictJSON(b.Bytes(), &index); e != nil {
			return e
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
		if _, e = fresh(ctx, m, &index); e != nil {
			return e
		}
		r, e := call(ctx, signer.Request{Action: "fixture-update-status", IntentID: id})
		if e != nil {
			return e
		}
		if r.UpdateIntent == nil || r.UpdateIntent.State != "staged" || r.UpdateIntent.ManifestSHA256 != index.Binding.Pause.Request.ManifestSHA256 {
			return errors.New("signed staged contracts unavailable")
		}
		t, e = custodyinstall.PrepareController(ctx, m, index, r.UpdateIntent.Contracts, artifacts, system)
		if e != nil {
			return e
		}
	}
	callbacks := custodyinstall.InstallationCallbacks{
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
		Hold: func(ctx context.Context, phase string, b update.InstallBinding) (*update.Installation, error) {
			m, e := custodyinstall.Load(os.Getuid())
			if e != nil {
				return nil, e
			}
			p, e := fresh(ctx, m, &b.Index)
			if e != nil {
				return nil, e
			}
			r, e := call(ctx, signer.Request{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: phase, IntentID: id, Binding: &b, Preflight: &update.PreflightRequest{IntentID: id, Sequence: p.Sequence, ManifestSHA256: p.ManifestSHA256, ProbeID: p.Pause.Core.ProbeID, Pause: p.Pause, ExpectedSigner: p.Signer}}})
			if e != nil {
				return nil, e
			}
			if r.Installation == nil || !update.ValidInstallation(*r.Installation) || r.Installation.Digest != update.InstallDigest(b) {
				return nil, errors.New("unbound signer installation receipt")
			}
			return r.Installation, nil
		},
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
