//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestMigrationRunnerRequiresExactIndependentApproval(t *testing.T) {
	tx, _ := modelTransaction(t)
	approval := migrationRunnerApproval{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, IntentID: tx.Binding.Index.Binding.Pause.Request.IntentID, BindingDigest: update.InstallDigest(tx.Binding), PreviousCore: tx.Base.CoreHash, TargetCore: tx.Target.CoreHash, TargetSigner: tx.Target.SignerHash, RunnerSHA256: strings.Repeat("c", 64), GuardSHA256: strings.Repeat("d", 64)}
	marker := tx.dir() + "/migration-runner.json"
	runner := tx.dir() + "/migration-runner"
	src := filepath.Join("/var/lib", "fased-app-approved-"+strconv.Itoa(tx.Base.Owner), approval.RunnerSHA256)
	for _, scenario := range []string{"legacy", "missing", "valid", "marker-without-scope", "wrong-binding", "target-as-runner", "predecessor-as-runner", "foreign-marker", "changed-runner", "changed-source", "invalid-contract"} {
		t.Run(scenario, func(t *testing.T) {
			x := approval
			if scenario == "wrong-binding" {
				x.BindingDigest = strings.Repeat("d", 64)
			}
			if scenario == "target-as-runner" {
				x.RunnerSHA256 = tx.Target.CoreHash
			}
			if scenario == "predecessor-as-runner" {
				x.RunnerSHA256 = tx.Base.CoreHash
			}
			scope := scenario != "legacy" && scenario != "marker-without-scope"
			f := hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(x)}
			if scenario == "foreign-marker" {
				f.UID = tx.Base.Owner
			}
			read := func(p string) (hostFile, error) {
				if p != marker || scenario == "missing" || scenario == "legacy" {
					return hostFile{}, os.ErrNotExist
				}
				return f, nil
			}
			check := func(path, digest string) (int64, error) {
				if path != runner || digest != approval.RunnerSHA256 {
					t.Fatal("unbound runner identity")
				}
				if scenario == "changed-runner" {
					return 0, errors.New("changed")
				}
				return 123, nil
			}
			verify := func(path, digest string, size int64) error {
				if path != src || digest != approval.RunnerSHA256 || size != 123 {
					t.Fatal("unbound approved source")
				}
				if scenario == "changed-source" {
					return errors.New("changed")
				}
				return nil
			}
			got, err := selectMigrationRunner(tx, read, func(int) (bool, error) { return scope, nil }, check, verify, func(path string, got InstallTransaction) error {
				if scenario == "invalid-contract" {
					return errors.New("incompatible reader")
				}
				if path != runner || update.InstallDigest(got.Binding) != update.InstallDigest(tx.Binding) {
					return errors.New("wrong migration contract probe")
				}
				return nil
			})
			switch scenario {
			case "legacy":
				if err != nil || got != tx.dir()+"/runner" {
					t.Fatal(got, err)
				}
			case "valid":
				if err != nil || got != runner {
					t.Fatal(got, err)
				}
			default:
				if err == nil {
					t.Fatal("unsafe migration accepted", scenario)
				}
			}
		})
	}
}

func TestReusableAuthorityRejectsTransactionSubstitutionOfPinnedGuard(t *testing.T) {
	tx, _ := modelTransaction(t)
	runner, guard := []byte("untrusted staged runner"), []byte("untrusted staged guard")
	approval := migrationRunnerApproval{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, IntentID: tx.Binding.Index.Binding.Pause.Request.IntentID, BindingDigest: update.InstallDigest(tx.Binding), PreviousCore: tx.Base.CoreHash, TargetCore: tx.Target.CoreHash, TargetSigner: tx.Target.SignerHash, RunnerSHA256: sum(runner), GuardSHA256: sum(guard)}
	plan := mustJSON(approval)
	pinned := reusableMaintenanceAuthority{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, RunnerSHA256: strings.Repeat("c", 64), GuardSHA256: strings.Repeat("d", 64)}
	authority := mustJSON(pinned)
	dir := filepath.Join("/var/lib", "fased-app-approved-"+strconv.Itoa(tx.Base.Owner))
	files := map[string]hostFile{
		filepath.Join(tx.Base.Root, "maintenance", "authority.json"):   {UID: 0, Mode: 0644, Regular: true, Data: authority},
		filepath.Join(tx.Base.Root, "maintenance", "reusable-v1.json"): {UID: 0, Mode: 0644, Regular: true, Data: reusableMaintenanceMarkerBytes(pinned)},
		filepath.Join(dir, "migration-"+approval.IntentID+".json"):     {UID: 0, Mode: 0644, Regular: true, Data: plan},
		filepath.Join(dir, approval.RunnerSHA256):                      {UID: 0, Mode: 0444, Regular: true, Data: runner},
		filepath.Join(dir, approval.GuardSHA256):                       {UID: 0, Mode: 0444, Regular: true, Data: guard},
	}
	read := func(path string) (hostFile, error) {
		f, ok := files[path]
		if !ok {
			return hostFile{}, os.ErrNotExist
		}
		return f, nil
	}
	if _, _, _, err := migrationRunnerInputs(tx, read, func(string, string, int64) error { return nil }); err == nil {
		t.Fatal("owner-staged binaries replaced the independently pinned maintenance authority")
	}
}

