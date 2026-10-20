//go:build linux

package custodyinstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fased-ai/fased-app/internal/update"
)

func noActiveController(m Manifest) error {
	_, e := readRoot(m.Root + "/update-active.json")
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	return errors.New("recoverable installation owns this instance; resume or roll back that exact intent first")
}
func LoadController(uid int, id string) (InstallTransaction, error) {
	var t InstallTransaction
	if uid <= 0 || !update.IDOK(id) {
		return t, errors.New("ordinary owner and exact installation intent required")
	}
	f, e := readRoot(Root(uid) + "/updates/" + id + "/transaction.json")
	if e != nil {
		return t, e
	}
	if f.Mode != 0644 || strict(f.Data, &t) != nil || !t.valid() || t.Base.Owner != uid || t.Binding.Index.Binding.Pause.Request.IntentID != id {
		return t, errors.New("foreign installation transaction; preserve state")
	}
	return t, nil
}

// The old immutable runner is retained before either live executable changes.
// It is selected only by a validated fixed transaction path, never a CLI path.
func ControllerRunner(uid int, id string) (string, error) {
	t, e := LoadController(uid, id)
	if e != nil {
		return "", e
	}
	f, e := readRoot(t.dir() + "/runner")
	if e != nil {
		return "", e
	}
	if f.Mode != 0755 || sum(f.Data) != t.Base.CoreHash {
		return "", errors.New("recovery runner changed")
	}
	f, e = readRoot(t.dir() + "/previous-signer")
	if e != nil {
		return "", e
	}
	if f.Mode != 0755 || sum(f.Data) != t.Base.SignerHash {
		return "", errors.New("cold inspection signer changed")
	}
	if e = verifyTopology(t.Base); e != nil {
		return "", e
	}
	if e = allowedInstallation(t, readRoot); e != nil {
		return "", e
	}
	return t.dir() + "/runner", nil
}
func PrepareController(ctx context.Context, m Manifest, index update.RecoveryIndex, contracts []update.Contract, artifacts string, s *System) (InstallTransaction, error) {
	var zero InstallTransaction
	if m.Owner != os.Getuid() || Verify(m) != nil || !update.ValidRecoveryIndex(index, index.Binding) || index.Binding.Pause.Request == nil {
		return zero, errors.New("verified installation and metadata recovery index required")
	}
	id := index.Binding.Pause.Request.IntentID
	if !update.IDOK(id) {
		return zero, errors.New("exact intent required")
	}
	if e := noActiveController(m); e != nil {
		return zero, e
	}
	t := InstallTransaction{Version: 1, Base: m, Target: m}
	t.Binding = update.InstallBinding{Contracts: contracts, Version: 1, Index: index, InstallationID: m.ID, PreviousCore: m.CoreHash, PreviousSigner: m.SignerHash, PreviousManifest: sum(mustJSON(m))}
	for _, f := range index.Binding.Files {
		switch f.Artifact.File {
		case "fased-app":
			t.Target.CoreHash = f.Artifact.SHA256
			t.Binding.TargetCore = f.Artifact.SHA256
		case "fased-signer":
			t.Target.SignerHash = f.Artifact.SHA256
			t.Binding.TargetSigner = f.Artifact.SHA256
		}
	}
	if !t.valid() {
		return zero, errors.New("unsupported fixed candidate pair")
	}
	var fs unix.Statfs_t
	if e := unix.Statfs(m.Root, &fs); e != nil {
		return zero, e
	}
	needed := uint64(8 << 20)
	for _, f := range index.Binding.Files {
		needed += uint64(f.Bytes) * 2
	}
	for _, name := range []string{"fased-app", "fased-signer"} {
		st, e := os.Stat(m.Root + "/" + name)
		if e != nil {
			return zero, e
		}
		needed += uint64(st.Size()) * 2
	}
	if fs.Bsize <= 0 || fs.Bavail < uint64((needed+uint64(fs.Bsize)-1)/uint64(fs.Bsize)) {
		return zero, errors.New("insufficient space for candidate, recovery pair and atomic replacements")
	}
	for _, d := range []string{m.Root + "/updates", t.dir()} {
		if e := directory(ctx, d, 0, 0, 0755, s); e != nil {
			return zero, e
		}
	}
	files := map[string][]byte{}
	for _, p := range []struct{ source, name, hash string }{{m.Root + "/fased-app", "runner", m.CoreHash}, {m.Root + "/fased-signer", "previous-signer", m.SignerHash}} {
		f, e := readRoot(p.source)
		if e != nil || sum(f.Data) != p.hash {
			return zero, errors.New("previous executable changed")
		}
		files[p.name] = f.Data
	}
	for _, f := range index.Binding.Files {
		b, e := source(filepath.Join(artifacts, f.Artifact.File))
		if e != nil {
			return zero, e
		}
		if int64(len(b)) != f.Bytes || sum(b) != f.Artifact.SHA256 {
			return zero, errors.New("candidate source differs from staged identity")
		}
		name := "next-core"
		if f.Artifact.File == "fased-signer" {
			name = "next-signer"
		}
		files[name] = b
	}
	for _, name := range []string{"runner", "previous-signer", "next-core", "next-signer"} {
		if e := publish(ctx, t.dir()+"/"+name, m.ID, files[name], 0755, readRoot, s.Run); e != nil {
			return zero, e
		}
	}
	if e := probeRollbackCompatibility(ctx, t, s); e != nil {
		return zero, e
	}
	if e := publish(ctx, t.dir()+"/transaction.json", m.ID, mustJSON(t), 0644, readRoot, s.Run); e != nil {
		return zero, e
	}
	return t, nil
}
func probeRollbackCompatibility(ctx context.Context, t InstallTransaction, s *System) error {
	probe := func(name string, core bool) (update.Contract, error) {
		i := t.Base.Signer
		args := []string{"fixture-compatibility"}
		if core {
			i = t.Base.App
			args = []string{"updates", "compatibility"}
		}
		cmd := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(i.UID), "--regid", strconv.Itoa(i.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", t.dir() + "/" + name}
		cmd = append(cmd, args...)
		b, e := s.Run(ctx, nil, cmd...)
		if e != nil {
			return update.Contract{}, e
		}
		var c update.Contract
		if core {
			var x struct {
				Contract           update.Contract `json:"contract"`
				WalletSigner       *bool           `json:"walletSigner"`
				FinancialExecution *bool           `json:"financialExecution"`
				UpdaterEnabled     *bool           `json:"updaterEnabled"`
			}
			if e = json.Unmarshal(b, &x); e != nil {
				return c, e
			}
			c = x.Contract
			if x.WalletSigner == nil || *x.WalletSigner || x.FinancialExecution == nil || *x.FinancialExecution || x.UpdaterEnabled == nil || *x.UpdaterEnabled {
				return c, errors.New("candidate financial/automatic-update capabilities must be explicitly disabled")
			}
		} else if e = update.StrictJSON(b, &c); e != nil {
			return c, e
		}
		return c, nil
	}
	oldCore, e := probe("runner", true)
	if e != nil {
		return e
	}
	newCore, e := probe("next-core", true)
	if e != nil {
		return e
	}
	oldSigner, e := probe("previous-signer", false)
	if e != nil {
		return e
	}
	newSigner, e := probe("next-signer", false)
	if e != nil {
		return e
	}
	for _, claimed := range t.Binding.Contracts {
		actual := newCore
		if claimed.Component == "signer" {
			actual = newSigner
		}
		if update.SnapshotDigest("contract", claimed) != update.SnapshotDigest("contract", actual) {
			return errors.New("candidate contract probe differs from signed manifest")
		}
	}
	for _, c := range []update.Contract{oldCore, newCore} {
		if c.Lifecycle == nil || c.Lifecycle.Entrypoint != 4 {
			return errors.New("both helpers must read installation-controller records")
		}
	}
	for _, c := range []update.Contract{oldSigner, newSigner} {
		found := false
		for _, v := range c.State {
			if v.Name == "operations" {
				for _, f := range v.Features {
					if f == "install-completion-v1" {
						found = true
					}
				}
			}
		}
		if !found {
			return errors.New("both signers must preserve installation holds during rollback")
		}
	}
	if oldCore.Protocol != newCore.Protocol || oldSigner.Protocol != newSigner.Protocol || update.SnapshotDigest("state-contract", oldCore.State) != update.SnapshotDigest("state-contract", newCore.State) || update.SnapshotDigest("state-contract", oldSigner.State) != update.SnapshotDigest("state-contract", newSigner.State) {
		return errors.New("trial installation supports unchanged state formats only; migrations unavailable")
	}
	return nil
}

type InstallationCallbacks struct {
	Observe func(context.Context, Manifest, update.InstallBinding) error
	Hold    func(context.Context, string, update.InstallBinding) (*update.Installation, error)
	State   func(context.Context) (*update.Installation, error)
	Release func(context.Context, update.Installation) error
}

func ExecuteController(ctx context.Context, t InstallTransaction, rollback bool, s *System, c InstallationCallbacks) (*update.Installation, error) {
	if !t.valid() {
		return nil, errors.New("invalid installation transaction")
	}
	if os.Getuid() != t.Base.Owner || os.Geteuid() != os.Getuid() {
		return nil, errors.New("ordinary installation owner required")
	}
	_, e := ControllerRunner(t.Base.Owner, t.Binding.Index.Binding.Pause.Request.IntentID)
	if e != nil {
		return nil, e
	}
	o := installOps{read: readRoot, run: s.Run, state: func(ctx context.Context) (*update.Installation, error) {
		return currentInstallation(ctx, t, s, c.State)
	}, topology: func(m Manifest) error {
		if e := verifyTopology(m); e != nil {
			return e
		}
		for _, role := range []string{"app", "signer"} {
			if e := loadedUnit(m, role); e != nil {
				return e
			}
		}
		return nil
	}, health: func(ctx context.Context, m Manifest) error { return waitReady(ctx, m, s) }, observe: c.Observe, hold: c.Hold, checkpoint: func(phase string) error {
		fmt.Fprintln(s.Out, "Installation boundary:", phase)
		if s.installFault != nil {
			return s.installFault(phase)
		}
		return nil
	}}
	return runTransaction(ctx, t, rollback, o)
}

func InstallationStatus(ctx context.Context, t InstallTransaction, s *System, c InstallationCallbacks, out io.Writer) error {
	if _, e := ControllerRunner(t.Base.Owner, t.Binding.Index.Binding.Pause.Request.IntentID); e != nil {
		return e
	}
	m, e := Load(t.Base.Owner)
	if e != nil {
		return e
	}
	if e = waitReady(ctx, m, s); e != nil {
		return e
	}
	i, e := c.State(ctx)
	if e == nil && i != nil && i.Phase != "completed" {
		e = c.Observe(ctx, m, t.Binding)
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(struct {
		Transaction        InstallTransaction   `json:"transaction"`
		Installation       *update.Installation `json:"installation"`
		FinancialExecution bool                 `json:"financialExecution"`
		ResumeAllowed      bool                 `json:"resumeAllowed"`
	}{Transaction: t, Installation: i})
}

// Cold inspection is allowed only while systemd proves the signer inactive.
// The retained signer executes after a fixed drop to the configured signer UID.
func currentInstallation(ctx context.Context, t InstallTransaction, s *System, state func(context.Context) (*update.Installation, error)) (*update.Installation, error) {
	i, e := state(ctx)
	if e == nil {
		return i, nil
	}
	b, se := s.Run(ctx, nil, "/usr/bin/systemctl", "show", Unit(t.Base.Owner, "signer"), "--property=ActiveState,MainPID")
	if se != nil {
		return nil, se
	}
	if string(b) != "ActiveState=inactive\nMainPID=0\n" && string(b) != "MainPID=0\nActiveState=inactive\n" {
		return nil, e
	}
	p := t.Base.Signer
	b, se = s.Run(ctx, nil, "/usr/bin/setpriv", "--reuid", strconv.Itoa(p.UID), "--regid", strconv.Itoa(p.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", t.dir()+"/previous-signer", "fixture-install-inspect", "--disposable-fixture", "--config", t.Base.Root+"/identity.json", "--intent-id", t.Binding.Index.Binding.Pause.Request.IntentID)
	if se != nil {
		return nil, se
	}
	var inspected update.Installation
	if update.StrictJSON(b, &inspected) != nil || !update.ValidInstallation(inspected) {
		return nil, errors.New("invalid cold signer hold receipt")
	}
	return &inspected, nil
}
