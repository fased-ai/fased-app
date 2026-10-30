package onchain

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"sort"
)

// RouteV2 puts amount/slippage before the opaque route plan. Never tail-parse
// legacy Route data: its variable-length enum could alias the reviewed amounts.
var routeV2 = [8]byte{187, 100, 250, 204, 49, 196, 175, 20}
var sharedRouteV2 = [8]byte{209, 152, 83, 147, 124, 254, 216, 233}

func ValidateInstructions(q Quote) error {
	if len(q.Instructions) < 1 || len(q.Instructions) > 4 {
		return errors.New("unsupported swap instruction count")
	}
	source, e := TokenAccount(q.Owner, q.InputMint)
	if e != nil {
		return e
	}
	dest, e := TokenAccount(q.Owner, q.OutputMint)
	if e != nil {
		return e
	}
	amount, e := Uint(q.Amount)
	if e != nil {
		return e
	}
	out, e := Uint(q.Output)
	if e != nil {
		return e
	}
	swaps := 0
	setup := map[string]bool{}
	for i, ix := range q.Instructions {
		data, e := base64.StdEncoding.Strict().DecodeString(ix.Data)
		if e != nil || len(data) > 1024 || len(ix.Accounts) > 48 {
			return errors.New("invalid swap instruction")
		}
		for _, a := range ix.Accounts {
			if _, e := Key(a.Pubkey); e != nil {
				return e
			}
			if a.IsSigner && a.Pubkey != q.Owner {
				return errors.New("additional swap signer denied")
			}
		}
		switch ix.ProgramID {
		case AssociatedToken:
			if swaps != 0 || len(data) != 1 || data[0] != 1 || len(ix.Accounts) != 6 {
				return errors.New("only pre-swap idempotent token setup is supported")
			}
			a := ix.Accounts
			mint := a[3].Pubkey
			ata, e := TokenAccount(q.Owner, mint)
			if e != nil || (mint != q.InputMint && mint != q.OutputMint) || a[0].Pubkey != q.Owner || !a[0].IsSigner || !a[0].IsWritable || a[1].Pubkey != ata || !a[1].IsWritable || a[2].Pubkey != q.Owner || a[4].Pubkey != System || a[5].Pubkey != Token || setup[ata] {
				return errors.New("token setup differs from wallet/mints")
			}
			setup[ata] = true
		case Jupiter:
			if swaps != 0 || i != len(q.Instructions)-1 || len(data) < 38 {
				return errors.New("one final Jupiter RouteV2 required")
			}
			swaps++
			offset := 8
			shared := bytes.Equal(data[:8], sharedRouteV2[:])
			if shared {
				offset++
			} else if !bytes.Equal(data[:8], routeV2[:]) {
				return errors.New("unsupported Jupiter route variant")
			}
			if len(data) < offset+26 || binary.LittleEndian.Uint64(data[offset:]) != amount || binary.LittleEndian.Uint64(data[offset+8:]) != out || binary.LittleEndian.Uint16(data[offset+16:]) != q.SlippageBPS || binary.LittleEndian.Uint16(data[offset+18:]) != 0 || binary.LittleEndian.Uint16(data[offset+20:]) != 0 {
				return errors.New("Jupiter amount, slippage or fees differ from review")
			}
			steps := binary.LittleEndian.Uint32(data[offset+22:])
			if steps < 1 || steps > 16 || len(data) < offset+26+int(steps)*5 {
				return errors.New("invalid Jupiter route plan")
			}
			a := ix.Accounts
			if !shared {
				if len(a) < 10 || a[0].Pubkey != q.Owner || !a[0].IsSigner || a[1].Pubkey != source || !a[1].IsWritable || a[2].Pubkey != dest || !a[2].IsWritable || a[3].Pubkey != q.InputMint || a[4].Pubkey != q.OutputMint || a[5].Pubkey != Token || a[6].Pubkey != Token || a[7].Pubkey != Jupiter || a[8].Pubkey != EventAuthority || a[9].Pubkey != Jupiter {
					return errors.New("Jupiter token/wallet account binding differs")
				}
			} else {
				return errors.New("shared-account routes require their own accepted policy")
			}
		default:
			return errors.New("unapproved swap instruction program")
		}
	}
	if swaps != 1 {
		return errors.New("Jupiter swap instruction missing")
	}
	return nil
}