func TestAtomicAdmissionRejectsSubstitutedPinnedGuard(t *testing.T) {
	tx, _ := modelTransaction(t)
	pin := reusableMaintenanceAuthority{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, RunnerSHA256: strings.Repeat("c", 64), GuardSHA256: strings.Repeat("d", 64)}
	approval := migrationRunnerApproval{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, IntentID: tx.Binding.Index.Binding.Pause.Request.IntentID, BindingDigest: update.InstallDigest(tx.Binding), PreviousCore: tx.Base.CoreHash, TargetCore: tx.Target.CoreHash, TargetSigner: tx.Target.SignerHash, RunnerSHA256: pin.RunnerSHA256, GuardSHA256: strings.Repeat("e", 64)}
	files := map[string]hostFile{
		tx.dir() + "/migration-runner.json":                  {UID: 0, Mode: 0644, Regular: true, Data: mustJSON(approval)},
		reusableMaintenancePath(tx.Base, "authority.json"):   {UID: 0, Mode: 0644, Regular: true, Data: mustJSON(pin)},
		reusableMaintenancePath(tx.Base, "reusable-v1.json"): {UID: 0, Mode: 0644, Regular: true, Data: reusableMaintenanceMarkerBytes(pin)},
	}
	read := func(path string) (hostFile, error) {
		if f, ok := files[path]; ok {
			return f, nil
		}
		return hostFile{}, os.ErrNotExist
	}
	check := func(string, string) (int64, error) { return 1, nil }
	if _, err := readAtomicMigrationApproval(tx, read, check); err == nil {
		t.Fatal("atomic worker admission accepted a transaction-selected signer guard")
	}
	approval.GuardSHA256 = pin.GuardSHA256
	files[tx.dir()+"/migration-runner.json"] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(approval)}
	if _, err := readAtomicMigrationApproval(tx, read, check); err != nil {
		t.Fatal("matching pinned authority was refused", err)
	}
}

