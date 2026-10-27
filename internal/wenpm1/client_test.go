package wenpm1

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

type goldenFile struct {
	Binding      Binding `json:"binding"`
	Instructions []struct {
		Name     string          `json:"name"`
		Args     json.RawMessage `json:"args"`
		Hex      string          `json:"hex"`
		Accounts map[string]Key  `json:"accounts"`
		Keys     []AccountMeta   `json:"keys"`
	} `json:"instructions"`
	AccountHex            string `json:"accountHex"`
	ReceiptHex            string `json:"receiptHex"`
	CommitmentHex         string `json:"commitmentHex"`
	CommitmentPreimageHex string `json:"commitmentPreimageHex"`
	SchemaHash            string `json:"schemaHash"`
}

func golden(t *testing.T) goldenFile {
	t.Helper()
	b, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCanonicalAccountAndReceipt(t *testing.T) {
	g := golden(t)
	ledger, err := DecodeAccount(unhex(t, g.AccountHex), g.Binding)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeAccount(ledger)
	if err != nil || hex.EncodeToString(b) != g.AccountHex {
		t.Fatalf("account parity: %v", err)
	}
	r, err := ProjectReceipt(ledger, 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err = EncodeReceipt(r)
	if err != nil || hex.EncodeToString(b) != g.ReceiptHex {
		t.Fatalf("receipt parity: %v", err)
	}
	decoded, err := DecodeReceipt(b, g.Binding)
	if err != nil || !reflect.DeepEqual(decoded, r) {
		t.Fatalf("receipt decode: %v", err)
	}
	read, err := ReadAccount(g.Binding.Ledger, g.Binding.Program, unhex(t, g.AccountHex), g.Binding)
	if err != nil || !reflect.DeepEqual(read, ledger) {
		t.Fatalf("account read: %v", err)
	}
	if ledger.Rounds[0].CreditWeight != (Uint128{Lo: math.MaxUint64 - 999999, Hi: 999999}) {
		t.Fatal("u128 golden narrowed or word order changed")
	}
	if ledger.Rounds[0].Observations[0][0] == nil || ledger.Rounds[0].Observations[0][0].SourceTime != math.MinInt64 {
		t.Fatal("signed timestamp narrowed")
	}
	if r.Nonce != math.MaxUint64 || r.CreditPPM != 1_000_000 || !r.Exact || r.Forfeiture != 0 || r.WinnerFee != math.MaxUint64/100 {
		t.Fatal("entry fields lost")
	}
	// Projections must not expose a mutable pointer into their input ledger.
	prior := *ledger.Rounds[0].Entries[0].Selection
	*r.Selection = 99
	if *ledger.Rounds[0].Entries[0].Selection != prior {
		t.Fatal("projection aliases ledger")
	}
	if _, err := ProjectReceipt(ledger, 999, 0); err == nil {
		t.Fatal("unknown round")
	}
	if _, err := ProjectReceipt(ledger, 7, math.MaxUint32); err == nil {
		t.Fatal("unknown entry")
	}
}

func TestCanonicalInstructions(t *testing.T) {
	g := golden(t)
	if len(g.Instructions) != 7 {
		t.Fatal("fixture operation coverage")
	}
	for i, x := range g.Instructions {
		t.Run(x.Name, func(t *testing.T) {
			v := reflect.New(reflect.TypeOf(operations[i].prototype))
			d := json.NewDecoder(bytes.NewReader(x.Args))
			d.DisallowUnknownFields()
			if err := d.Decode(v.Interface()); err != nil {
				t.Fatal(err)
			}
			request := v.Elem().Interface().(Request)
			b, err := EncodeRequest(g.Binding, request)
			if err != nil || hex.EncodeToString(b) != x.Hex {
				t.Fatalf("instruction parity: %v", err)
			}
			decoded, err := DecodeRequest(b, g.Binding)
			if err != nil || !reflect.DeepEqual(decoded, request) {
				t.Fatalf("decode parity: %v", err)
			}
			out, err := BuildInstruction(g.Binding, request, x.Accounts)
			if err != nil || out.Program != g.Binding.Program || !reflect.DeepEqual(out.Keys, x.Keys) || !bytes.Equal(out.Data, b) {
				t.Fatalf("metadata parity: %v", err)
			}
		})
	}
}

func TestCanonicalCommitment(t *testing.T) {
	g := golden(t)
	owner, _ := ParseKey(strings.Repeat("0a", 32))
	salt, _ := ParseKey(strings.Repeat("0c", 32))
	v := CommitmentValues{Slot: 7, Owner: owner, Amount: 10_000_001, Nonce: math.MaxUint64, Selection: 2, Salt: salt}
	b, err := CommitmentPreimage(g.Binding, v)
	if err != nil || hex.EncodeToString(b) != g.CommitmentPreimageHex {
		t.Fatalf("preimage parity: %v", err)
	}
	k, err := Commitment(g.Binding, v)
	if err != nil || k.String() != g.CommitmentHex {
		t.Fatalf("hash parity: %v", err)
	}
	for _, selection := range []uint8{5, 255} {
		v.Selection = selection
		if _, err := Commitment(g.Binding, v); err == nil {
			t.Fatal("invalid range")
		}
	}
	v.Selection = 2
	for i := 0; i < 5; i++ {
		b := mutateBinding(g.Binding, i)
		k, err := Commitment(b, v)
		if err != nil || k.String() == g.CommitmentHex {
			t.Fatal("unbound commitment")
		}
	}
}

func mutateBinding(b Binding, i int) Binding {
	v := reflect.ValueOf(&b).Elem().Field(i)
	k := v.Interface().(Key)
	k[0] ^= 0x55
	v.Set(reflect.ValueOf(k))
	return b
}
func TestIdentityRefusal(t *testing.T) {
	g := golden(t)
	a := unhex(t, g.AccountHex)
	r := unhex(t, g.ReceiptHex)
	ix := unhex(t, g.Instructions[0].Hex)
	for i := 0; i < 5; i++ {
		t.Run(reflect.TypeFor[Binding]().Field(i).Name, func(t *testing.T) {
			wrong := mutateBinding(g.Binding, i)
			if _, e := DecodeRequest(ix, wrong); e == nil {
				t.Fatal("instruction mismatch")
			}
			if _, e := DecodeAccount(a, wrong); e == nil {
				t.Fatal("account mismatch")
			}
			if _, e := DecodeReceipt(r, wrong); e == nil {
				t.Fatal("receipt mismatch")
			}
			zero := g.Binding
			reflect.ValueOf(&zero).Elem().Field(i).Set(reflect.Zero(keyType))
			if _, e := EncodeRequest(zero, Open{}); e == nil {
				t.Fatal("empty binding")
			}
			if _, e := DecodeAccount(a, zero); e == nil {
				t.Fatal("missing read identity")
			}
			if _, e := CommitmentPreimage(zero, CommitmentValues{}); e == nil {
				t.Fatal("missing commitment identity")
			}
		})
	}
	if _, e := ReadAccount(Key{}, g.Binding.Program, a, g.Binding); e == nil {
		t.Fatal("wrong address")
	}
	if _, e := ReadAccount(g.Binding.Ledger, Key{}, a, g.Binding); e == nil {
		t.Fatal("wrong owner")
	}
}

func TestMalformedFrames(t *testing.T) {
	g := golden(t)
	cases := []struct {
		name   string
		data   []byte
		decode func([]byte) error
	}{
		{"instruction", unhex(t, g.Instructions[0].Hex), func(b []byte) error { _, e := DecodeRequest(b, g.Binding); return e }},
		{"account", unhex(t, g.AccountHex), func(b []byte) error { _, e := DecodeAccount(b, g.Binding); return e }},
		{"receipt", unhex(t, g.ReceiptHex), func(b []byte) error { _, e := DecodeReceipt(b, g.Binding); return e }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, at := range []int{0, 8, 9, 10, 11} {
				b := bytes.Clone(c.data)
				b[at] ^= 0x55
				if e := c.decode(b); e == nil {
					t.Fatalf("accepted header %d", at)
				}
			}
			for n := 0; n < len(c.data); n++ {
				if e := c.decode(c.data[:n]); e == nil {
					t.Fatalf("accepted truncation %d", n)
				}
			}
			if e := c.decode(bytes.Repeat([]byte{0}, MaxFrame+1)); e == nil {
				t.Fatal("oversized frame")
			}
			if e := c.decode(append(bytes.Clone(c.data), 1)); e == nil {
				t.Fatal("nonzero trailing data")
			}
			if e := c.decode([]byte("SATLOCAL legacy data")); e == nil {
				t.Fatal("legacy data")
			}
		})
	}
	ix := unhex(t, g.Instructions[0].Hex)
	for _, id := range []byte{0, 8, 255} {
		b := bytes.Clone(ix)
		b[172] = id
		if _, e := DecodeRequest(b, g.Binding); e == nil {
			t.Fatal("unknown opcode")
		}
	}
	if _, e := DecodeRequest(append(ix, 0), g.Binding); e == nil {
		t.Fatal("instruction padding")
	}
	if _, e := DecodeRequest(make([]byte, MaxInstruction+1), g.Binding); e == nil {
		t.Fatal("oversized request")
	}
	if _, e := EncodeRequest(g.Binding, nil); e == nil {
		t.Fatal("nil request")
	}
	if _, e := EncodeRequest(g.Binding, (*Open)(nil)); e == nil {
		t.Fatal("nil pointer request")
	}
}

func TestFrameLengthsPaddingTagsAndMapOrder(t *testing.T) {
	g := golden(t)
	a := unhex(t, g.AccountHex)
	r := unhex(t, g.ReceiptHex)
	if _, e := DecodeAccount(append(bytes.Clone(a), make([]byte, 32)...), g.Binding); e != nil {
		t.Fatal("allocated zero padding refused", e)
	}
	if _, e := DecodeReceipt(append(bytes.Clone(r), 0), g.Binding); e == nil {
		t.Fatal("receipt padding")
	}
	for _, raw := range [][]byte{a, r} {
		for _, n := range []uint32{0, math.MaxUint32, uint32(MaxFrame + 1)} {
			b := bytes.Clone(raw)
			binary.LittleEndian.PutUint32(b[12:16], n)
			if raw[11] == 1 {
				if _, e := DecodeAccount(b, g.Binding); e == nil {
					t.Fatal("invalid account length")
				}
			} else {
				if _, e := DecodeReceipt(b, g.Binding); e == nil {
					t.Fatal("invalid receipt length")
				}
			}
		}
	}
	b := append(bytes.Clone(a), 0)
	binary.LittleEndian.PutUint32(b[12:16], binary.LittleEndian.Uint32(a[12:16])+1)
	if _, e := DecodeAccount(b, g.Binding); e == nil {
		t.Fatal("unparsed account payload")
	}
	// These offsets follow the pinned schema and the canonical nonempty fixture.
	for _, at := range []int{236, 238, 239} {
		b := bytes.Clone(r)
		b[at] = 2
		if _, e := DecodeReceipt(b, g.Binding); e == nil {
			t.Fatalf("invalid option/bool tag %d", at)
		}
	}
	const annualCount = 16 + 160 + 8 + 96 + 32 + 8 + 8
	b = bytes.Clone(a)
	binary.LittleEndian.PutUint32(b[annualCount:], 8193)
	if _, e := DecodeAccount(b, g.Binding); e == nil {
		t.Fatal("oversized map")
	}
	b = bytes.Clone(a)
	copy(b[annualCount+4+16:annualCount+4+24], b[annualCount+4:annualCount+12])
	if _, e := DecodeAccount(b, g.Binding); e == nil {
		t.Fatal("duplicate map key")
	}
	l, e := DecodeAccount(a, g.Binding)
	if e != nil {
		t.Fatal(e)
	}
	l.Annual[1].Year = l.Annual[0].Year
	if _, e := EncodeAccount(l); e == nil {
		t.Fatal("encode duplicate map")
	}
	l.Annual = nil
	l.Rounds = make([]Round, 1025)
	if _, e := EncodeAccount(l); e == nil {
		t.Fatal("encode excessive rounds")
	}
	l.Rounds = make([]Round, 1024)
	if _, e := EncodeAccount(l); e == nil {
		t.Fatal("oversized encoded frame")
	}
	l.Rounds = []Round{{Entries: make([]Entry, 1025)}}
	if _, e := EncodeAccount(l); e == nil {
		t.Fatal("encode excessive entries")
	}
	var rounds []Round
	count := make([]byte, 4)
	binary.LittleEndian.PutUint32(count, 1025)
	reader := reader{data: count}
	reader.value(reflect.ValueOf(&rounds).Elem(), 1024)
	if reader.err == nil {
		t.Fatal("decode excessive vector")
	}
}

func TestBuilderRolesAndPrograms(t *testing.T) {
	g := golden(t)
	for i, x := range g.Instructions {
		v := operations[i].prototype
		extra := map[string]Key{}
		for k, value := range x.Accounts {
			extra[k] = value
		}
		extra["unexpected"] = Key{}
		if _, e := BuildInstruction(g.Binding, v, extra); e == nil {
			t.Fatal("extra role", x.Name)
		}
		for k := range x.Accounts {
			missing := map[string]Key{}
			for role, value := range x.Accounts {
				if role != k {
					missing[role] = value
				}
			}
			if _, e := BuildInstruction(g.Binding, v, missing); e == nil {
				t.Fatal("missing role", x.Name, k)
			}
		}
	}
	owner, _ := ParseKey(strings.Repeat("0a", 32))
	if _, e := BuildInstruction(g.Binding, Commit{}, map[string]Key{"owner": owner, "system": owner}); e == nil {
		t.Fatal("wrong System Program")
	}
	accounts := map[string]Key{"mint": owner, "destination": owner, "authority": owner, "token": owner}
	if _, e := BuildInstruction(g.Binding, ClaimSat{}, accounts); e == nil {
		t.Fatal("wrong Token Program")
	}
	accounts["token"] = TokenProgram()
	built, e := BuildInstruction(g.Binding, ClaimSat{}, accounts)
	if e != nil {
		t.Fatal(e)
	}
	accounts["mint"] = Key{}
	if built.Keys[1].Address != owner {
		t.Fatal("metadata aliases caller")
	}
}

func TestExactScalars(t *testing.T) {
	const max = "340282366920938463463374607431768211455"
	for _, s := range []string{"0", "18446744073709551616", max} {
		n, e := ParseUint128(s)
		if e != nil || n.String() != s {
			t.Fatal("u128 range", s, e)
		}
		b, e := json.Marshal(n)
		if e != nil {
			t.Fatal(e)
		}
		var out Uint128
		if e = json.Unmarshal(b, &out); e != nil || out != n {
			t.Fatal("u128 JSON", e)
		}
	}
	for _, s := range []string{"", "01", "-0", "+1", "-1", "1.0", " 1", "1 ", max + "0", "340282366920938463463374607431768211456"} {
		if _, e := ParseUint128(s); e == nil {
			t.Fatal("noncanonical u128", s)
		}
	}
	for _, s := range []string{"1", "null", "1.5", `"01"`} {
		var n Uint128
		if e := json.Unmarshal([]byte(s), &n); e == nil {
			t.Fatal("inexact JSON integer", s)
		}
	}
	for _, s := range []string{"", strings.Repeat("A0", 32), strings.Repeat("00", 31), strings.Repeat("zz", 32)} {
		if _, e := ParseKey(s); e == nil {
			t.Fatal("noncanonical key")
		}
	}
	var k Key
	if e := json.Unmarshal([]byte("null"), &k); e == nil {
		t.Fatal("null key")
	}
}

func TestCanonicalSourcePins(t *testing.T) {
	var source struct {
		Repository, Commit string
		Files              map[string]struct{ Path, SHA256 string }
	}
	b, e := os.ReadFile("testdata/source.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &source); e != nil {
		t.Fatal(e)
	}
	if source.Repository != "https://github.com/satcoin-org/sat" || source.Commit != "55a4dc562c2516a48f500047df85cb81fabf8084" || len(source.Files) != 2 {
		t.Fatal("unreviewed source revision")
	}
	want := map[string]string{"schema.fixture": SchemaHash, "golden.json": "45de61cdfadd383080158fb6786639af26f4c366ae967d0a545643cb036cbdff"}
	for file, hash := range want {
		b, e := os.ReadFile("testdata/" + file)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != hash || source.Files[file].SHA256 != hash {
			t.Fatal("canonical byte drift", file)
		}
	}
	if golden(t).SchemaHash != SchemaHash {
		t.Fatal("golden/schema mismatch")
	}
}

func TestCanonicalFixtureCopy(t *testing.T) {
	ledger, receipt, err := CanonicalFixture()
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Binding != golden(t).Binding || receipt.Amount != math.MaxUint64 {
		t.Fatal("not canonical")
	}
	ledger.Rounds[0].Entries[0].Amount = 1
	again, _, err := CanonicalFixture()
	if err != nil || again.Rounds[0].Entries[0].Amount != math.MaxUint64 {
		t.Fatal("shared fixture mutation", err)
	}
}
