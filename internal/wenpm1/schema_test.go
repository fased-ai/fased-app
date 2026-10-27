package wenpm1

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

type schemaField struct {
	name string
	wire json.RawMessage
}

func orderedFields(t *testing.T, raw json.RawMessage) []schemaField {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		t.Fatal("schema object", err)
	}
	var out []schemaField
	for d.More() {
		k, err := d.Token()
		if err != nil {
			t.Fatal(err)
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			t.Fatal(err)
		}
		out = append(out, schemaField{k.(string), value})
	}
	if _, err = d.Token(); err != nil {
		t.Fatal(err)
	}
	return out
}

// This checks all typed layouts, including fields not populated in the golden,
// without a generator, Python, Node, RPC, or sibling checkout at test/runtime.
func TestSchemaLayout(t *testing.T) {
	var s struct {
		Version, Flag, MaxFrame, MaxInstruction int
		FixtureOnly                             bool
		Types                                   map[string]json.RawMessage
		Instructions                            []struct {
			ID       byte
			Name     string
			Fields   json.RawMessage
			Accounts []string
		}
		WellKnownPrograms map[string]string
	}
	b, e := os.ReadFile("testdata/schema.fixture")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &s); e != nil {
		t.Fatal(e)
	}
	if s.Version != Version || s.Flag != 1 || s.FixtureOnly != FixtureOnly || s.MaxFrame != MaxFrame || s.MaxInstruction != MaxInstruction {
		t.Fatal("profile drift")
	}
	if SystemProgram().String() != s.WellKnownPrograms["system"] || TokenProgram().String() != s.WellKnownPrograms["token"] {
		t.Fatal("well-known program drift")
	}
	models := map[string]reflect.Type{"Binding": reflect.TypeFor[Binding](), "Vote": reflect.TypeFor[Vote](), "Entry": reflect.TypeFor[Entry](), "Round": reflect.TypeFor[Round](), "Ledger": reflect.TypeFor[Ledger](), "Receipt": reflect.TypeFor[Receipt]()}
	var shape func(reflect.Type, json.RawMessage, int)
	var fields func(reflect.Type, json.RawMessage)
	fields = func(g reflect.Type, raw json.RawMessage) {
		f := orderedFields(t, raw)
		if g.Kind() != reflect.Struct || g.NumField() != len(f) {
			t.Fatalf("field count %v", g)
		}
		for i, x := range f {
			field := g.Field(i)
			if strings.Split(field.Tag.Get("json"), ",")[0] != x.name {
				t.Fatalf("field order %v.%s versus %s", g, field.Name, x.name)
			}
			shape(field.Type, x.wire, collectionMax(field))
		}
	}
	shape = func(g reflect.Type, raw json.RawMessage, max int) {
		var scalar string
		if json.Unmarshal(raw, &scalar) == nil {
			want := map[string]reflect.Type{"key": keyType, "hash": keyType, "bool": reflect.TypeFor[bool](), "u8": reflect.TypeFor[uint8](), "u32": reflect.TypeFor[uint32](), "u64": reflect.TypeFor[uint64](), "i64": reflect.TypeFor[int64](), "u128": reflect.TypeFor[Uint128]()}[scalar]
			if want != nil {
				if g != want {
					t.Fatalf("scalar %s versus %v", scalar, g)
				}
				return
			}
			if g != models[scalar] {
				t.Fatalf("model type %s versus %v", scalar, g)
			}
			fields(g, s.Types[scalar])
			return
		}
		var spec map[string]json.RawMessage
		if e := json.Unmarshal(raw, &spec); e != nil {
			t.Fatal(e)
		}
		if r, ok := spec["option"]; ok {
			if g.Kind() != reflect.Pointer {
				t.Fatal("option type", g)
			}
			shape(g.Elem(), r, 0)
			return
		}
		if r, ok := spec["array"]; ok {
			var n int
			if e := json.Unmarshal(spec["len"], &n); e != nil {
				t.Fatal(e)
			}
			if g.Kind() != reflect.Array || g.Len() != n {
				t.Fatal("fixed length", g, n)
			}
			shape(g.Elem(), r, 0)
			return
		}
		var n int
		if e := json.Unmarshal(spec["max"], &n); e != nil {
			t.Fatal(e)
		}
		if max != n || g.Kind() != reflect.Slice {
			t.Fatal("collection bound", g, max, n)
		}
		if r, ok := spec["vec"]; ok {
			shape(g.Elem(), r, 0)
			return
		}
		if g != annualType || string(spec["map"]) != `"u64"` || string(spec["value"]) != `"u64"` {
			t.Fatal("annual map layout")
		}
	}
	if len(s.Types) != len(models) || len(s.Instructions) != len(operations) {
		t.Fatal("supported surface drift")
	}
	for name, g := range models {
		t.Log(name)
		fields(g, s.Types[name])
	}
	for i, op := range s.Instructions {
		g := reflect.TypeOf(operations[i].prototype)
		if op.ID != operations[i].prototype.pm1Operation() || op.Name != g.Name() || !reflect.DeepEqual(op.Accounts, operations[i].roles) {
			t.Fatal("instruction/account-order drift", op.Name)
		}
		fields(g, op.Fields)
	}
}

func FuzzDecodeFrames(f *testing.F) {
	b, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		f.Fatal(err)
	}
	var g goldenFile
	if err = json.Unmarshal(b, &g); err != nil {
		f.Fatal(err)
	}
	for _, value := range []string{g.AccountHex, g.ReceiptHex, g.Instructions[0].Hex} {
		data, err := hex.DecodeString(value)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte("SATLOCAL"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if v, e := DecodeRequest(b, g.Binding); e == nil {
			out, e := EncodeRequest(g.Binding, v)
			if e != nil || !bytes.Equal(out, b) {
				t.Fatal("noncanonical request")
			}
		}
		if v, e := DecodeAccount(b, g.Binding); e == nil {
			out, e := EncodeAccount(v)
			if e != nil || !bytes.HasPrefix(b, out) {
				t.Fatal("noncanonical account")
			}
			for _, x := range b[len(out):] {
				if x != 0 {
					t.Fatal("noncanonical padding")
				}
			}
		}
		if v, e := DecodeReceipt(b, g.Binding); e == nil {
			out, e := EncodeReceipt(v)
			if e != nil || !bytes.Equal(out, b) {
				t.Fatal("noncanonical receipt")
			}
		}
	})
}
