package wenstrategy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/wenpm1"
)

func TestCompiledStrategyCanonicalPreview(t *testing.T) {
	raw, e := os.ReadFile("testdata/preview-strategy.json")
	if e != nil {
		t.Fatal(e)
	}
	d, e := Parse(raw)
	if e != nil {
		t.Fatal(e)
	}
	amounts, e := CompileAllocation(d.Limits.PerRoundLamports, d.Allocation.WeightsBps)
	if e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile("../wenpm1/testdata/conditional-preview.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Binding   wenpm1.Binding
		Scenarios []struct {
			Name     string
			Request  wenpm1.ConditionalPreviewRequest
			Response wenpm1.ConditionalPreviewResponse
		}
	}
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, s := range fixture.Scenarios {
		if s.Name != "all-five" {
			continue
		}
		found = true
		bytes, e := hex.DecodeString(s.Request.AccountHex)
		if e != nil {
			t.Fatal(e)
		}
		ledger, e := wenpm1.DecodeAccount(bytes, fixture.Binding)
		if e != nil {
			t.Fatal(e)
		}
		slot, e := strconv.ParseUint(s.Request.Slot, 10, 64)
		if e != nil {
			t.Fatal(e)
		}
		now, e := strconv.ParseInt(s.Request.Now, 10, 64)
		if e != nil {
			t.Fatal(e)
		}
		matched := 0
		for _, r := range ledger.Rounds {
			if r.Slot != slot {
				continue
			}
			if len(r.Entries) != 5 {
				t.Fatal("five entries required")
			}
			for _, entry := range r.Entries {
				if entry.Selection == nil || int(*entry.Selection) >= 5 {
					t.Fatal("range missing")
				}
				if strconv.FormatUint(entry.Amount, 10) != amounts[*entry.Selection] {
					t.Fatal("allocation mismatch")
				}
				matched++
			}
		}
		if matched != 5 {
			t.Fatal("round missing")
		}
		request, e := wenpm1.PrepareConditionalPreview(ledger, slot, now)
		if e != nil || request != s.Request {
			t.Fatal("request mismatch", e)
		}
		preview, e := wenpm1.ReadConditionalPreview(request, s.Response, fixture.Binding)
		if e != nil {
			t.Fatal(e)
		}
		if len(preview.Receipts) != 5 {
			t.Fatal("receipt count")
		}
		for i, r := range preview.Receipts {
			b, e := wenpm1.EncodeReceipt(r)
			if e != nil || hex.EncodeToString(b) != s.Response.ReceiptHexes[i] {
				t.Fatal("receipt mismatch", e)
			}
		}
	}
	if !found {
		t.Fatal("all-five scenario missing")
	}
}
func TestRecordJSONShapes(t *testing.T) {
	record := FinancialHistory{MarketBinding: specimen().Market, TransactionID: "tx", InstructionIndex: "0", EventIndex: "1", Finality: "finalized", EvidenceClass: "paper"}
	expected := `["fixture-network","fixture-program","tx","0","1"]`
	if HistoryIdentity(record) != expected {
		t.Fatal(HistoryIdentity(record))
	}
	values := []any{Observation{}, ResearchSnapshot{}, PreviewEnvelope{}, StrategyProposal{}, SessionRecord{}, DecisionRecord{}, record, ValuationOverlay{}, APIEnvelope[FinancialHistory]{Data: record}, APIError{}}
	for _, v := range values {
		b, e := json.Marshal(v)
		if e != nil || !json.Valid(b) {
			t.Fatal(e)
		}
		if strings.Contains(string(b), `"RecordID"`) {
			t.Fatal("Go field leaked")
		}
	}
	b, e := json.Marshal(Availability[string]{Available: false, Reason: "unavailable"})
	if e != nil || string(b) != `{"available":false,"value":null,"reason":"unavailable"}` {
		t.Fatal(string(b), e)
	}
}

func TestCopiedFixtureProvenance(t *testing.T) {
	raw, e := os.ReadFile("testdata/source.json")
	if e != nil {
		t.Fatal(e)
	}
	var manifest struct {
		Files []struct {
			File   string `json:"file"`
			SHA256 string `json:"sha256"`
		}
	}
	if e = json.Unmarshal(raw, &manifest); e != nil {
		t.Fatal(e)
	}
	if len(manifest.Files) != 2 {
		t.Fatal("fixture manifest coverage")
	}
	for _, f := range manifest.Files {
		if f.File != "conformance.json" && f.File != "preview-strategy.json" {
			t.Fatal("unexpected fixture")
		}
		raw, e = os.ReadFile("testdata/" + f.File)
		if e != nil {
			t.Fatal(e)
		}
		digest := sha256.Sum256(raw)
		if hex.EncodeToString(digest[:]) != f.SHA256 {
			t.Fatal("fixture drift", f.File)
		}
	}
}
