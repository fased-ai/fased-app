//go:build linux

package signer

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func configuredFixture(t *testing.T, dir string) (*Vault, FixturePolicy, FixtureGrant) {
	t.Helper()
	v := openFixture(t, dir)
	if err := v.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	if err := v.Unlock(fixturePassword, time.Hour); err != nil {
		t.Fatal(err)
	}
	ref := *v.Status().Account
	p := FixturePolicy{Account: ref, Asset: FixtureAsset, Budget: 100, MaxOperation: 80, Destinations: []string{"fixture:treasury", "fixture:reject"}, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	g := FixtureGrant{Account: ref, ID: randomID(), BotID: randomID(), Asset: FixtureAsset, Budget: 100, MaxOperation: 80, Destinations: []string{"fixture:treasury", "fixture:reject"}, ExpiresAt: p.ExpiresAt}
	if err := v.SetFixturePolicy(p); err != nil {
		t.Fatal(err)
	}
	if err := v.AddFixtureGrant(g); err != nil {
		t.Fatal(err)
	}
	return v, p, g
}
func intentFor(g FixtureGrant, n uint64) FixtureIntent {
	return FixtureIntent{Account: g.Account, OperationID: randomID(), GrantID: g.ID, BotID: g.BotID, Action: "fixture-transfer", Asset: FixtureAsset, Destination: "fixture:treasury", Units: n, ExpiresAt: g.ExpiresAt}
}
func requireSummary(t *testing.T, v *Vault, spent, held uint64) {
	t.Helper()
	s, e := v.FixtureState()
	if e != nil || s.Spent != spent || s.Held != held || !s.FixtureOnly || s.FinancialExecution {
		t.Fatalf("wrong accounting: %+v %v", s, e)
	}
}

func TestFixtureAuthorityAndImmutableOperations(t *testing.T) {
	v, p, g := configuredFixture(t, fixtureDir(t))
	base := intentFor(g, 20)
	for name, mutate := range map[string]func(*FixtureIntent){
		"asset":           func(i *FixtureIntent) { i.Asset = "SOL" },
		"destination":     func(i *FixtureIntent) { i.Destination = "arbitrary-address" },
		"amount":          func(i *FixtureIntent) { i.Units = 81 },
		"zero":            func(i *FixtureIntent) { i.Units = 0 },
		"epoch":           func(i *FixtureIntent) { i.Account.Epoch = randomID() },
		"signer":          func(i *FixtureIntent) { i.Account.SignerID = randomID() },
		"network":         func(i *FixtureIntent) { i.Account.Network = "solana" },
		"public-key":      func(i *FixtureIntent) { i.Account.PublicKey = strings.Repeat("0", 64) },
		"bot":             func(i *FixtureIntent) { i.BotID = randomID() },
		"grant":           func(i *FixtureIntent) { i.GrantID = randomID() },
		"action":          func(i *FixtureIntent) { i.Action = "sign-message" },
		"expiry":          func(i *FixtureIntent) { i.ExpiresAt = time.Now().Add(-time.Second).Unix() },
		"extended-expiry": func(i *FixtureIntent) { i.ExpiresAt = g.ExpiresAt + 1 },
	} {
		t.Run(name, func(t *testing.T) { i := base; mutate(&i); _, e := v.ReserveFixture(i); requireError(t, e) })
	}
	if _, e := v.ReserveFixture(base); e != nil {
		t.Fatal(e)
	}
	requireSummary(t, v, 0, 20)
	if _, e := v.ReserveFixture(base); e != nil {
		t.Fatal("idempotent identity", e)
	}
	requireSummary(t, v, 0, 20)
	changed := base
	changed.Units++
	_, e := v.ReserveFixture(changed)
	requireError(t, e)
	result, e := v.ExecuteFixture(base.OperationID)
	if e != nil || result.State != "confirmed" || len(result.Original) == 0 {
		t.Fatalf("execute: %+v %v", result, e)
	}
	requireSummary(t, v, 20, 0)
	if _, e = v.ExecuteFixture(base.OperationID); e != nil {
		t.Fatal(e)
	}
	if len(v.ledger.receipts) != 1 {
		t.Fatal("duplicate mock send")
	}
	p.Budget = 19
	requireError(t, v.SetFixturePolicy(p))
	p.Budget = 100
	g.Revoked = true
	g.ID = randomID()
	requireError(t, v.AddFixtureGrant(g))
	g.Revoked = false
	g.ID = randomID()
	g.Destinations = []string{"fixture:unknown"}
	requireError(t, v.AddFixtureGrant(g))
	v.Lock()
	requireError(t, v.SetFixturePolicy(p))
	requireError(t, v.AddFixtureGrant(g))
}

func TestFixtureCrossBotReservationsAndRevocation(t *testing.T) {
	v, _, g := configuredFixture(t, fixtureDir(t))
	other := g
	other.ID = randomID()
	other.BotID = randomID()
	if e := v.AddFixtureGrant(other); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := []FixtureIntent{}
	for _, grant := range []FixtureGrant{g, other} {
		for range 10 {
			wg.Go(func() {
				i := intentFor(grant, 30)
				if _, e := v.ReserveFixture(i); e == nil {
					mu.Lock()
					success = append(success, i)
					mu.Unlock()
				}
			})
		}
	}
	wg.Wait()
	if len(success) != 3 {
		t.Fatalf("cross-bot cap allowed %d reservations", len(success))
	}
	requireSummary(t, v, 0, 90)
	i := success[0]
	v.Lock()
	if e := v.RevokeFixtureGrant(i.Account, i.GrantID); e != nil {
		t.Fatal(e)
	}
	if e := v.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	_, e := v.ExecuteFixture(i.OperationID)
	requireError(t, e)
	requireSummary(t, v, 0, 90)
	if e := v.AddFixtureGrant(g); e == nil {
		t.Fatal("grant ID reused")
	}
	if _, e = v.ReconcileFixture(i.OperationID); e != nil {
		t.Fatal(e)
	}
	requireSummary(t, v, 0, 60)
	_, e = v.ExecuteFixture(i.OperationID)
	if e != nil {
		t.Fatal(e)
	}
	if len(v.ledger.receipts) != 0 {
		t.Fatal("revoked operation sent")
	}
}

func TestFixtureExpiryOverflowAndRejectedProof(t *testing.T) {
	v, p, g := configuredFixture(t, fixtureDir(t))
	i := intentFor(g, 10)
	if _, e := v.ReserveFixture(i); e != nil {
		t.Fatal(e)
	}
	v.now = func() time.Time { return time.Unix(g.ExpiresAt, 0) }
	_, e := v.ExecuteFixture(i.OperationID)
	requireError(t, e)
	requireSummary(t, v, 0, 10)
	if _, e = v.ReconcileFixture(i.OperationID); e != nil {
		t.Fatal(e)
	}
	requireSummary(t, v, 0, 0)
	v.now = func() time.Time { return time.Unix(g.ExpiresAt-1, 0) }
	p.ExpiresAt += 100
	requireError(t, v.SetFixturePolicy(p)) // clock rollback fails closed

	v, p, g = configuredFixture(t, fixtureDir(t))
	p.Budget = ^uint64(0)
	p.MaxOperation = p.Budget
	if e = v.SetFixturePolicy(p); e != nil {
		t.Fatal(e)
	}
	g.ID = randomID()
	g.Budget = p.Budget
	g.MaxOperation = p.Budget
	if e = v.AddFixtureGrant(g); e != nil {
		t.Fatal(e)
	}
	i = intentFor(g, ^uint64(0)-5)
	if _, e = v.ReserveFixture(i); e != nil {
		t.Fatal(e)
	}
	_, e = v.ReserveFixture(intentFor(g, 6))
	requireError(t, e)
	if _, e = v.ExecuteFixture(i.OperationID); e != nil {
		t.Fatal(e)
	}
	requireSummary(t, v, ^uint64(0)-5, 0)
	r := intentFor(g, 5)
	r.Destination = "fixture:reject"
	if _, e = v.ReserveFixture(r); e != nil {
		t.Fatal(e)
	}
	op, e := v.ExecuteFixture(r.OperationID)
	if e != nil || op.State != "rejected" {
		t.Fatal("mock rejection", e)
	}
	requireSummary(t, v, ^uint64(0)-5, 0)
}

// Each child terminates immediately at a real persistence/send boundary without
// running defers. Only generated nonauthoritative fixtures are selected; no
// runtime flag or RPC enables these package-private injection hooks.
func TestFixtureProcessCrashAndReconciliation(t *testing.T) {
	if dir := os.Getenv("FASED_POLICY_CRASH_DIR"); dir != "" {
		if os.Geteuid() == 0 || !strings.Contains(dir, "/.evidence/p3-signer/unit-") {
			os.Exit(90)
		}
		v, e := Open(dir, "11111111111111111111111111111111")
		if e != nil {
			os.Exit(91)
		}
		if e = v.Unlock(fixturePassword, time.Minute); e != nil {
			os.Exit(92)
		}
		phase := os.Getenv("FASED_POLICY_CRASH_PHASE")
		v.store.crash = func(p string) error {
			if p == phase {
				os.Exit(83)
			}
			return nil
		}
		if phase == "journal:reserve:before" || phase == "journal:reserve:durable" {
			g := v.ledger.grants[os.Getenv("FASED_POLICY_CRASH_GRANT")]
			i := intentFor(g, 70)
			i.OperationID = os.Getenv("FASED_POLICY_CRASH_ID")
			v.ReserveFixture(i)
		} else {
			v.ExecuteFixture(os.Getenv("FASED_POLICY_CRASH_ID"))
		}
		os.Exit(93)
	}
	for _, phase := range []string{"journal:reserve:before", "journal:reserve:durable", "journal:prepare:before", "journal:prepare:written", "journal:prepare:durable", "journal:sending:durable", "mock:before-send", "journal:mock-receipt:durable", "mock:after-send", "journal:outcome:before", "journal:outcome:durable"} {
		t.Run(phase, func(t *testing.T) {
			dir := fixtureDir(t)
			v, _, g := configuredFixture(t, dir)
			i := intentFor(g, 70)
			reserving := strings.Contains(phase, "reserve:")
			if !reserving {
				if _, e := v.ReserveFixture(i); e != nil {
					t.Fatal(e)
				}
			}
			v.Close()
			cmd := exec.Command(os.Args[0], "-test.run=^TestFixtureProcessCrashAndReconciliation$")
			cmd.Env = append(os.Environ(), "FASED_POLICY_CRASH_DIR="+dir, "FASED_POLICY_CRASH_PHASE="+phase, "FASED_POLICY_CRASH_ID="+i.OperationID, "FASED_POLICY_CRASH_GRANT="+g.ID)
			b, e := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 83 {
				t.Fatalf("crash hook not reached: %v %s", e, b)
			}
			v = openFixture(t, dir)
			if !v.Status().Locked {
				t.Fatal("restart unlocked")
			}
			if phase == "journal:reserve:before" {
				requireSummary(t, v, 0, 0)
				return
			}
			if phase == "journal:outcome:durable" {
				requireSummary(t, v, 70, 0)
			} else {
				requireSummary(t, v, 0, 70)
			}
			if e = v.Unlock(fixturePassword, time.Minute); e != nil {
				t.Fatal(e)
			}
			_, e = v.ReserveFixture(intentFor(g, 40))
			requireError(t, e)
			op, e := v.FixtureOperation(i.OperationID)
			if e != nil {
				t.Fatal(e)
			}
			if op.State == "prepared" || op.State == "sending" {
				_, e = v.ExecuteFixture(i.OperationID)
				if !errors.Is(e, ErrHeld) {
					t.Fatal("uncertain original retried", e)
				}
			}
			op, e = v.ReconcileFixture(i.OperationID)
			if e != nil {
				t.Fatal(e)
			}
			expectSent := phase == "journal:mock-receipt:durable" || phase == "mock:after-send" || phase == "journal:outcome:before" || phase == "journal:outcome:durable"
			if expectSent {
				if op.State != "confirmed" || len(v.ledger.receipts) != 1 {
					t.Fatal("lost submitted proof")
				}
				requireSummary(t, v, 70, 0)
			} else {
				if op.State != "not-submitted" || len(v.ledger.receipts) != 0 {
					t.Fatal("unsubmitted result")
				}
				requireSummary(t, v, 0, 0)
			}
			before := len(v.ledger.receipts)
			if _, e = v.ExecuteFixture(i.OperationID); e != nil {
				t.Fatal(e)
			}
			if len(v.ledger.receipts) != before {
				t.Fatal("reconciliation resent original")
			}
		})
	}
}

func TestFixtureRecoveryRetainsLifetimeBudgetAndRevokesGrants(t *testing.T) {
	dir := fixtureDir(t)
	v, p, g := configuredFixture(t, dir)
	backup, e := v.Backup(backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	i := intentFor(g, 70)
	if _, e = v.ReserveFixture(i); e != nil {
		t.Fatal(e)
	}
	v.store.crash = func(s string) error {
		if s == "mock:after-send" {
			return errors.New("fixture interruption")
		}
		return nil
	}
	_, e = v.ExecuteFixture(i.OperationID)
	requireError(t, e)
	v.Close()
	// Simulate verified disposable key loss only. Real product removal never
	// deletes a vault or either financial journal.
	if e = os.Remove(filepath.Join(dir, "vault.json")); e != nil {
		t.Fatal(e)
	}
	v = openFixture(t, dir)
	if e = v.Restore(backup, backupPassword, fixturePassword); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	if !v.Status().RecoveryRequired || ref.Epoch == g.Account.Epoch {
		t.Fatal("restore hold/epoch")
	}
	if e = v.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	p.Account = ref
	requireError(t, v.SetFixturePolicy(p))
	if e = v.RecoverFixtureAuthority(ref); e != nil {
		t.Fatal(e)
	}
	if v.Status().RecoveryRequired {
		t.Fatal("complete mock history not reconciled")
	}
	requireSummary(t, v, 70, 0)
	if v.ledger.policy != nil || !v.ledger.grants[g.ID].Revoked {
		t.Fatal("old authority survived recovery")
	}
	if e = v.SetFixturePolicy(p); e != nil {
		t.Fatal(e)
	}
	g.Account = ref
	g.ID = randomID()
	if e = v.AddFixtureGrant(g); e != nil {
		t.Fatal(e)
	}
	_, e = v.ReserveFixture(intentFor(g, 31))
	requireError(t, e)
	if _, e = v.ReserveFixture(intentFor(g, 30)); e != nil {
		t.Fatal(e)
	}
	v.Close()
	v = openFixture(t, dir)
	requireSummary(t, v, 70, 30)
	if v.Status().RecoveryRequired {
		t.Fatal("recovery proof lost on restart")
	}

	other := openFixture(t, fixtureDir(t))
	if e = other.Restore(backup, backupPassword, fixturePassword); e != nil {
		t.Fatal(e)
	}
	if e = other.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	requireError(t, other.RecoverFixtureAuthority(*other.Status().Account))
	if !other.Status().RecoveryRequired {
		t.Fatal("key-only backup reset budget")
	}
}

func TestFixtureHistoryDamageMissingFilesAndUncertainWrites(t *testing.T) {
	for _, which := range []string{"operations.log", "mock-receipts.log"} {
		for _, damage := range []string{"missing", "torn", "corrupt", "symlink", "hardlink"} {
			t.Run(which+"/"+damage, func(t *testing.T) {
				dir := fixtureDir(t)
				v, _, _ := configuredFixture(t, dir)
				v.Close()
				p := filepath.Join(dir, which)
				b, e := os.ReadFile(p)
				if e != nil {
					t.Fatal(e)
				}
				switch damage {
				case "missing":
					e = os.Remove(p)
				case "torn":
					e = os.WriteFile(p, b[:len(b)-1], 0600)
				case "corrupt":
					e = os.WriteFile(p, bytes.Replace(b, []byte("\"version\":1"), []byte("\"version\":2"), 1), 0600)
				case "symlink":
					if e = os.Rename(p, p+".original"); e == nil {
						e = os.Symlink(p+".original", p)
					}
				case "hardlink":
					e = os.Link(p, p+".link")
				}
				if e != nil {
					t.Fatal(e)
				}
				if opened, e := Open(dir, v.signerID); e == nil {
					opened.Close()
					t.Fatal("unsafe history accepted")
				}
				if _, e = os.Lstat(p); e != nil && damage != "missing" {
					t.Fatal("damaged state removed")
				}
			})
		}
	}
	v, _, g := configuredFixture(t, fixtureDir(t))
	v.store.crash = func(s string) error {
		if s == "journal:reserve:durable" {
			return errors.New("uncertain fixture storage")
		}
		return nil
	}
	_, e := v.ReserveFixture(intentFor(g, 70))
	requireError(t, e)
	_, e = v.ReserveFixture(intentFor(g, 70))
	requireError(t, e)
}

func TestFixtureRPCSeparatesOwnerAuthorityFromAppRequests(t *testing.T) {
	c, v := protocolFixture(t, os.Geteuid(), os.Geteuid())
	if e := v.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	if e := v.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	p := FixturePolicy{Account: ref, Asset: FixtureAsset, Budget: 100, MaxOperation: 80, Destinations: []string{"fixture:treasury"}, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	g := FixtureGrant{Account: ref, ID: randomID(), BotID: randomID(), Asset: FixtureAsset, Budget: 100, MaxOperation: 80, Destinations: p.Destinations, ExpiresAt: p.ExpiresAt}
	for _, r := range []Request{
		{Action: "fixture-policy", PublicInput: PublicInput{Policy: &p}},
		{Action: "fixture-grant", PublicInput: PublicInput{Grant: &g}},
		{Action: "fixture-revoke", PublicInput: PublicInput{Account: &ref, GrantID: g.ID}},
		{Action: "fixture-recover-authority", PublicInput: PublicInput{Account: &ref}},
	} {
		if response := protocolRequest(t, c, false, r); response.Error == "" {
			t.Fatal("app acquired owner authority", r.Action)
		}
	}
	for _, r := range []Request{{Action: "fixture-policy", PublicInput: PublicInput{Policy: &p}}, {Action: "fixture-grant", PublicInput: PublicInput{Grant: &g}}} {
		if r := protocolRequest(t, c, true, r); r.Error != "" {
			t.Fatal(r.Error)
		}
	}
	i := intentFor(g, 70)
	r := protocolRequest(t, c, false, Request{Action: "fixture-reserve", PublicInput: PublicInput{Intent: &i}})
	if r.Error != "" || r.Operation == nil || r.Operation.State != "reserved" {
		t.Fatal("app bounded request", r.Error)
	}
	r = protocolRequest(t, c, false, Request{Action: "fixture-execute", PublicInput: PublicInput{OperationID: i.OperationID}})
	if r.Error != "" || r.Operation.State != "confirmed" || r.Status.FinancialExecution || r.Document != nil {
		t.Fatal("fixture execution leaked authority")
	}
	if len(v.ledger.receipts) != 1 {
		t.Fatal(strconv.Itoa(len(v.ledger.receipts)))
	}
}

func TestFixtureInitializationResumesSameEncryptedKey(t *testing.T) {
	for _, phase := range []string{"journal:mock-init:before", "journal:mock-init:durable", "journal:init:before", "journal:init:durable"} {
		t.Run(phase, func(t *testing.T) {
			dir := fixtureDir(t)
			v := openFixture(t, dir)
			v.store.crash = func(s string) error {
				if s == phase {
					return errors.New("disposable init interruption")
				}
				return nil
			}
			requireError(t, v.Create(fixturePassword))
			b, e := os.ReadFile(filepath.Join(dir, "vault.pending"))
			if e != nil {
				t.Fatal(e)
			}
			var pending envelope
			if e = decode(b, &pending); e != nil {
				t.Fatal(e)
			}
			v.Close()
			v = openFixture(t, dir)
			if v.Status().Account == nil || *v.Status().Account != pending.Ref || v.Status().RecoveryRequired || !v.Status().Locked {
				t.Fatal("initialization lost/replaced key or reset history")
			}
			if e = v.Unlock(fixturePassword, time.Minute); e != nil {
				t.Fatal(e)
			}
			requireSummary(t, v, 0, 0)
		})
	}
}

func TestPublicFixtureInputRejectsCredentialsAndLossyAmounts(t *testing.T) {
	for _, document := range []string{`{"password":"never forwarded"}`, `{"document":"never imported"}`, `{"owner":true}`, `{} {}`, strings.Repeat(" ", maxDocument+1), `{"intent":{"units":1}}`, `{"intent":{"units":"1.5"}}`, `{"intent":{"units":"-1"}}`, `{"intent":{"units":"18446744073709551616"}}`} {
		_, e := ParsePublicInput([]byte(document))
		requireError(t, e)
	}
	i, e := ParsePublicInput([]byte(`{"intent":{"units":"18446744073709551615"}}`))
	if e != nil || i.Intent.Units != ^uint64(0) {
		t.Fatal("integer precision", e)
	}
}
