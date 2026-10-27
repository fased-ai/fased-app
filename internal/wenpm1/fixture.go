package wenpm1

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const FixtureSource = "55a4dc562c2516a48f500047df85cb81fabf8084"
const FixtureSHA256 = "45de61cdfadd383080158fb6786639af26f4c366ae967d0a545643cb036cbdff"

//go:embed testdata/golden.json
var fixtureBytes []byte

// CanonicalFixture returns a fresh decoded copy of the pinned, public golden
// ledger. Its extreme boundary values are codec test data, never user balances.
func CanonicalFixture() (Ledger, Receipt, error) {
	if fmt.Sprintf("%x", sha256.Sum256(fixtureBytes)) != FixtureSHA256 {
		return Ledger{}, Receipt{}, ErrInvalid
	}
	var g struct {
		Binding    Binding
		AccountHex string
		ReceiptHex string
		SchemaHash string
	}
	if err := json.Unmarshal(fixtureBytes, &g); err != nil || g.SchemaHash != SchemaHash {
		return Ledger{}, Receipt{}, ErrInvalid
	}
	a, err := hex.DecodeString(g.AccountHex)
	if err != nil {
		return Ledger{}, Receipt{}, err
	}
	ledger, err := ReadAccount(g.Binding.Ledger, g.Binding.Program, a, g.Binding)
	if err != nil {
		return Ledger{}, Receipt{}, err
	}
	b, err := hex.DecodeString(g.ReceiptHex)
	if err != nil {
		return Ledger{}, Receipt{}, err
	}
	receipt, err := DecodeReceipt(b, g.Binding)
	return ledger, receipt, err
}
