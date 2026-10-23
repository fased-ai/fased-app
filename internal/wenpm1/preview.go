package wenpm1

// Conditional previews consume the pinned local Rust processor's supplied-state
// result. This package does not implement a second financial formula, start a
// process, access RPC, or establish a verified onchain settlement.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
)

type ConditionalPreviewRequest struct {
	SchemaHash string `json:"schemaHash"`
	AccountHex string `json:"accountHex"`
	Slot       string `json:"slot"`
	Now        string `json:"now"`
}
type ConditionalPreviewResponse struct {
	SchemaHash   string                    `json:"schemaHash"`
	Request      ConditionalPreviewRequest `json:"request"`
	AccountHex   string                    `json:"accountHex"`
	ReceiptHexes []string                  `json:"receiptHexes"`
}
type ConditionalPreview struct {
	Profile     string    `json:"profile"`
	SchemaHash  string    `json:"schemaHash"`
	InputSHA256 string    `json:"inputSHA256"`
	Ledger      Ledger    `json:"ledger"`
	Receipts    []Receipt `json:"receipts"`
}

// Closed envelopes reject unknown, missing and duplicated keys before typed
// decoding. The nested request uses this same boundary when decoded in a result.
func closedPreviewJSON(data []byte, fields []string, dst any) error {
	if len(data) > 2*1024*1024 {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		name, ok := token.(string)
		allowed := false
		for _, field := range fields {
			if field == name {
				allowed = true
			}
		}
		if err != nil || !ok || !allowed || seen[name] {
			return ErrInvalid
		}
		seen[name] = true
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return ErrInvalid
		}
	}
	if _, err = d.Token(); err != nil || len(seen) != len(fields) {
		return ErrInvalid
	}
	if _, err = d.Token(); err != io.EOF {
		return ErrInvalid
	}
	return json.Unmarshal(data, dst)
}
func (v *ConditionalPreviewRequest) UnmarshalJSON(data []byte) error {
	type plain ConditionalPreviewRequest
	var out plain
	if err := closedPreviewJSON(data, []string{"schemaHash", "accountHex", "slot", "now"}, &out); err != nil {
		return err
	}
	*v = ConditionalPreviewRequest(out)
	return nil
}
func (v *ConditionalPreviewResponse) UnmarshalJSON(data []byte) error {
	type plain ConditionalPreviewResponse
	var out plain
	if err := closedPreviewJSON(data, []string{"schemaHash", "request", "accountHex", "receiptHexes"}, &out); err != nil {
		return err
	}
	*v = ConditionalPreviewResponse(out)
	return nil
}

func previewHex(v string) ([]byte, error) {
	if len(v) == 0 || len(v) > MaxFrame*2 {
		return nil, ErrInvalid
	}
	b, e := hex.DecodeString(v)
	if e != nil || hex.EncodeToString(b) != v {
		return nil, ErrInvalid
	}
	return b, nil
}
func previewRequest(v ConditionalPreviewRequest, b Binding) (Ledger, uint64, error) {
	if v.SchemaHash != SchemaHash {
		return Ledger{}, 0, ErrInvalid
	}
	slot, e := strconv.ParseUint(v.Slot, 10, 64)
	if e != nil || strconv.FormatUint(slot, 10) != v.Slot {
		return Ledger{}, 0, ErrInvalid
	}
	now, e := strconv.ParseInt(v.Now, 10, 64)
	if e != nil || strconv.FormatInt(now, 10) != v.Now {
		return Ledger{}, 0, ErrInvalid
	}
	data, e := previewHex(v.AccountHex)
	if e != nil {
		return Ledger{}, 0, e
	}
	l, e := DecodeAccount(data, b)
	if e != nil {
		return Ledger{}, 0, e
	}
	canonical, e := EncodeAccount(l)
	if e != nil || hex.EncodeToString(canonical) != v.AccountHex {
		return Ledger{}, 0, ErrInvalid
	}
	for _, r := range l.Rounds {
		if r.Slot == slot && !r.Settled {
			return l, slot, nil
		}
	}
	return Ledger{}, 0, ErrInvalid
}
func PrepareConditionalPreview(l Ledger, slot uint64, now int64) (ConditionalPreviewRequest, error) {
	b, e := EncodeAccount(l)
	if e != nil {
		return ConditionalPreviewRequest{}, e
	}
	v := ConditionalPreviewRequest{SchemaHash, hex.EncodeToString(b), strconv.FormatUint(slot, 10), strconv.FormatInt(now, 10)}
	_, _, e = previewRequest(v, l.Binding)
	return v, e
}
func ReadConditionalPreview(request ConditionalPreviewRequest, response ConditionalPreviewResponse, b Binding) (ConditionalPreview, error) {
	before, slot, e := previewRequest(request, b)
	if e != nil {
		return ConditionalPreview{}, e
	}
	if response.SchemaHash != SchemaHash || response.Request != request {
		return ConditionalPreview{}, ErrInvalid
	}
	data, e := previewHex(response.AccountHex)
	if e != nil {
		return ConditionalPreview{}, e
	}
	after, e := DecodeAccount(data, b)
	if e != nil {
		return ConditionalPreview{}, e
	}
	encoded, e := EncodeAccount(after)
	if e != nil || hex.EncodeToString(encoded) != response.AccountHex {
		return ConditionalPreview{}, ErrInvalid
	}
	if before.Epoch != after.Epoch || before.Mint != after.Mint || before.EligibleStake != after.EligibleStake || before.Recorders != after.Recorders || len(before.Rounds) != len(after.Rounds) {
		return ConditionalPreview{}, ErrInvalid
	}
	var receipts []Receipt
	for i, old := range before.Rounds {
		r := after.Rounds[i]
		if old.Slot != slot {
			if !reflect.DeepEqual(old, r) {
				return ConditionalPreview{}, ErrInvalid
			}
			continue
		}
		if !r.Settled || old.Slot != r.Slot || old.Start != r.Start || !reflect.DeepEqual(old.Observations, r.Observations) || len(old.Entries) != len(r.Entries) || len(response.ReceiptHexes) != len(r.Entries) {
			return ConditionalPreview{}, ErrInvalid
		}
		for j, entry := range r.Entries {
			original := old.Entries[j]
			if entry.Owner != original.Owner || entry.Nonce != original.Nonce || entry.Amount != original.Amount || entry.Commitment != original.Commitment || !reflect.DeepEqual(entry.Selection, original.Selection) || entry.SOLClaimed != original.SOLClaimed || entry.SATClaimed != original.SATClaimed {
				return ConditionalPreview{}, ErrInvalid
			}
			bytes, e := previewHex(response.ReceiptHexes[j])
			if e != nil {
				return ConditionalPreview{}, e
			}
			receipt, e := DecodeReceipt(bytes, b)
			if e != nil {
				return ConditionalPreview{}, e
			}
			projected, e := ProjectReceipt(after, slot, uint32(j))
			if e != nil {
				return ConditionalPreview{}, e
			}
			expected, e := EncodeReceipt(projected)
			if e != nil || hex.EncodeToString(expected) != response.ReceiptHexes[j] {
				return ConditionalPreview{}, ErrInvalid
			}
			receipts = append(receipts, receipt)
		}
	}
	requestBytes, e := json.Marshal(request)
	if e != nil {
		return ConditionalPreview{}, e
	}
	if receipts == nil {
		receipts = []Receipt{}
	}
	return ConditionalPreview{"fixture-only-canonical-conditional-preview-v1", SchemaHash, fmt.Sprintf("%x", sha256.Sum256(requestBytes)), after, receipts}, nil
}
