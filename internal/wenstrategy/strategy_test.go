package wenstrategy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"
)

func specimen() Document {
	return Document{Schema: "wen-strategy/1", Name: "Example", Description: "", Market: Market{"fixture-network", "fixture-program", "fixture-economy", "fixture-mint", "fixture-terms", "1"}, Research: Research{"trend", 240, []Condition{}, nil}, Allocation: Allocation{"wide", []int{2000, 2000, 2000, 2000, 2000}, "fixed", []Rule{}}, Schedule: Schedule{"2026-10-22T00:00:00Z", "2026-10-23T00:00:00Z", 1, "0", []Window{}, []string{}, []string{}, 144, 144, 0, nil}, Limits: Limits{"100000000", "1000000000", "500000000", "200000000", "10000", "1000", "100000000"}, Execution: Execution{"review", "browser", nil}}
}
func rawDocument(t *testing.T, d Document) []byte {
	t.Helper()
	b, e := json.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestDecodeOriginalAndClosedObjects(t *testing.T) {
	raw := rawDocument(t, specimen())
	a, e := Decode(raw)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	if a.Digest != hex.EncodeToString(sum[:]) || !bytes.Equal(a.Original, raw) {
		t.Fatal("bytes or digest")
	}
	raw[0] = ' '
	if a.Original[0] != '{' {
		t.Fatal("aliased original")
	}
	pretty, e := json.MarshalIndent(a.Document, "", " ")
	if e != nil {
		t.Fatal(e)
	}
	b, e := Decode(pretty)
	if e != nil || b.Digest == a.Digest {
		t.Fatal("reformat must change version")
	}
	base := string(a.Original)
	bad := map[string]string{
		"negativeZeroCounter": strings.Replace(base, `"cooldownRounds":0`, `"cooldownRounds":-0`, 1),
		"loneHighSurrogate":   strings.Replace(base, `"name":"Example"`, `"name":"\ud800"`, 1),
		"loneLowSurrogate":    strings.Replace(base, `"name":"Example"`, `"name":"\udfff"`, 1),
		"duplicate":           strings.Replace(base, `"name":"Example"`, `"name":"Example","na\u006de":"other"`, 1),
		"nestedDuplicate":     strings.Replace(base, `"thesis":"trend"`, `"thesis":"trend","thesis":"trend"`, 1),
		"unknown":             strings.Replace(base, `"name":"Example"`, `"name":"Example","secret":"x"`, 1),
		"missing":             strings.Replace(base, `"description":"",`, "", 1),
		"nullScalar":          strings.Replace(base, `"name":"Example"`, `"name":null`, 1),
		"nullArray":           strings.Replace(base, `"conditions":[]`, `"conditions":null`, 1),
		"fraction":            strings.Replace(base, `"horizonMinutes":240`, `"horizonMinutes":240.0`, 1),
		"exponent":            strings.Replace(base, `"horizonMinutes":240`, `"horizonMinutes":24e1`, 1),
		"trailing":            base + " {}", "oversize": base + strings.Repeat(" ", MaxDocumentBytes),
		"badUtf8": base + string([]byte{0xff}), "date": strings.Replace(base, "2026-10-22T00:00:00Z", "2026-02-30T00:00:00Z", 1),
	}
	for name, v := range bad {
		t.Run(name, func(t *testing.T) {
			if _, e := Decode([]byte(v)); e == nil {
				t.Fatal("accepted invalid JSON/document")
			}
		})
	}
}
func TestSemanticBounds(t *testing.T) {
	cases := map[string]func(*Document){
		"u64overflow":  func(d *Document) { d.Limits.TotalTurnoverLamports = "18446744073709551616" },
		"leadingzero":  func(d *Document) { d.Limits.PerRoundLamports = "0100000000" },
		"negativezero": func(d *Document) { d.Research.Conditions = []Condition{{"returnBps", 30, "gte", "-0"}} },
		"i128overflow": func(d *Document) {
			d.Research.Conditions = []Condition{{"returnBps", 30, "gte", "170141183460469231731687303715884105728"}}
		},
		"negativevol": func(d *Document) { d.Research.Conditions = []Condition{{"realizedVolBps", 30, "gte", "-1"}} },
		"badpreset":   func(d *Document) { d.Allocation.Preset = "center" },
		"branchdust": func(d *Document) {
			d.Allocation.Mode = "conditional"
			d.Allocation.Rules = []Rule{{[]Condition{{"returnBps", 30, "gte", "1"}}, "center", []int{500, 1500, 6000, 1500, 500}}}
		},
		"conditions33": func(d *Document) {
			for i := 0; i < 33; i++ {
				d.Research.Conditions = append(d.Research.Conditions, Condition{"returnBps", 30, "gte", "1"})
			}
		},
		"rules9": func(d *Document) {
			d.Allocation.Mode = "conditional"
			for i := 0; i < 9; i++ {
				d.Allocation.Rules = append(d.Allocation.Rules, Rule{[]Condition{{"returnBps", 30, "gte", "1"}}, "wide", []int{2000, 2000, 2000, 2000, 2000}})
			}
		},
		"browserauto":       func(d *Document) { d.Execution.Mode = "bounded-auto" },
		"missingmarket":     func(d *Document) { d.Market.AssetMint = "" },
		"duplicateSelected": func(d *Document) { d.Schedule.SelectedRoundIDs = []string{"1", "1"} },
		"longSession":       func(d *Document) { d.Schedule.ExpiresAt = "2026-10-23T00:00:01Z" },
		"feeOrder":          func(d *Document) { d.Limits.MaxUserFeesPerRoundLamports = "10001" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			d := specimen()
			change(&d)
			if _, e := Decode(rawDocument(t, d)); e == nil {
				t.Fatal("invalid accepted")
			}
		})
	}
	d := specimen()
	d.Research.Conditions = []Condition{{"returnBps", 30, "gte", "-170141183460469231731687303715884105728"}}
	if _, e := Decode(rawDocument(t, d)); e != nil {
		t.Fatal(e)
	}
}
func TestAllocationConservationAndTies(t *testing.T) {
	out, e := CompileAllocation("50000003", []int{2000, 2000, 2000, 2000, 2000})
	if e != nil {
		t.Fatal(e)
	}
	if out != [5]string{"10000001", "10000001", "10000001", "10000000", "10000000"} {
		t.Fatal(out)
	}
	for name, p := range presets {
		t.Run(name, func(t *testing.T) {
			out, e := CompileAllocation("18446744073709551615", p[:])
			if e != nil {
				t.Fatal(e)
			}
			sum := new(big.Int)
			for i, s := range out {
				v, _ := new(big.Int).SetString(s, 10)
				sum.Add(sum, v)
				if p[i] == 0 && s != "0" {
					t.Fatal("zero gained units")
				}
			}
			if sum.String() != "18446744073709551615" {
				t.Fatal(sum)
			}
		})
	}
	for _, v := range []string{"0", "1", "49999999", "18446744073709551616", "+1", "01"} {
		if _, e := CompileAllocation(v, []int{2000, 2000, 2000, 2000, 2000}); e == nil {
			t.Fatalf("accepted %s", v)
		}
	}
}
func TestScheduleBoundariesAndRandom(t *testing.T) {
	s := specimen().Schedule
	s.EveryNthRound = 3
	s.AnchorRound = "100"
	s.SkipRoundIDs = []string{"103"}
	s.UTCWindows = []Window{{60, 120}}
	slots := []Slot{{"100", "2026-10-22T01:00:00Z"}, {"103", "2026-10-22T01:10:00Z"}, {"106", "2026-10-22T01:20:00Z"}, {"109", "2026-10-22T02:00:00Z"}, {"112", "2026-10-23T00:00:00Z"}}
	cs, e := CandidateSlots(s, slots)
	if e != nil || len(cs) != 2 || cs[0].RoundID != "100" || cs[1].RoundID != "106" {
		t.Fatal(cs, e)
	}
	s.StartsAt = "2026-10-22T01:00:00Z"
	s.ExpiresAt = "2026-10-22T02:00:00Z"
	inventory := []Slot{{"100", "2026-10-22T01:00:00Z"}, {"101", "2026-10-22T01:10:00Z"}, {"102", "2026-10-22T01:20:00Z"}, {"103", "2026-10-22T01:30:00Z"}, {"104", "2026-10-22T01:40:00Z"}, {"105", "2026-10-22T01:50:00Z"}}
	s.SkipRoundIDs = []string{}
	s.EveryNthRound = 1
	s.RandomSelection = &RandomSelection{strings.Repeat("a", 64), 2}
	p, e := PinRandom(s, inventory)
	if e != nil {
		t.Fatal(e)
	}
	reversed := []Slot{inventory[5], inventory[4], inventory[3], inventory[2], inventory[1], inventory[0]}
	q, e := PinRandom(s, reversed)
	if e != nil || !reflect.DeepEqual(p, q) {
		t.Fatal("order-dependent pin", p, q, e)
	}
	for name, partial := range map[string][]Slot{"opening": inventory[1:], "ending": inventory[:5], "middle": append(append([]Slot{}, inventory[:2]...), inventory[3:]...)} {
		t.Run(name, func(t *testing.T) {
			if _, e := PinRandom(s, partial); e == nil {
				t.Fatal("incomplete inventory accepted")
			}
		})
	}
	s.RandomSelection.Count = 7
	if _, e := PinRandom(s, inventory); e == nil {
		t.Fatal("expanded random count")
	}
	s.RandomSelection = nil
	s.CooldownRounds = 6
	last := "100"
	ok, e := Eligible(s, slots[2], ScheduleState{1, 1, &last}, nil)
	if e != nil || ok {
		t.Fatal("cooldown")
	}
	s.CooldownRounds = 5
	ok, e = Eligible(s, slots[2], ScheduleState{1, 1, &last}, nil)
	if e != nil || !ok {
		t.Fatal("cooldown boundary")
	}
	ok, e = Eligible(s, slots[2], ScheduleState{144, 1, &last}, nil)
	if e != nil || ok {
		t.Fatal("count ceiling")
	}
}
func metric(d Document) Metric {
	return Metric{"returnBps", 30, "25", "wen-research/1", d.Market, "record", strings.Repeat("a", 64), "coinbase", "2026-10-22T01:00:04Z", "2026-10-22T01:00:00Z", "2026-10-22T01:00:00Z", 30, "valid", true}
}
func TestFiltersAndConditionalBranches(t *testing.T) {
	d := specimen()
	d.Allocation.Mode = "conditional"
	d.Allocation.Rules = []Rule{{[]Condition{{"returnBps", 30, "gte", "20"}}, "custom", []int{1000, 1000, 2000, 3000, 3000}}}
	m := metric(d)
	out, e := Evaluate(d, []Metric{m}, "2026-10-22T01:00:05Z")
	if e != nil || !out.Pass || out.Branch != 0 {
		t.Fatal(out, e)
	}
	out, e = Evaluate(d, []Metric{m}, "2026-10-22T01:00:06Z")
	if e != nil || out.Pass || out.Reason != "unavailable-metric" {
		t.Fatal("quote stale", out, e)
	}
	changes := map[string]func(*Metric){"gap": func(m *Metric) { m.CompleteMinutes = 29 }, "disconnected": func(m *Metric) { m.Connected = false }, "method": func(m *Metric) { m.MethodVersion = "other" }, "future": func(m *Metric) { m.QuoteAt = "2026-10-22T01:00:06Z" }, "market": func(m *Metric) { m.MarketBinding.AssetMint = "other" }, "minute": func(m *Metric) { m.LastMinuteEnd = "2026-10-22T00:58:00Z" }, "quality": func(m *Metric) { m.Quality = "gap" }}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			x := m
			change(&x)
			o, e := Evaluate(d, []Metric{x}, "2026-10-22T01:00:05Z")
			if e != nil || o.Pass {
				t.Fatal(o, e)
			}
		})
	}
	m.ValueBps = "19"
	out, e = Evaluate(d, []Metric{m}, "2026-10-22T01:00:05Z")
	if e != nil || !out.Pass || out.Branch != -1 {
		t.Fatal("false valid falls to root", out, e)
	}
	out, e = Evaluate(d, []Metric{m, m}, "2026-10-22T01:00:05Z")
	if e != nil || out.Pass {
		t.Fatal("duplicate metric accepted", out, e)
	}
}
func TestBudgetHoldsAndTurnoverReuse(t *testing.T) {
	l := specimen().Limits
	s := BudgetState{"0", "0", "0", "0", "0", "0", "0", "100001000"}
	r := Reservation{"100000000", "1000", "10000000"}
	if e := CheckLimits(l, s, r); e != nil {
		t.Fatal(e)
	}
	cases := map[string]func(*BudgetState, *Reservation){"total": func(s *BudgetState, _ *Reservation) { s.TotalTurnoverLamports = "900000001" }, "daily": func(s *BudgetState, _ *Reservation) { s.DailyTurnoverLamports = "400000001" }, "unsettled": func(s *BudgetState, _ *Reservation) { s.UnsettledLamports = "100000001" }, "fees": func(s *BudgetState, _ *Reservation) { s.UserFeeHoldsLamports = "9001" }, "perFee": func(_ *BudgetState, r *Reservation) { r.UserFeeLamports = "1001" }, "grossloss": func(s *BudgetState, _ *Reservation) { s.ActualDeductionsLamports = "89999001" }, "balance": func(s *BudgetState, _ *Reservation) { s.AvailableLamports = "100000999" }, "overflow": func(s *BudgetState, _ *Reservation) { s.TotalTurnoverLamports = "18446744073709551615" }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			a, b := s, r
			change(&a, &b)
			if e := CheckLimits(l, a, b); e == nil {
				t.Fatal("budget bypass")
			}
		})
	}
	// Principal returns do not subtract historical committed turnover.
	s.TotalTurnoverLamports = "1000000000"
	if e := CheckLimits(l, s, r); e == nil {
		t.Fatal("turnover reuse bypass")
	}
}

