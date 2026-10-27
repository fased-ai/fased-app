package wenpm1

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
)

type operation struct {
	prototype Request
	roles     []string
}

var operations = []operation{
	{Open{}, []string{"ledger:w"}},
	{Commit{}, []string{"ledger:w", "owner:sw", "system:"}},
	{Reveal{}, []string{"ledger:w"}},
	{Observe{}, []string{"ledger:w", "recorder:s"}},
	{Settle{}, []string{"ledger:w"}},
	{ClaimSol{}, []string{"ledger:w", "beneficiary:w"}},
	{ClaimSat{}, []string{"ledger:w", "mint:w", "destination:w", "authority:", "token:"}},
}

func operationFor(v Request) (operation, error) {
	for _, op := range operations {
		if reflect.TypeOf(v) == reflect.TypeOf(op.prototype) {
			return op, nil
		}
	}
	return operation{}, fmt.Errorf("%w: unsupported operation", ErrInvalid)
}
func EncodeRequest(expected Binding, v Request) ([]byte, error) {
	if err := expected.validate(); err != nil {
		return nil, err
	}
	op, err := operationFor(v)
	if err != nil {
		return nil, err
	}
	w := writer{}
	w.header("WENPM1IX", 1)
	w.value(reflect.ValueOf(expected), 0)
	w.number(uint64(op.prototype.pm1Operation()), 1)
	w.value(reflect.ValueOf(v), 0)
	if w.err != nil {
		return nil, w.err
	}
	if len(w.data) > MaxInstruction {
		return nil, ErrInvalid
	}
	return w.data, nil
}
func DecodeRequest(data []byte, expected Binding) (Request, error) {
	if err := expected.validate(); err != nil {
		return nil, err
	}
	if len(data) > MaxInstruction {
		return nil, ErrInvalid
	}
	r := reader{data: data}
	r.header("WENPM1IX", 1)
	var b Binding
	r.value(reflect.ValueOf(&b).Elem(), 0)
	if r.err != nil {
		return nil, r.err
	}
	if err := b.match(expected); err != nil {
		return nil, err
	}
	id := r.number(1)
	if r.err != nil || id < 1 || id > uint64(len(operations)) {
		return nil, ErrInvalid
	}
	v := reflect.New(reflect.TypeOf(operations[id-1].prototype))
	r.value(v.Elem(), 0)
	r.done()
	if r.err != nil {
		return nil, r.err
	}
	return v.Elem().Interface().(Request), nil
}

type AccountMeta struct {
	Address    Key  `json:"address"`
	IsSigner   bool `json:"isSigner"`
	IsWritable bool `json:"isWritable"`
}

// InstructionTemplate contains public metadata and bytes only. It is not a
// transaction, grant, simulation result, permission, or executable action.
type InstructionTemplate struct {
	Program Key
	Keys    []AccountMeta
	Data    []byte
}

func SystemProgram() Key { return Key{} }
func TokenProgram() Key {
	return Key{0x06, 0xdd, 0xf6, 0xe1, 0xd7, 0x65, 0xa1, 0x93, 0xd9, 0xcb, 0xe1, 0x46, 0xce, 0xeb, 0x79, 0xac, 0x1c, 0xb4, 0x85, 0xed, 0x5f, 0x5b, 0x37, 0x91, 0x3a, 0x8c, 0xf5, 0x85, 0x7e, 0xff, 0x00, 0xa9}
}
func BuildInstruction(b Binding, v Request, accounts map[string]Key) (InstructionTemplate, error) {
	data, err := EncodeRequest(b, v)
	if err != nil {
		return InstructionTemplate{}, err
	}
	op, _ := operationFor(v)
	if len(accounts) != len(op.roles)-1 {
		return InstructionTemplate{}, ErrInvalid
	}
	keys := make([]AccountMeta, 0, len(op.roles))
	for i, role := range op.roles {
		parts := strings.Split(role, ":")
		k := b.Ledger
		if i > 0 {
			var ok bool
			k, ok = accounts[parts[0]]
			if !ok {
				return InstructionTemplate{}, ErrInvalid
			}
		}
		keys = append(keys, AccountMeta{k, strings.Contains(parts[1], "s"), strings.Contains(parts[1], "w")})
	}
	if v.pm1Operation() == 2 && accounts["system"] != SystemProgram() {
		return InstructionTemplate{}, ErrInvalid
	}
	if v.pm1Operation() == 7 && accounts["token"] != TokenProgram() {
		return InstructionTemplate{}, ErrInvalid
	}
	return InstructionTemplate{b.Program, keys, data}, nil
}
func ReadAccount(address, owner Key, data []byte, expected Binding) (Ledger, error) {
	if address != expected.Ledger || owner != expected.Program {
		return Ledger{}, ErrInvalid
	}
	return DecodeAccount(data, expected)
}
func copyOption(v *uint8) *uint8 {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}
func ProjectReceipt(v Ledger, slot uint64, index uint32) (Receipt, error) {
	if err := v.Binding.validate(); err != nil {
		return Receipt{}, err
	}
	for _, r := range v.Rounds {
		if r.Slot != slot {
			continue
		}
		if uint64(index) >= uint64(len(r.Entries)) {
			break
		}
		e := r.Entries[index]
		return Receipt{Binding: v.Binding, Slot: slot, Entry: index, Owner: e.Owner, Nonce: e.Nonce, Amount: e.Amount, Selection: copyOption(e.Selection), Settled: r.Settled, Outcome: copyOption(r.Outcome), Principal: e.Principal, SAT: e.SAT, MinerInput: e.MinerInput, SOLClaimed: e.SOLClaimed, SATClaimed: e.SATClaimed, CreditPPM: e.CreditPPM, Exact: e.Exact, Forfeiture: e.Forfeiture, WinnerFee: e.WinnerFee}, nil
	}
	return Receipt{}, fmt.Errorf("%w: missing entry", ErrInvalid)
}

type CommitmentValues struct {
	Slot      uint64
	Owner     Key
	Amount    uint64
	Nonce     uint64
	Selection uint8
	Salt      Key
}

func CommitmentPreimage(b Binding, v CommitmentValues) ([]byte, error) {
	if err := b.validate(); err != nil {
		return nil, err
	}
	if v.Selection >= 5 {
		return nil, ErrInvalid
	}
	w := writer{}
	w.put([]byte("wen-pm1-commit-v1"))
	w.put([]byte{Version, 0})
	w.value(reflect.ValueOf(b), 0)
	w.number(v.Slot, 8)
	w.put(v.Owner[:])
	w.put(v.Owner[:]) // v1 owner and beneficiary are the same identity.
	w.number(v.Amount, 8)
	w.number(v.Nonce, 8)
	w.number(uint64(v.Selection), 1)
	w.put(v.Salt[:])
	return w.data, w.err
}
func Commitment(b Binding, v CommitmentValues) (Key, error) {
	p, err := CommitmentPreimage(b, v)
	if err != nil {
		return Key{}, err
	}
	return sha256.Sum256(p), nil
}
