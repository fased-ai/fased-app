//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Continue the real recovery conductor into the next maintenance guard. Merely
// accepting the new manifest does not establish a coherent installed generation.
func TestEnrollmentRecoveryInstallerIntentBeforeHandoff(t *testing.T) {
	r, dir, o, owner := recoveryModel(t)
	if err := os.WriteFile(dir+"/intent.json", mustJSON(installerIntent(r.Base)), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := recoverEnrollmentCore(t.Context(), r, []byte("corrected-core"), o, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(dir+"/ready.json", readyRecord(m), 0644); err != nil {
		t.Fatal(err)
	}
	o.ownerCurrent = func(want Manifest) error {
		b, e := os.ReadFile(dir + "/owner-generation")
		if e != nil || string(b) != want.CoreHash {
			return errors.New("owner generation mismatch")
		}
		return nil
	}
	if err = ordinaryBaseExact(BootstrapTransaction{Base: m}, o); err != nil {
		t.Fatal("recovery left the next ordinary handoff inconsistent:", err)
	}
}

// Use an actual completed historical recovery record and the ordinary bootstrap
// conductor, not a metadata-only acceptance shim. All OS effects stay in modelOps.
func historicalEnrollmentBootstrapModel(t *testing.T) (BootstrapTransaction, string, installOps, ordinaryBootstrapOps) {
	t.Helper()
	tx, dir, o, c := ordinaryBootstrapModel(t, true)
	previous := tx.Base
	previous.CoreHash = sum([]byte("before-enrollment-recovery"))
	r := enrollmentCoreRecovery{Version: 1, ID: strings.Repeat("d", 32), Base: previous, NextCoreHash: tx.Base.CoreHash}
	write := func(path string, data []byte, mode os.FileMode) {
		t.Helper()
		p := dir + strings.TrimPrefix(path, tx.Base.Root)
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, data, mode); e != nil {
			t.Fatal(e)
		}
	}
	write(tx.Base.Root+"/intent.json", mustJSON(installerIntent(previous)), 0644)
	for _, row := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"recovery.json", mustJSON(r), 0644}, {"previous-core", []byte("before-enrollment-recovery"), 0755},
		{"next-core", []byte("old-core"), 0755}, {"previous-manifest", mustJSON(previous), 0644},
		{"previous-owner-intent.json", mustJSON(recoveryOwnerIntent(previous)), 0644},
	} {
		write(r.dir()+"/"+row.name, row.data, row.mode)
	}
	for _, phase := range []string{"core-replaced", "inventory-replaced", "owner-intent-replaced"} {
		write(r.dir()+"/"+phase+".json", mustJSON(phaseRecord{1, sum(mustJSON(r)), phase}), 0644)
	}
	for _, name := range []string{"signer-state/vault.json", "signer-state/operations.log", "auth-sentinel"} {
		write(tx.Base.Root+"/"+name, []byte("original-"+name), 0600)
	}
	o.recoveryDirs = func(Manifest) ([]string, error) { return []string{"enrollment-core-" + r.ID}, nil }
	return tx, dir, o, c
}

func assertHistoricalEnrollmentPreserved(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"signer-state/vault.json", "signer-state/operations.log", "auth-sentinel"} {
		b, e := os.ReadFile(dir + "/" + name)
		if e != nil || string(b) != "original-"+name {
			t.Fatal("protected source changed", name, e)
		}
	}
}

func TestHistoricalEnrollmentIntentThroughOrdinaryHandoff(t *testing.T) {
	tx, dir, o, c := historicalEnrollmentBootstrapModel(t)
	if err := ordinaryBaseExact(tx, o); err == nil {
		t.Fatal("stale predecessor unexpectedly exact")
	}
	for n := 0; n < 2; n++ {
		receipt, err := runOrdinaryBootstrap(t.Context(), tx, o, c)
		if err != nil {
			t.Fatal("real handoff failed", err)
		}
		if n == 1 && !receipt.AlreadyCurrent {
			t.Fatal("completed handoff was replayed")
		}
	}
	f, e := o.read(tx.Base.Root + "/intent.json")
	if e != nil || !known(f, mustJSON(installerIntent(tx.Target)), 0644) {
		t.Fatal("target root intent incomplete", e)
	}
	assertHistoricalEnrollmentPreserved(t, dir)
}