// TestSharedConformance exports a test-only parity artifact, never a runtime CLI.
func TestSharedConformance(t *testing.T) {
	path := os.Getenv("WEN_STRATEGY_CORPUS")
	if path == "" {
		path = "testdata/conformance.json"
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		Schema string            `json:"schema"`
		Cases  []json.RawMessage `json:"cases"`
	}
	if e = json.Unmarshal(raw, &corpus); e != nil {
		t.Fatal(e)
	}
	if corpus.Schema != "wen-strategy-conformance/1" {
		t.Fatal("corpus schema")
	}
	outputs := []map[string]any{}
	for _, data := range corpus.Cases {
		var c struct {
			Name           string          `json:"name"`
			Op             string          `json:"op"`
			JSON           string          `json:"json"`
			BytesHex       string          `json:"bytesHex"`
			Valid          bool            `json:"valid"`
			Amount         string          `json:"amount"`
			Weights        []int           `json:"weights"`
			Expected       json.RawMessage `json:"expected"`
			Schedule       Schedule        `json:"schedule"`
			Slots          []Slot          `json:"slots"`
			Slot           Slot            `json:"slot"`
			State          json.RawMessage `json:"state"`
			PinnedIDs      []string        `json:"pinnedIDs"`
			Document       Document        `json:"document"`
			Records        []Metric        `json:"records"`
			EvaluationTime string          `json:"evaluationTime"`
			Limits         Limits          `json:"limits"`
			Reservation    Reservation     `json:"reservation"`
		}
		if e = json.Unmarshal(data, &c); e != nil {
			t.Fatal(e)
		}
		row := map[string]any{"name": c.Name, "valid": false}
		var result any
		var opErr error
		switch c.Op {
		case "validate":
			input := []byte(c.JSON)
			if c.BytesHex != "" {
				input, e = hex.DecodeString(c.BytesHex)
				if e != nil {
					t.Fatal(e)
				}
			}
			a, e := Decode(input)
			opErr = e
			if e == nil {
				out, e := CompileAllocation(a.Document.Limits.PerRoundLamports, a.Document.Allocation.WeightsBps)
				if e != nil {
					t.Fatal(e)
				}
				row["digest"] = a.Digest
				row["allocations"] = out
			}
		case "allocation":
			out, e := CompileAllocation(c.Amount, c.Weights)
			opErr = e
			if e == nil {
				row["allocations"] = out
				result = out
			}
		case "candidate":
			result, opErr = CandidateSlots(c.Schedule, c.Slots)
		case "random":
			result, opErr = PinRandom(c.Schedule, c.Slots)
		case "eligible":
			var state ScheduleState
			if e = json.Unmarshal(c.State, &state); e != nil {
				t.Fatal(e)
			}
			result, opErr = Eligible(c.Schedule, c.Slot, state, c.PinnedIDs)
		case "evaluate":
			result, opErr = Evaluate(c.Document, c.Records, c.EvaluationTime)
		case "limits":
			var state BudgetState
			if e = json.Unmarshal(c.State, &state); e != nil {
				t.Fatal(e)
			}
			opErr = CheckLimits(c.Limits, state, c.Reservation)
		default:
			t.Fatalf("unknown op %s", c.Op)
		}
		row["valid"] = opErr == nil
		if opErr == nil && c.Op != "validate" && c.Op != "allocation" && c.Op != "limits" {
			row["result"] = result
		}
		if row["valid"] != c.Valid {
			t.Errorf("%s valid=%v expected=%v err=%v", c.Name, row["valid"], c.Valid, opErr)
		}
		if opErr == nil && len(c.Expected) > 0 {
			actual, e := json.Marshal(result)
			if e != nil {
				t.Fatal(e)
			}
			var a, b any
			if e = json.Unmarshal(actual, &a); e != nil {
				t.Fatal(e)
			}
			if e = json.Unmarshal(c.Expected, &b); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(a, b) {
				t.Errorf("%s result=%s expected=%s", c.Name, actual, c.Expected)
			}
		}
		outputs = append(outputs, row)
	}
	if out := os.Getenv("WEN_STRATEGY_OUTPUT"); out != "" {
		raw, e = json.MarshalIndent(outputs, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(out, append(raw, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
	}
}