func short(n int) []byte {
	out := []byte{}
	for {
		b := byte(n & 127)
		n >>= 7
		if n != 0 {
			b |= 128
		}
		out = append(out, b)
		if n == 0 {
			return out
		}
	}
}

// Message constructs v0 with inline keys and no lookup tables. We never sign
// externally serialized messages. Oversized routes fail instead of resolving
// mutable unreviewed lookup tables or silently using another route/version.
func Message(q Quote) ([]byte, error) {
	if e := ValidateInstructions(q); e != nil {
		return nil, e
	}
	owner, e := Key(q.Owner)
	if e != nil {
		return nil, e
	}
	block, e := Key(q.Blockhash)
	if e != nil {
		return nil, e
	}
	cu := make([]byte, 5)
	cu[0] = 2
	binary.LittleEndian.PutUint32(cu[1:], 1_400_000)
	price := make([]byte, 9)
	price[0] = 3
	if q.MaxFeeLamports < 5000 || q.MaxFeeLamports > 1_000_000 {
		return nil, errors.New("invalid fee cap")
	}
	binary.LittleEndian.PutUint64(price[1:], (q.MaxFeeLamports-5000)*1_000_000/1_400_000)
	ixs := append([]Instruction{{Compute, nil, base64.StdEncoding.EncodeToString(cu)}, {Compute, nil, base64.StdEncoding.EncodeToString(price)}}, q.Instructions...)
	flags := map[string]bool{q.Owner: true}
	for _, ix := range ixs {
		if _, e := Key(ix.ProgramID); e != nil {
			return nil, e
		}
		if _, ok := flags[ix.ProgramID]; !ok {
			flags[ix.ProgramID] = false
		}
		for _, a := range ix.Accounts {
			flags[a.Pubkey] = flags[a.Pubkey] || a.IsWritable
		}
	}
	keys := []string{q.Owner}
	rest := []string{}
	for k := range flags {
		if k != q.Owner {
			rest = append(rest, k)
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		if flags[rest[i]] != flags[rest[j]] {
			return flags[rest[i]]
		}
		return rest[i] < rest[j]
	})
	keys = append(keys, rest...)
	if len(keys) > 48 {
		return nil, errors.New("route exceeds supported inline account limit")
	}
	readonly := 0
	indexes := map[string]byte{}
	for i, k := range keys {
		indexes[k] = byte(i)
		if !flags[k] {
			readonly++
		}
	}
	message := []byte{0x80, 1, 0, byte(readonly)}
	message = append(message, short(len(keys))...)
	message = append(message, owner...)
	for _, k := range keys[1:] {
		b, _ := Key(k)
		message = append(message, b...)
	}
	message = append(message, block...)
	message = append(message, short(len(ixs))...)
	for _, ix := range ixs {
		message = append(message, indexes[ix.ProgramID])
		message = append(message, short(len(ix.Accounts))...)
		for _, a := range ix.Accounts {
			message = append(message, indexes[a.Pubkey])
		}
		data, _ := base64.StdEncoding.DecodeString(ix.Data)
		message = append(message, short(len(data))...)
		message = append(message, data...)
	}
	message = append(message, 0)
	if len(message)+65 > 1232 {
		return nil, errors.New("route exceeds supported transaction size")
	}
	return message, nil
}
func Unsigned(q Quote) ([]byte, error) {
	m, e := Message(q)
	if e != nil {
		return nil, e
	}
	return append(append([]byte{1}, make([]byte, 64)...), m...), nil
}
func Signed(q Quote, signature []byte) ([]byte, error) {
	m, e := Message(q)
	if e != nil {
		return nil, e
	}
	owner, e := Key(q.Owner)
	if e != nil || len(signature) != 64 || !ed25519.Verify(owner, m, signature) {
		return nil, errors.New("swap signature verification failed")
	}
	return append(append([]byte{1}, signature...), m...), nil
}
