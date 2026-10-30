package signer

import (
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/onchain"
	"os"
	"strings"
	"testing"
)

// Public signed bytes use a disposable key and an inert blockhash. This same
// reader test is copied into the explicitly composed historical fixtures.
func TestSwapStateReader(t *testing.T) {
	var vector struct {
		Proposal    SwapProposal        `json:"proposal"`
		Signed      []byte              `json:"signed"`
		Observation onchain.Observation `json:"observation"`
	}
	raw, e := os.ReadFile("testdata/onchain-reader.json")
	if e != nil || json.Unmarshal(raw, &vector) != nil {
		t.Fatal("public reader vector", e)
	}
	p := vector.Proposal
	if p.Quote.Blockhash != onchain.ControlledBlockhash {
		t.Fatal("reader vector is not inert")
	}
	fresh := func() *ledger { l := newLedger(); l.ref = p.Account; return l }
	record := func(kind string) journalRecord {
		return journalRecord{Kind: kind, ID: p.Quote.ID, At: p.Quote.CreatedAt}
	}
	approve := func(l *ledger) {
		r := record("onchain-approve")
		r.Swap = &p
		if e := l.apply(r); e != nil {
			t.Fatal(e)
		}
	}
	feature := []string{"originals-v1", "onchain-swap-fixture-v1"}
	empty := fresh()
	if len(empty.swapStateFeatures(feature)) != 1 {
		t.Fatal("unused feature changed old snapshots")
	}
	cancelled := fresh()
	approve(cancelled)
	if n, u := cancelled.swapPending(p.Quote.CreatedAt); n != 1 || u != 0 {
		t.Fatal("approved pending count")
	}
	if e := cancelled.apply(record("onchain-cancel")); e != nil {
		t.Fatal(e)
	}
	if n, u := cancelled.swapPending(p.Quote.CreatedAt); n != 0 || u != 0 || cancelled.swaps[p.Quote.ID].State != "cancelled" {
		t.Fatal("cancelled read")
	}
	signed := fresh()
	approve(signed)
	r := record("onchain-sign")
	r.Original = vector.Signed
	if e := signed.apply(r); e != nil {
		t.Fatal(e)
	}
	if n, u := signed.swapPending(p.Quote.ExpiresAt + 1); n != 1 || u != 1 || len(signed.swapStateFeatures(feature)) != 2 {
		t.Fatal("signed unknown hold/feature lost")
	}
	next := p.Account
	next.Epoch = strings.Repeat("f", 32)
	if next.Epoch == p.Account.Epoch {
		next.Epoch = strings.Repeat("e", 32)
	}
	rebind := journalRecord{Kind: "rebind", At: p.Quote.CreatedAt, Account: &next}
	unsigned := fresh()
	approve(unsigned)
	if e := unsigned.apply(rebind); e != nil {
		t.Fatal("unsigned epoch recovery", e)
	}
	if n, u := unsigned.swapPending(p.Quote.CreatedAt); n != 0 || u != 0 || unsigned.swaps[p.Quote.ID].State != "cancelled" {
		t.Fatal("old unsigned approval survived rebind")
	}
	if e := signed.apply(rebind); e == nil || signed.ref != p.Account {
		t.Fatal("signed hold rebound")
	}
	r = record("onchain-settled")
	r.SwapObservation = &vector.Observation
	if e := signed.apply(r); e != nil {
		t.Fatal(e)
	}
	if n, u := signed.swapPending(p.Quote.CreatedAt); n != 0 || u != 0 || signed.swaps[p.Quote.ID].State != "confirmed" {
		t.Fatal("confirmed receipt read")
	}
	if e := signed.apply(rebind); e != nil {
		t.Fatal("settled retained history rebind", e)
	}
	bad := fresh()
	changed := p
	changed.Quote.Amount = "1"
	changed.Quote.Digest = onchain.QuoteDigest(changed.Quote)
	changed.Digest = SwapProposalDigest(changed)
	r = record("onchain-approve")
	r.Swap = &changed
	if e := bad.apply(r); e == nil {
		t.Fatal("changed intent accepted")
	}
	bad = fresh()
	approve(bad)
	r = record("onchain-sign")
	r.Original = append([]byte(nil), vector.Signed...)
	r.Original[66] ^= 1
	if e := bad.apply(r); e == nil {
		t.Fatal("changed original accepted")
	}
	if os.Getenv("FASED_COMPOSED_SWAP_READER") == "1" {
		for _, file := range []string{"rpc.go", "vault.go"} {
			b, e := os.ReadFile(file)
			if e != nil {
				t.Fatal(e)
			}
			for _, verb := range []string{`"swap-approve"`, `"swap-sign"`, `"swap-reconcile"`, "ConfigureSwapObserver"} {
				if strings.Contains(string(b), verb) {
					t.Fatal("reader composition added signing admission", file, verb)
				}
			}
		}
	}
}