func TestReusableAuthoritySelectsPinnedRunnerForInitialAndRecoveryEntry(t *testing.T) {
	tx, _ := modelTransaction(t)
	id := tx.Binding.Index.Binding.Pause.Request.IntentID
	pinned := reusableMaintenanceAuthority{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, RunnerSHA256: strings.Repeat("c", 64), GuardSHA256: strings.Repeat("d", 64)}
	approval := migrationRunnerApproval{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, IntentID: id, BindingDigest: update.InstallDigest(tx.Binding), PreviousCore: tx.Base.CoreHash, TargetCore: tx.Target.CoreHash, TargetSigner: tx.Target.SignerHash, RunnerSHA256: pinned.RunnerSHA256, GuardSHA256: pinned.GuardSHA256}
	files := map[string]hostFile{
		reusableMaintenancePath(tx.Base, "authority.json"):   {UID: 0, Mode: 0644, Regular: true, Data: mustJSON(pinned)},
		reusableMaintenancePath(tx.Base, "reusable-v1.json"): {UID: 0, Mode: 0644, Regular: true, Data: reusableMaintenanceMarkerBytes(pinned)},
		filepath.Join(tx.dir(), "migration-runner.json"):     {UID: 0, Mode: 0644, Regular: true, Data: mustJSON(approval)},
	}
	read := func(path string) (hostFile, error) {
		f, ok := files[path]
		if !ok {
			return hostFile{}, os.ErrNotExist
		}
		return f, nil
	}
	checks := map[string]string{reusableMaintenancePath(tx.Base, "runner"): pinned.RunnerSHA256, reusableMaintenancePath(tx.Base, "atomic-guard"): pinned.GuardSHA256}
	candidate := func(path, hash string) (int64, error) {
		if checks[path] != hash {
			return 0, errors.New("foreign pinned executable")
		}
		return 123, nil
	}
	verify := func(string, string, int64) error {
		return errors.New("transaction staging must not select the executable")
	}
	scope := func(int) (bool, error) { return true, nil }
	want := reusableMaintenancePath(tx.Base, "runner")
	initial, err := selectInitialMigrationRunner(tx.Base, id, read, scope, candidate, verify, func(path string, m Manifest) error {
		if path != want || m.ID != tx.Base.ID {
			return errors.New("foreign initial reader")
		}
		return nil
	})
	if err != nil || initial != want {
		t.Fatal("initial handoff did not use the pinned reader", initial, err)
	}
	recovery, err := selectMigrationRunner(tx, read, scope, candidate, verify, func(path string, got InstallTransaction) error {
		if path != want || update.InstallDigest(got.Binding) != update.InstallDigest(tx.Binding) {
			return errors.New("foreign recovery reader")
		}
		return nil
	})
	if err != nil || recovery != want {
		t.Fatal("recovery did not use the pinned reader", recovery, err)
	}
	// An independently pinned binary can still implement the older controller.
	// Keep the predecessor closed until its transaction-specific approval and
	// serialized OS boundary are available.
	delete(files, filepath.Join(tx.dir(), "migration-runner.json"))
	if _, err = selectMigrationRunner(tx, read, scope, candidate, verify, func(string, InstallTransaction) error { return nil }); err == nil {
		t.Fatal("pinned reader accepted without transaction-specific approval")
	}
	files[filepath.Join(tx.dir(), "migration-runner.json")] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(approval)}
	checks[reusableMaintenancePath(tx.Base, "atomic-guard")] = strings.Repeat("e", 64)
	if _, err = selectInitialMigrationRunner(tx.Base, id, read, scope, candidate, verify, func(string, Manifest) error { return nil }); err == nil {
		t.Fatal("initial handoff accepted a changed pinned guard")
	}
	if _, err = selectMigrationRunner(tx, read, scope, candidate, verify, func(string, InstallTransaction) error { return nil }); err == nil {
		t.Fatal("changed pinned guard was accepted")
	}
}

func TestReusableAuthorityRejectsForeignOrDamagedPins(t *testing.T) {
	tx, _ := modelTransaction(t)
	valid := reusableMaintenanceAuthority{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, RunnerSHA256: strings.Repeat("c", 64), GuardSHA256: strings.Repeat("d", 64)}
	path := reusableMaintenancePath(tx.Base, "authority.json")
	for _, tc := range []struct {
		name string
		file hostFile
	}{
		{"foreign owner", hostFile{UID: tx.Base.Owner, Mode: 0644, Regular: true, Data: mustJSON(valid)}},
		{"writable record", hostFile{UID: 0, Mode: 0664, Regular: true, Data: mustJSON(valid)}},
		{"foreign installation", hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(reusableMaintenanceAuthority{Version: 1, Owner: tx.Base.Owner, InstallationID: strings.Repeat("e", 32), RunnerSHA256: valid.RunnerSHA256, GuardSHA256: valid.GuardSHA256})}},
		{"noncanonical record", hostFile{UID: 0, Mode: 0644, Regular: true, Data: append(mustJSON(valid), '\n')}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readReusableMaintenanceAuthority(tx.Base, func(got string) (hostFile, error) {
				if got != path {
					return hostFile{}, os.ErrNotExist
				}
				return tc.file, nil
			})
			if err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatal("damaged authority accepted or treated as absent")
			}
		})
	}
	if err := verifyReusableMaintenanceAuthority(tx.Base, &valid, valid.RunnerSHA256, valid.GuardSHA256, func(path, digest string) (int64, error) {
		if path == reusableMaintenancePath(tx.Base, "atomic-guard") {
			return 0, errors.New("changed guard")
		}
		return 1, nil
	}); err == nil {
		t.Fatal("changed authority executable accepted")
	}
}

func TestReusableAuthorityDoesNotFallBackWhenPinRecordIsMissing(t *testing.T) {
	tx, _ := modelTransaction(t)
	marker := reusableMaintenancePath(tx.Base, "reusable-v1.json")
	read := func(path string) (hostFile, error) {
		if path == marker {
			return hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte(`{"version":1}`)}, nil
		}
		return hostFile{}, os.ErrNotExist
	}
	if _, err := readReusableMaintenanceAuthority(tx.Base, read); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing reusable authority silently selected the older transaction path")
	}
}

