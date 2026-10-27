package wenpm1

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestCanonicalConditionalPreviewParity(t *testing.T) {
	data, err := os.ReadFile("testdata/conditional-preview.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Binding   Binding
		Scenarios []struct {
			Name        string
			Request     ConditionalPreviewRequest
			Response    ConditionalPreviewResponse
			InputSHA256 string
		}
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Scenarios) != 9 {
		t.Fatal("missing canonical cases")
	}
	for _, s := range fixture.Scenarios {
		t.Run(s.Name, func(t *testing.T) {
			before, slot, err := previewRequest(s.Request, fixture.Binding)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := PrepareConditionalPreview(before, slot, 600720)
			if err != nil || prepared != s.Request {
				t.Fatalf("request parity: %v", err)
			}
			result, err := ReadConditionalPreview(prepared, s.Response, fixture.Binding)
			if err != nil {
				t.Fatal(err)
			}
			if result.InputSHA256 != s.InputSHA256 {
				t.Fatal("input digest parity")
			}
			for i, r := range result.Receipts {
				actual, err := EncodeReceipt(r)
				if err != nil || hex.EncodeToString(actual) != s.Response.ReceiptHexes[i] {
					t.Fatalf("receipt parity: %v", err)
				}
				projected, err := ProjectReceipt(result.Ledger, slot, uint32(i))
				if err != nil || !reflect.DeepEqual(r, projected) {
					t.Fatal("projection parity")
				}
			}
			changed := s.Response
			changed.Request.Now = "600721"
			if _, err = ReadConditionalPreview(prepared, changed, fixture.Binding); err == nil {
				t.Fatal("changed request accepted")
			}
			changed = s.Response
			changed.SchemaHash = "wrong"
			if _, err = ReadConditionalPreview(prepared, changed, fixture.Binding); err == nil {
				t.Fatal("changed schema accepted")
			}
			changed = s.Response
			changed.ReceiptHexes = []string{}
			if _, err = ReadConditionalPreview(prepared, changed, fixture.Binding); err == nil {
				t.Fatal("missing receipts accepted")
			}
			if _, err = PrepareConditionalPreview(result.Ledger, slot, 600720); err == nil {
				t.Fatal("settled input accepted")
			}
			encoded, err := json.Marshal(s.Response)
			if err != nil {
				t.Fatal(err)
			}
			var decoded ConditionalPreviewResponse
			if err = json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(append([]byte(`{"unknown":true,`), encoded[1:]...), &decoded); err == nil {
				t.Fatal("unknown field accepted")
			}
			duplicate := bytes.Replace(encoded, []byte(`"schemaHash":`), []byte(`"schemaHash":"duplicate","schemaHash":`), 1)
			if err = json.Unmarshal(duplicate, &decoded); err == nil {
				t.Fatal("duplicate field accepted")
			}
		})
	}
}
