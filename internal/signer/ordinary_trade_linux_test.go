//go:build linux

package signer

import (
	"bytes"
	"errors"
	"github.com/fased-ai/fased-app/internal/onchaintest"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ordinaryTradeFixture(t *testing.T, origin string) (*Vault, SwapProposal, *onchaintest.Chain, string, []byte) {
	t.Helper()
	dir := fixtureDir(t)
	v, err := OpenControlledOwnerCustody(dir, strings.Repeat("a", 32), "mainnet-beta")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	pw := []byte("disposable ordinary typed approval password")
	chain := &onchaintest.Chain{}
	if err = v.ConfigureControlledOrdinarySwapObserver(onchaintest.Client(chain)); err != nil {
		t.Fatal(err)
	}
	if origin == "imported" {
		err = v.ImportControlledOrdinarySeed(bytes.Repeat([]byte{9}, 32), pw)
	} else {
		err = v.Create(pw)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = v.Unlock(pw, time.Minute); err != nil {
		t.Fatal(err)
	}
	return v, ordinaryTradeQuote(t, *v.Status().Account, chain), chain, dir, pw
}
func tradeCall(v *Vault, action string, p SwapProposal, owner bool) Response {
	r := Request{Action: action, PublicInput: PublicInput{OperationID: p.Quote.ID}}
	if action == "swap-approve" {
		r.OperationID = ""
		r.Swap = &p
	}
	return execute(v, r, owner)
}
func TestOrdinaryTradeOwnerBindingReceiptRestartAndMaintenance(t *testing.T) {
	v, p, chain, dir, pw := ordinaryTradeFixture(t, "generated")
	before, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	ref := *v.Status().Account
	if !v.Status().RecoveryRequired || !v.Status().AdmissionHeld || !v.Status().SwapReady || v.Status().FinancialExecution {
		t.Fatal("general hold/capability changed")
	}
	if r := tradeCall(v, "swap-approve", p, false); r.Error == "" {
		t.Fatal("app approved order")
	}
	if r := tradeCall(v, "swap-sign", p, false); r.Error == "" {
		t.Fatal("unapproved order signed")
	}
	if r := tradeCall(v, "swap-approve", p, true); r.Error != "" {
		t.Fatal(r.Error)
	}
	if r := tradeCall(v, "swap-approve", p, true); r.Error == "" {
		t.Fatal("approval replay admitted")
	}
	signed := tradeCall(v, "swap-sign", p, false)
	if signed.Error != "" || signed.Swap == nil || len(signed.Swap.Signed) == 0 {
		t.Fatal(signed.Error)
	}
	again := tradeCall(v, "swap-sign", p, false)
	if again.Error != "" || !bytes.Equal(signed.Swap.Signed, again.Swap.Signed) {
		t.Fatal("not idempotent")
	}
	chain.Unknown = true
	if _, err := onchaintest.Client(chain).Send(t.Context(), p.Quote, signed.Swap.Signed); err != nil {
		t.Fatal(err)
	}
	inv, err := v.UpdateInventory(update.NewProbe())
	if err != nil || inv.Pending != 1 || inv.Unknown != 1 || !inv.RecoveryHold || inv.Spent != 0 || inv.Held != 0 {
		t.Fatal(inv, err)
	}
	if _, err = v.PrepareUpdate(intentFixture(t, v, 1)); err == nil {
		t.Fatal("pending order did not block update")
	}
	if r := tradeCall(v, "swap-cancel", p, true); r.Error == "" {
		t.Fatal("signed original cancelled")
	}
	// Backups include the durable unknown original and keep old archive contracts.
	archive, err := v.BackupHistory(pw)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.VerifyBackup(archive, pw); err != nil {
		t.Fatal(err)
	}
	restoredDir := fixtureDir(t)
	restored, err := OpenControlledOwnerCustody(restoredDir, ref.SignerID, ref.Network)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err = restored.RestoreHistory(archive, pw, []byte("disposable restored ordinary password"), BackupFingerprint(archive)); err != nil {
		t.Fatal(err)
	}
	if err = restored.ConfigureControlledOrdinarySwapObserver(onchaintest.Client(chain)); err != nil {
		t.Fatal(err)
	}
	if restored.Status().SwapReady || !restored.Status().RecoveryRequired {
		t.Fatal("historical recovery gained permission")
	}
	if r := tradeCall(restored, "swap-reconcile", p, false); r.Error == "" {
		t.Fatal("historical recovery mutated its held journal")
	}
	if r := tradeCall(restored, "swap-sign", p, true); r.Error == "" {
		t.Fatal("restored key signed")
	}
	hist, err := restored.UpdateInventory(update.NewProbe())
	if err != nil || hist.Pending != 1 || hist.Unknown != 1 || !hist.RecoveryHold {
		t.Fatal(hist, err)
	}
	v.Close()
	v, err = OpenControlledOwnerCustody(dir, ref.SignerID, ref.Network)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if r := tradeCall(v, "swap-receipt", p, true); r.Error == "" {
		t.Fatal("ordinary startup enabled operation transport")
	}
	if err = v.ConfigureControlledOrdinarySwapObserver(onchaintest.Client(chain)); err != nil {
		t.Fatal(err)
	}
	receipt := tradeCall(v, "swap-receipt", p, false)
	if receipt.Error != "" || !bytes.Equal(receipt.Swap.Signed, signed.Swap.Signed) {
		t.Fatal("lost durable original", receipt.Error)
	}
	chain.Unknown = false
	settled := tradeCall(v, "swap-reconcile", p, false)
	if settled.Error != "" || settled.Swap.State != "confirmed" {
		t.Fatal("locked independent reconciliation", settled.Error)
	}
	inv, err = v.UpdateInventory(update.NewProbe())
	if err != nil || inv.Pending != 0 || inv.Unknown != 0 || !inv.RecoveryHold || !v.Status().Locked {
		t.Fatal(inv, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	if !bytes.Equal(before, after) || *v.Status().Account != ref || chain.Submissions != 1 {
		t.Fatal("keys/epoch changed or signer submitted")
	}
	_, records, err := v.store.readJournal("operations.log")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records[1:] {
		if !strings.HasPrefix(r.Kind, "ordinary-trade-") {
			t.Fatal("legacy authority domain used", r.Kind)
		}
	}
}
func TestOrdinaryTradeRefusesImportedExpiredMixedAndRevoked(t *testing.T) {
	for _, mode := range []string{"imported", "expired", "mixed", "network", "wallet", "rpc", "lock", "restart"} {
		t.Run(mode, func(t *testing.T) {
			origin := "generated"
			if mode == "imported" {
				origin = mode
			}
			v, p, chain, dir, pw := ordinaryTradeFixture(t, origin)
			if mode == "expired" {
				v.now = func() time.Time { return time.Unix(p.Quote.ExpiresAt, 0) }
			}
			if mode == "network" {
				p.Quote.Network = "devnet"
				p.Digest = SwapProposalDigest(p)
			}
			if mode == "wallet" {
				p.Account.Epoch = strings.Repeat("b", 32)
				p.Digest = SwapProposalDigest(p)
			}
			if mode == "rpc" {
				p.Quote.RPCSHA256 = "invalid"
				p.Digest = SwapProposalDigest(p)
			}
			if mode == "mixed" {
				r := Request{Action: "swap-approve", Password: []byte("unrelated"), PublicInput: PublicInput{Swap: &p}}
				if execute(v, r, true).Error == "" {
					t.Fatal("mixed fields admitted")
				}
				return
			}
			if mode != "lock" && mode != "restart" {
				if r := tradeCall(v, "swap-approve", p, true); r.Error == "" {
					t.Fatal("invalid approval", mode)
				}
				return
			}
			if r := tradeCall(v, "swap-approve", p, true); r.Error != "" {
				t.Fatal(r.Error)
			}
			if mode == "restart" {
				v.Close()
				var err error
				v, err = OpenControlledOwnerCustody(dir, p.Account.SignerID, p.Account.Network)
				if err != nil {
					t.Fatal(err)
				}
				defer v.Close()
				if err = v.ConfigureControlledOrdinarySwapObserver(onchaintest.Client(chain)); err != nil {
					t.Fatal(err)
				}
			} else {
				v.Lock()
			}
			if err := v.Unlock(pw, time.Minute); err != nil {
				t.Fatal(err)
			}
			if r := tradeCall(v, "swap-sign", p, false); r.Error == "" {
				t.Fatal("revoked/old-boot approval signed")
			}
			if r := tradeCall(v, "swap-cancel", p, true); r.Error != "" || r.Swap.State != "cancelled" {
				t.Fatal("cannot cancel revoked unsigned original", r.Error)
			}
		})
	}
}

func TestOrdinaryTradeUncertainAppendAndReaderNegotiation(t *testing.T) {
	v, p, chain, dir, _ := ordinaryTradeFixture(t, "generated")
	if r := tradeCall(v, "swap-approve", p, true); r.Error != "" {
		t.Fatal(r.Error)
	}
	v.store.crash = func(point string) error {
		if point == "journal:ordinary-trade-sign:durable" {
			return errors.New("lost signing response")
		}
		return nil
	}
	if r := tradeCall(v, "swap-sign", p, false); r.Error == "" {
		t.Fatal("durable interruption not injected")
	}
	if _, err := v.UpdateInventory(update.NewProbe()); err == nil {
		t.Fatal("uncertain append not held")
	}
	v.Close()
	var err error
	v, err = OpenControlledOwnerCustody(dir, p.Account.SignerID, p.Account.Network)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err = v.ConfigureControlledOrdinarySwapObserver(onchaintest.Client(chain)); err != nil {
		t.Fatal(err)
	}
	receipt := tradeCall(v, "swap-receipt", p, false)
	if receipt.Error != "" || receipt.Swap == nil || receipt.Swap.State != "signed" {
		t.Fatal("lost durable signed original", receipt.Error)
	}
	inv, err := v.UpdateInventory(update.NewProbe())
	if err != nil || inv.Pending != 1 || inv.Unknown != 1 {
		t.Fatal(inv, err)
	}
	if _, err = onchaintest.Client(chain).Send(t.Context(), p.Quote, receipt.Swap.Signed); err != nil {
		t.Fatal(err)
	}
	if r := tradeCall(v, "swap-reconcile", p, false); r.Error != "" || r.Swap.State != "confirmed" {
		t.Fatal("cannot reconcile interrupted original", r.Error)
	}
	request := intentFixture(t, v, 1)
	trust, err := update.ParseTrust(request.Trust, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := update.Authenticate(request.Manifest, trust, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := update.JoinSnapshots(request.Core, request.Signer, manifest.Target)
	if err != nil {
		t.Fatal("trade inventory invalid", err)
	}
	if _, err = update.Compatible(manifest, digest, inventory); err != nil {
		t.Fatal("reviewed reader cannot maintain settled record", err)
	}
	old := clone(manifest)
	for i := range old.Components {
		if old.Components[i].Contract.Component == "signer" {
			for j := range old.Components[i].Contract.State {
				s := &old.Components[i].Contract.State[j]
				if s.Name == "operations" {
					fs := []string{}
					for _, f := range s.Features {
						if f != ordinaryTradeFeature {
							fs = append(fs, f)
						}
					}
					s.Features = fs
				}
			}
		}
	}
	if _, err = update.Compatible(old, digest, inventory); err == nil {
		t.Fatal("reader without ordinary trade support accepted new journal")
	}
}

func TestOrdinaryTradeExistingAndKeyOnlyRestoredRemainHeld(t *testing.T) {
	for _, mode := range []string{"existing", "key-only-restored"} {
		t.Run(mode, func(t *testing.T) {
			dir := fixtureDir(t)
			v, err := OpenControlledOwnerCustody(dir, strings.Repeat("a", 32), "mainnet-beta")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { v.Close() }()
			pw := []byte("disposable existing ordinary key password")
			if err = v.Create(pw); err != nil {
				t.Fatal(err)
			}
			if err = v.Unlock(pw, time.Minute); err != nil {
				t.Fatal(err)
			}
			if mode == "key-only-restored" {
				archive, e := v.Backup(pw)
				if e != nil {
					t.Fatal(e)
				}
				v.Close()
				v, err = OpenControlledOwnerCustody(fixtureDir(t), strings.Repeat("a", 32), "mainnet-beta")
				if err != nil {
					t.Fatal(err)
				}
				if err = v.ConfigureControlledOrdinarySwapObserver(onchaintest.Client(&onchaintest.Chain{})); err != nil {
					t.Fatal(err)
				}
				if err = v.Restore(archive, pw, pw); err != nil {
					t.Fatal(err)
				}
				if err = v.Unlock(pw, time.Minute); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(v.store.path, "operations.log"))
			if err = v.ConfigureControlledOrdinarySwapObserver(onchaintest.Client(&onchaintest.Chain{})); err != nil {
				t.Fatal(err)
			}
			p := ordinaryTradeQuote(t, *v.Status().Account, &onchaintest.Chain{})
			if v.Status().SwapReady || !v.Status().RecoveryRequired {
				t.Fatal("old/restored key acquired capability")
			}
			if r := tradeCall(v, "swap-approve", p, true); r.Error == "" {
				t.Fatal("old/restored key approved")
			}
			after, _ := os.ReadFile(filepath.Join(v.store.path, "operations.log"))
			if !bytes.Equal(before, after) {
				t.Fatal("preflight/approval changed held history")
			}
		})
	}
}