func TestReusableAuthorityDoesNotFallBackWhenScopeIsMissing(t *testing.T) {
	tx, _ := modelTransaction(t)
	pin := reusableMaintenanceAuthority{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, RunnerSHA256: strings.Repeat("c", 64), GuardSHA256: strings.Repeat("d", 64)}
	files := map[string]hostFile{
		reusableMaintenancePath(tx.Base, "authority.json"):   {UID: 0, Mode: 0644, Regular: true, Data: mustJSON(pin)},
		reusableMaintenancePath(tx.Base, "reusable-v1.json"): {UID: 0, Mode: 0644, Regular: true, Data: reusableMaintenanceMarkerBytes(pin)},
	}
	read := func(path string) (hostFile, error) {
		if f, ok := files[path]; ok {
			return f, nil
		}
		return hostFile{}, os.ErrNotExist
	}
	noScope := func(int) (bool, error) { return false, nil }
	for _, check := range []struct {
		name string
		run  func() (string, error)
	}{
		{"initial entry", func() (string, error) {
			return selectInitialMigrationRunner(tx.Base, tx.Binding.Index.Binding.Pause.Request.IntentID, read, noScope, nil, nil, nil)
		}},
		{"recovery entry", func() (string, error) {
			return selectMigrationRunner(tx, read, noScope, nil, nil, nil)
		}},
	} {
		t.Run(check.name, func(t *testing.T) {
			if got, err := check.run(); err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing scope fell back to predecessor: runner=%q err=%v", got, err)
			}
		})
	}
}

func TestScopedMaintenanceRefusesMissingScopeWithPinnedAuthority(t *testing.T) {
	const owner = 1800
	authority := Root(owner) + "/maintenance/authority.json"
	marker := Root(owner) + "/maintenance/reusable-v1.json"
	for _, tc := range []struct {
		name     string
		present  string
		readFail string
		wantErr  bool
	}{
		{name: "ordinary predecessor"},
		{name: "authority exists", present: authority, wantErr: true},
		{name: "marker exists", present: marker, wantErr: true},
		{name: "authority unavailable", readFail: authority, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := func(path string) (hostFile, error) {
				if path == tc.readFail {
					return hostFile{}, errors.New("unavailable")
				}
				if path == tc.present {
					return hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("pinned")}, nil
				}
				return hostFile{}, os.ErrNotExist
			}
			enabled, err := scopedMigrationState(owner, func(string) error { return os.ErrNotExist }, read)
			if enabled || (err != nil) != tc.wantErr {
				t.Fatalf("missing-scope result: enabled=%v err=%v", enabled, err)
			}
		})
	}
}

func TestInitialMigrationRunnerRequiresExactOwnerIntentAndSource(t *testing.T) {
	tx, _ := modelTransaction(t)
	id := tx.Binding.Index.Binding.Pause.Request.IntentID
	a := migrationRunnerApproval{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, IntentID: id, BindingDigest: update.InstallDigest(tx.Binding), PreviousCore: tx.Base.CoreHash, TargetCore: tx.Target.CoreHash, TargetSigner: tx.Target.SignerHash, RunnerSHA256: strings.Repeat("c", 64), GuardSHA256: strings.Repeat("d", 64)}
	dir := filepath.Join("/var/lib", "fased-app-approved-"+strconv.Itoa(tx.Base.Owner))
	for _, scenario := range []string{"legacy", "valid", "missing", "missing-launcher", "missing-source", "foreign-owner", "foreign-intent", "foreign-base", "foreign-marker", "target-as-runner", "changed-source", "invalid-capabilities"} {
		t.Run(scenario, func(t *testing.T) {
			x := a
			switch scenario {
			case "foreign-owner":
				x.Owner++
			case "foreign-intent":
				x.IntentID = strings.Repeat("d", 32)
			case "foreign-base":
				x.PreviousCore = strings.Repeat("e", 64)
			case "target-as-runner":
				x.RunnerSHA256 = tx.Target.CoreHash
			}
			f := hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(x)}
			if scenario == "foreign-marker" {
				f.UID = tx.Base.Owner
			}
			read := func(path string) (hostFile, error) {
				if path != filepath.Join(dir, "migration-"+id+".json") || scenario == "missing" {
					return hostFile{}, os.ErrNotExist
				}
				return f, nil
			}
			candidate := func(path, digest string) (int64, error) {
				if scenario == "missing-launcher" {
					return 0, os.ErrNotExist
				}
				if path != filepath.Join(dir, "migration-launcher-"+id) || digest != a.RunnerSHA256 {
					return 0, errors.New("wrong launcher")
				}
				return 123, nil
			}
			verify := func(path, digest string, size int64) error {
				if scenario == "missing-source" {
					return os.ErrNotExist
				}
				if scenario == "changed-source" || path != filepath.Join(dir, a.RunnerSHA256) || digest != a.RunnerSHA256 || size != 123 {
					return errors.New("wrong source")
				}
				return nil
			}
			probe := func(path string, m Manifest) error {
				if scenario == "invalid-capabilities" || path != filepath.Join(dir, "migration-launcher-"+id) || m.ID != tx.Base.ID {
					return errors.New("wrong capabilities")
				}
				return nil
			}
			got, err := selectInitialMigrationRunner(tx.Base, id, read, func(int) (bool, error) { return scenario != "legacy", nil }, candidate, verify, probe)
			if scenario == "valid" {
				if err != nil || got != filepath.Join(dir, "migration-launcher-"+id) {
					t.Fatal(got, err)
				}
			} else if scenario == "legacy" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("legacy fallback changed", err)
				}
			} else if err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatal("unsafe initial migration accepted or fell back", err)
			}
		})
	}
}