func TestHistoricalEnrollmentIntentEveryOSInterruption(t *testing.T) {
	tx, _, o, _ := historicalEnrollmentBootstrapModel(t)
	count := 0
	execute := o.run
	o.run = func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
		count++
		return execute(ctx, b, args...)
	}
	if err := reconcileEnrollmentInstallerIntent(t.Context(), tx.Base, o); err != nil {
		t.Fatal(err)
	}
	for at := 1; at <= count; at++ {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-after-%t", at, after), func(t *testing.T) {
				tx, dir, o, c := historicalEnrollmentBootstrapModel(t)
				calls := 0
				failed := false
				execute := o.run
				o.run = func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
					calls++
					if calls == at && !after {
						failed = true
						return nil, errors.New("interrupted installer intent write")
					}
					v, e := execute(ctx, b, args...)
					if calls == at && after && e == nil {
						failed = true
						return nil, errors.New("lost installer intent acknowledgement")
					}
					return v, e
				}
				if _, err := runOrdinaryBootstrap(t.Context(), tx, o, c); err == nil || !failed {
					t.Fatal("interruption missed", err)
				}
				if _, err := o.read(tx.Dir() + "/forward-only.json"); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("interrupted metadata repair crossed fence", err)
				}
				o.run = execute
				for n := 0; n < 2; n++ {
					v, err := runOrdinaryBootstrap(t.Context(), tx, o, c)
					if err != nil || n == 1 && !v.AlreadyCurrent {
						t.Fatal("cold same-transaction recovery failed", err)
					}
				}
				assertHistoricalEnrollmentPreserved(t, dir)
			})
		}
	}
}

func TestHistoricalEnrollmentIntentRefusesForeignEvidence(t *testing.T) {
	for _, mode := range []string{"receipt", "phase", "previous-core", "previous-owner", "root-intent", "ready", "owner", "missing", "duplicates", "unsafe-directory", "active-controller", "enrollment-binding", "missing-enrollment"} {
		t.Run(mode, func(t *testing.T) {
			tx, dir, o, c := historicalEnrollmentBootstrapModel(t)
			root := dir + "/enrollment-core-" + strings.Repeat("d", 32)
			path := map[string]string{"receipt": root + "/recovery.json", "phase": root + "/inventory-replaced.json", "previous-core": root + "/previous-core", "previous-owner": root + "/previous-owner-intent.json", "root-intent": dir + "/intent.json", "ready": dir + "/ready.json", "active-controller": dir + "/update-active.json", "enrollment-binding": dir + "/enrollment-ready.json"}[mode]
			if path != "" {
				if e := os.WriteFile(path, []byte("foreign-preserved"), 0644); e != nil {
					t.Fatal(e)
				}
			}
			switch mode {
			case "missing-enrollment":
				if err := os.Remove(dir + "/enrollment-ready.json"); err != nil {
					t.Fatal(err)
				}
			case "owner":
				o.ownerCurrent = func(Manifest) error { return errors.New("foreign current owner") }
			case "missing":
				o.recoveryDirs = func(Manifest) ([]string, error) { return nil, nil }
			case "duplicates":
				o.recoveryDirs = func(Manifest) ([]string, error) {
					n := "enrollment-core-" + strings.Repeat("d", 32)
					return []string{n, n}, nil
				}
			case "unsafe-directory":
				o.recoveryDirs = func(Manifest) ([]string, error) {
					return []string{"../enrollment-core-" + strings.Repeat("d", 32)}, nil
				}
			}
			before := map[string][]byte{}
			filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
				if e == nil && !d.IsDir() {
					before[p], _ = os.ReadFile(p)
				}
				return e
			})
			writes := 0
			execute := o.run
			o.run = func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
				writes++
				return execute(ctx, b, args...)
			}
			if _, err := runOrdinaryBootstrap(t.Context(), tx, o, c); err == nil {
				t.Fatal("foreign recovery adopted")
			}
			if writes != 0 {
				t.Fatal("refusal performed OS mutations", writes)
			}
			for p, b := range before {
				got, e := os.ReadFile(p)
				if e != nil || !bytes.Equal(got, b) {
					t.Fatal("refusal changed protected file", p, e)
				}
			}
		})
	}
}