func TestPrepareMigrationRunnerRetainsApprovalAcrossInterruption(t *testing.T) {
	tx, _ := modelTransaction(t)
	if tx.Base.Owner != os.Getuid() {
		t.Skip("model requires the ordinary fixture owner")
	}
	runner := []byte("independent approved runner")
	guard := []byte("independent approved signer guard")
	a := migrationRunnerApproval{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, IntentID: tx.Binding.Index.Binding.Pause.Request.IntentID, BindingDigest: update.InstallDigest(tx.Binding), PreviousCore: tx.Base.CoreHash, TargetCore: tx.Target.CoreHash, TargetSigner: tx.Target.SignerHash, RunnerSHA256: sum(runner), GuardSHA256: sum(guard)}
	plan := mustJSON(a)
	dir := filepath.Join("/var/lib", "fased-app-approved-"+strconv.Itoa(tx.Base.Owner))
	planPath := filepath.Join(dir, "migration-"+a.IntentID+".json")
	h := &memoryHost{files: map[string]hostFile{
		planPath: {Data: bytes.Clone(plan), UID: 0, Mode: 0644, Regular: true},
		ApprovedExecutablePath(tx.Base.Owner, runner): {Data: bytes.Clone(runner), UID: 0, Mode: 0444, Regular: true},
		ApprovedExecutablePath(tx.Base.Owner, guard):  {Data: bytes.Clone(guard), UID: 0, Mode: 0444, Regular: true},
	}}
	verify := func(path, digest string, size int64) error {
		f, err := h.read(path)
		if err != nil || f.UID != 0 || f.Mode != 0444 || !f.Regular || sum(f.Data) != digest || int64(len(f.Data)) != size {
			return errors.New("unapproved source")
		}
		return nil
	}
	probe := func(path string, got InstallTransaction) error {
		f, err := h.read(path)
		if err != nil || !known(f, runner, 0755) || update.InstallDigest(got.Binding) != a.BindingDigest {
			return errors.New("wrong runner")
		}
		return nil
	}
	if err := prepareMigrationRunner(t.Context(), tx, h.read, h.run, verify, probe); err == nil || h.calls != 0 {
		t.Fatal("missing approved plan source reached privileged writes")
	}
	h.files[ApprovedExecutablePath(tx.Base.Owner, plan)] = hostFile{Data: bytes.Clone(plan), UID: 0, Mode: 0444, Regular: true}
	h.failAfter = 1
	if err := prepareMigrationRunner(t.Context(), tx, h.read, h.run, verify, probe); err == nil {
		t.Fatal("interruption was not reported")
	}
	if _, err := h.read(tx.dir() + "/migration-runner.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("approval published before runner verification")
	}
	h.failAfter = 0
	if err := prepareMigrationRunner(t.Context(), tx, h.read, h.run, verify, probe); err != nil {
		t.Fatal(err)
	}
	if f, err := h.read(tx.dir() + "/migration-runner.json"); err != nil || !known(f, plan, 0644) {
		t.Fatal("exact approval not retained", err)
	}
	foreign := a
	foreign.BindingDigest = strings.Repeat("0", 64)
	h.files[planPath] = hostFile{Data: mustJSON(foreign), UID: 0, Mode: 0644, Regular: true}
	before := h.calls
	if err := prepareMigrationRunner(t.Context(), tx, h.read, h.run, verify, probe); err == nil || h.calls != before {
		t.Fatal("foreign plan reached privileged writes")
	}
}

func TestScopedControllerPreflightRefusesMissingApprovedInputs(t *testing.T) {
	tx, _ := modelTransaction(t)
	files := map[string][]byte{
		"runner": []byte("old-core"), "previous-signer": []byte("old-signer"),
		"next-core": []byte("next-core"), "next-signer": []byte("next-signer"),
		"transaction.json": mustJSON(tx),
	}
	for missing, data := range files {
		t.Run(missing, func(t *testing.T) {
			denied := ApprovedExecutablePath(tx.Base.Owner, data)
			verify := func(path, digest string, size int64) error {
				if path == denied {
					return errors.New("approved source missing")
				}
				if !strings.HasSuffix(path, "/"+digest) || size <= 0 {
					return errors.New("unbound source")
				}
				return nil
			}
			if err := preflightScopedControllerInputs(tx, files, verify); err == nil {
				t.Fatal("missing approved source accepted")
			}
		})
	}
	if err := preflightScopedControllerInputs(tx, files, func(string, string, int64) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestScopedRestartDropInsRequireExactRootOwnedAssertions(t *testing.T) {
	tx, _ := modelTransaction(t)
	m := tx.Base
	want := []byte("[Unit]\nAssertPathExists=!" + m.Root + "/update-start-blocked.json\n")
	paths := map[string]hostFile{}
	for _, role := range []string{"app", "signer"} {
		path := "/etc/systemd/system/" + Unit(m.Owner, role) + ".d/20-fased-update-guard.conf"
		paths[path] = hostFile{Data: bytes.Clone(want), UID: 0, Mode: 0644, Regular: true}
	}
	read := func(path string) (hostFile, error) {
		f, ok := paths[path]
		if !ok {
			return hostFile{}, os.ErrNotExist
		}
		return f, nil
	}
	if err := preflightRestartDropIns(m, read); err != nil {
		t.Fatal(err)
	}
	for path, original := range paths {
		delete(paths, path)
		if err := preflightRestartDropIns(m, read); err == nil {
			t.Fatal("missing service assertion accepted", path)
		}
		paths[path] = hostFile{Data: bytes.Clone(want), UID: m.Owner, Mode: 0644, Regular: true}
		if err := preflightRestartDropIns(m, read); err == nil {
			t.Fatal("owner-controlled assertion accepted", path)
		}
		paths[path] = hostFile{Data: []byte("[Unit]\n"), UID: 0, Mode: 0644, Regular: true}
		if err := preflightRestartDropIns(m, read); err == nil {
			t.Fatal("changed assertion accepted", path)
		}
		paths[path] = original
	}
}

func TestScopedControllerRefusesDirectLiveReplacement(t *testing.T) {
	owner := os.Getuid()
	root := Root(owner)
	for _, name := range []string{"fased-app", "fased-signer", "install.json", "intent.json", "ready.json"} {
		args := []string{"/usr/bin/mv", "--no-target-directory", "--", root + "/.fixed-stage", root + "/" + name}
		if err := refuseUnserializedScopedMutation(owner, args, func(int) (bool, error) { return true, nil }); err == nil {
			t.Fatal("scoped direct replacement accepted", name)
		}
		if err := refuseUnserializedScopedMutation(owner, args, func(int) (bool, error) { return false, nil }); err != nil {
			t.Fatal("legacy replacement changed", name, err)
		}
		if err := refuseUnserializedScopedMutation(owner, args, func(int) (bool, error) { return false, errors.New("scope unavailable") }); err == nil {
			t.Fatal("unavailable scope fell back to direct replacement", name)
		}
	}
	other := []string{"/usr/bin/mv", "--no-target-directory", "--", root + "/tmp", root + "/other"}
	if err := refuseUnserializedScopedMutation(owner, other, func(int) (bool, error) { return true, nil }); err != nil {
		t.Fatal("unrelated move changed", err)
	}
}

func TestScopedControllerRefusesEquivalentLiveDestinations(t *testing.T) {
	owner := os.Getuid()
	root := Root(owner)
	for _, args := range [][]string{
		{"/usr/bin/mv", "--", root + "/stage", root + "/other/../fased-signer"},
		{"/usr/bin/install", "--", root + "/stage", root + "/fased-app"},
		{"/usr/bin/ln", "--", root + "/stage", root + "/ready.json"},
		{"/usr/bin/rm", "--", root + "/install.json", root + "/other"},
	} {
		if err := refuseUnserializedScopedMutation(owner, args, func(int) (bool, error) { return true, nil }); err == nil {
			t.Fatalf("scoped live mutation accepted: %v", args)
		}
	}
}

func TestScopedMaintenanceUsesExactPasswordFreeRules(t *testing.T) {
	s := &System{}
	if err := s.configureScopedMaintenance(os.Getuid(), func(int) (bool, error) { return true, nil }); err != nil || !s.NonInteractive {
		t.Fatal("scoped maintenance would request a general sudo password", err)
	}
	s = &System{}
	if err := s.configureScopedMaintenance(os.Getuid(), func(int) (bool, error) { return false, nil }); err != nil || s.NonInteractive {
		t.Fatal("ordinary setup behavior changed", err)
	}
	if err := s.configureScopedMaintenance(os.Getuid(), func(int) (bool, error) { return false, errors.New("unavailable") }); err == nil {
		t.Fatal("unknown scope accepted")
	}
}

func TestControllerRefusesDirectGuardMutationBeforeSudo(t *testing.T) {
	s := &System{NonInteractive: true}
	root := Root(os.Getuid())
	for _, args := range [][]string{
		{"/usr/bin/rm", "--", root + "/update-start-blocked.json"},
		{"/usr/bin/rm", "--", root + "/update-start-blocked.json", root + "/other"},
		{"/usr/bin/rm", "--", root + "/other/../update-start-blocked.json"},
		{"/usr/bin/mv", "--no-target-directory", "--", root + "/update-start-blocked.json", root + "/other"},
		{"/usr/bin/mv", "--no-target-directory", "--", root + "/other", root + "/update-start-blocked.json"},
		{"/usr/bin/install", "-m", "644", "-o", "root", "-g", "root", "--", "/dev/stdin", root + "/update-start-blocked.json"},
		{"/usr/bin/ln", "--no-target-directory", "--", root + "/other", root + "/update-start-blocked.json"},
	} {
		_, err := s.Run(t.Context(), nil, args...)
		if err == nil || !strings.Contains(err.Error(), "root-managed transaction boundary") {
			t.Fatal("direct restart-guard mutation reached the OS", err)
		}
	}
}

// This exercises real process death/re-execution of an independent controller
// against a filesystem model. It does not claim installed OS or signer proof.
func TestScopedMigrationProcessDeathAndFailedTarget(t *testing.T) {
	if dir := os.Getenv("FASED_SCOPED_PROCESS_DIR"); dir != "" {
		raw, err := os.ReadFile(filepath.Join(dir, "transaction-input.json"))
		if err != nil {
			os.Exit(80)
		}
		var tx InstallTransaction
		if strict(raw, &tx) != nil {
			os.Exit(81)
		}
		o := modelOps(t, tx, dir)
		allowed := map[string][]byte{}
		add := func(b []byte) { allowed[sum(b)] = bytes.Clone(b) }
		digest := update.InstallDigest(tx.Binding)
		for _, phase := range []string{"stopped", "core-replaced", "pair-replaced", "inventory-replaced", "started", "healthy-held", "rollback", "rollback-stopped", "rollback-core-replaced", "rollback-pair-replaced", "rollback-inventory-replaced", "rollback-started", "rolled-back-held"} {
			add(mustJSON(phaseRecord{1, digest, phase}))
		}
		add(mustJSON(phaseRecord{1, digest, tx.Binding.Index.Binding.Pause.Request.IntentID}))
		add(mustJSON(update.Installation{Version: 1, Binding: tx.Binding, Digest: digest, Phase: "active"}))
		for _, b := range [][]byte{[]byte("old-core"), []byte("old-signer"), []byte("next-core"), []byte("next-signer"), mustJSON(tx.Base), mustJSON(tx.Target)} {
			add(b)
		}
		o.verify = func(path, d string, size int64) error {
			b, ok := allowed[d]
			if !ok || path != ApprovedExecutablePath(os.Getuid(), b) || size != int64(len(b)) {
				return errors.New("unapproved source")
			}
			return nil
		}
		run := o.run
		o.run = func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
			if args[0] == "/usr/bin/install" && len(args) > 2 && strings.HasPrefix(args[len(args)-2], "/var/lib/fased-app-approved-") {
				data, ok := allowed[filepath.Base(args[len(args)-2])]
				if !ok || len(b) != 0 {
					return nil, errors.New("unbound input")
				}
				b = bytes.Clone(data)
			}
			return run(ctx, b, args...)
		}
		if os.Getenv("FASED_SCOPED_PROCESS_PHASE") == "cut" {
			o.checkpoint = func(phase string) error {
				if phase == "core-replaced" {
					os.Exit(79)
				}
				return nil
			}
		}
		result, err := runTransaction(t.Context(), tx, false, o)
		if err != nil || result == nil || result.Phase != "rolled-back-held" || result.FinancialExecution || result.InstalledSequenceCommitted {
			os.Exit(82)
		}
		if err = os.WriteFile(filepath.Join(dir, "migration-result.json"), mustJSON(result), 0600); err != nil {
			os.Exit(83)
		}
		os.Exit(0)
	}
	tx, dir := modelTransaction(t)
	history, err := os.ReadFile(filepath.Join(dir, "financial-history"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "transaction-input.json"), mustJSON(tx), 0600); err != nil {
		t.Fatal(err)
	}
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(dir, "migration-runner")
	if err = os.WriteFile(runner, self, 0700); err != nil {
		t.Fatal(err)
	}
	if sum(self) == tx.Base.CoreHash || sum(self) == tx.Target.CoreHash {
		t.Fatal("migration reader not independent")
	}
	child := func(phase string) *exec.Cmd {
		c := exec.Command(runner, "-test.run=^TestScopedMigrationProcessDeathAndFailedTarget$")
		c.Env = append(os.Environ(), "FASED_SCOPED_PROCESS_DIR="+dir, "FASED_SCOPED_PROCESS_PHASE="+phase)
		return c
	}
	c := child("cut")
	if err = c.Run(); err == nil || c.ProcessState.ExitCode() != 79 {
		t.Fatal("independent process missed interruption", err)
	}
	held, err := os.ReadFile(filepath.Join(dir, "signer-hold.json"))
	if err != nil {
		t.Fatal("signer hold missing after process death", err)
	}
	if err = os.WriteFile(filepath.Join(dir, "failed-health"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	c = child("resume")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("independent recovery failed: %v %s", err, out)
	}
	after, err := os.ReadFile(filepath.Join(dir, "financial-history"))
	if err != nil || !bytes.Equal(after, history) {
		t.Fatal("history changed", err)
	}
	var before update.Installation
	if strict(held, &before) != nil || before.Phase != "active" {
		t.Fatal("invalid retained hold")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "migration-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result update.Installation
	if strict(raw, &result) != nil || result.Digest != before.Digest || result.Phase != "rolled-back-held" {
		t.Fatal("recovery rebound hold")
	}
}

func TestMigrationContractRequiresSignedCoreAndHeldCapabilities(t *testing.T) {
	tx, _ := modelTransaction(t)
	var contract update.Contract
	for _, c := range tx.Binding.Contracts {
		if c.Component == "core" {
			contract = c
		}
	}
	if contract.Component != "core" {
		t.Fatal("missing model core contract")
	}
	response := func(c update.Contract, wallet, finance, automatic bool) []byte {
		return mustJSON(map[string]any{"contract": c, "walletSigner": wallet, "financialExecution": finance, "updaterEnabled": automatic})
	}
	if err := validateMigrationContract(tx, response(contract, false, false, false)); err != nil {
		t.Fatal(err)
	}
	foreign := contract
	foreign.Protocol++
	for _, bad := range [][]byte{response(foreign, false, false, false), response(contract, true, false, false), response(contract, false, true, false), response(contract, false, false, true), []byte(`{"contract":{}}`)} {
		if validateMigrationContract(tx, bad) == nil {
			t.Fatal("foreign contract or live capability was accepted")
		}
	}
}

func TestMigrationProbeRunsAsOrdinaryOwner(t *testing.T) {
	tx, _ := modelTransaction(t)
	if os.Getuid() != tx.Base.Owner || os.Geteuid() != os.Getuid() {
		t.Skip("fixture requires its ordinary owner")
	}
	var contract update.Contract
	for _, c := range tx.Binding.Contracts {
		if c.Component == "core" {
			contract = c
		}
	}
	body := mustJSON(map[string]any{"contract": contract, "walletSigner": false, "financialExecution": false, "updaterEnabled": false})
	if bytes.Contains(body, []byte("'")) {
		t.Fatal("unexpected fixture character")
	}
	path := filepath.Join(t.TempDir(), "reader")
	script := append([]byte("#!/bin/sh\nprintf '%s' '"), body...)
	script = append(script, []byte("'\n")...)
	if err := os.WriteFile(path, script, 0700); err != nil {
		t.Fatal(err)
	}
	if err := probeMigrationContract(path, tx); err != nil {
		t.Fatal(err)
	}
	tx.Base.Owner++
	if err := probeMigrationContract(path, tx); err == nil {
		t.Fatal("foreign owner ran migration reader")
	}
}
