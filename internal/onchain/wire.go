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

func validateAddressLookupTables(tables []AddressLookupTable) error {
	if len(tables) > 4 {
		return errors.New("address lookup table count exceeds bound")
	}
	seen := make(map[string]bool, len(tables))
	for _, tbl := range tables {
		if _, e := Key(tbl.AccountKey); e != nil {
			return e
		}
		if seen[tbl.AccountKey] {
			return errors.New("duplicate address lookup table")
		}
		seen[tbl.AccountKey] = true
		if len(tbl.Addresses) > 256 {
			return errors.New("address lookup table exceeds 256 entries")
		}
		for _, addr := range tbl.Addresses {
			if _, e := Key(addr); e != nil {
				return e
			}
		}
	}
	return nil
}

func ValidateInstructions(q Quote) error {
	if e := validateAddressLookupTables(q.AddressLookupTables); e != nil {
		return e
	}
	maxIxs := 4
	if q.WrapAndUnwrapSol {
		if q.InputMint != SOL && q.OutputMint != SOL {
			return errors.New("wrapAndUnwrapSol requires native SOL input or output")
		}
		maxIxs = 6
	}
	if len(q.Instructions) < 1 || len(q.Instructions) > maxIxs {
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
	wsolATA, e := TokenAccount(q.Owner, SOL)
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
	systemTransfers := 0
	syncNatives := 0
	closes := 0
	for i, ix := range q.Instructions {
		data, e := base64.StdEncoding.Strict().DecodeString(ix.Data)
		if e != nil || len(data) > 1024 || len(ix.Accounts) > 64 {
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
		case System:
			if !q.WrapAndUnwrapSol || q.InputMint != SOL || swaps != 0 || systemTransfers != 0 || len(data) != 12 || binary.LittleEndian.Uint32(data[:4]) != 2 || binary.LittleEndian.Uint64(data[4:12]) != amount || len(ix.Accounts) != 2 {
				return errors.New("unsupported system setup instruction")
			}
			a := ix.Accounts
			if a[0].Pubkey != q.Owner || !a[0].IsSigner || !a[0].IsWritable || a[1].Pubkey != wsolATA || !a[1].IsWritable || a[1].IsSigner {
				return errors.New("native SOL wrap transfer differs from wallet/wSOL ATA")
			}
			systemTransfers++
		case Token:
			if !q.WrapAndUnwrapSol {
				return errors.New("token program instruction requires wrapAndUnwrapSol")
			}
			if swaps == 0 {
				if q.InputMint != SOL || syncNatives != 0 || len(data) != 1 || data[0] != 17 || len(ix.Accounts) != 1 || ix.Accounts[0].Pubkey != wsolATA || !ix.Accounts[0].IsWritable || ix.Accounts[0].IsSigner {
					return errors.New("invalid wSOL sync instruction")
				}
				syncNatives++
			} else {
				if closes != 0 || i != len(q.Instructions)-1 || len(data) != 1 || data[0] != 9 || len(ix.Accounts) != 3 {
					return errors.New("invalid post-swap token close instruction")
				}
				a := ix.Accounts
				if a[0].Pubkey != wsolATA || !a[0].IsWritable || a[0].IsSigner || a[1].Pubkey != q.Owner || !a[1].IsWritable || a[2].Pubkey != q.Owner || !a[2].IsSigner {
					return errors.New("wSOL close recipient or authority differs from wallet")
				}
				closes++
			}
		case Jupiter:
			atEnd := i == len(q.Instructions)-1 || (q.WrapAndUnwrapSol && i == len(q.Instructions)-2)
			if swaps != 0 || !atEnd || len(data) < 38 {
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
				progAuth, e := JupiterProgramAuthority(data[8])
				if e != nil {
					return e
				}
				progSource, e := TokenAccount(progAuth, q.InputMint)
				if e != nil {
					return e
				}
				progDest, e := TokenAccount(progAuth, q.OutputMint)
				if e != nil {
					return e
				}
				if len(a) < 12 ||
					a[0].Pubkey != progAuth || a[0].IsSigner || a[0].IsWritable ||
					a[1].Pubkey != q.Owner || !a[1].IsSigner ||
					a[2].Pubkey != source || !a[2].IsWritable || a[2].IsSigner ||
					a[3].Pubkey != progSource || !a[3].IsWritable || a[3].IsSigner ||
					a[4].Pubkey != progDest || !a[4].IsWritable || a[4].IsSigner ||
					a[5].Pubkey != dest || !a[5].IsWritable || a[5].IsSigner ||
					a[6].Pubkey != q.InputMint || a[6].IsSigner ||
					a[7].Pubkey != q.OutputMint || a[7].IsSigner ||
					a[8].Pubkey != Token || a[8].IsSigner ||
					a[9].Pubkey != Token || a[9].IsSigner {
					return errors.New("Jupiter shared-account binding differs")
				}
				validTail := (a[10].Pubkey == EventAuthority && a[11].Pubkey == Jupiter) ||
					(len(a) >= 13 && a[10].Pubkey == Jupiter && a[11].Pubkey == EventAuthority && a[12].Pubkey == Jupiter)
				if !validTail {
					return errors.New("Jupiter shared-account tail binding differs")
				}
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

// Message constructs a Solana v0 message from validated instructions and
// review-bound AddressLookupTables. We never sign externally serialized messages.
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
	if q.MaxFeeLamports < 5000 || q.MaxFeeLamports > 1_000_000 {
		return nil, errors.New("invalid fee cap")
	}
	cuLimit := uint32(1_400_000)
	if q.ComputeUnits > 0 {
		if q.ComputeUnits < 50_000 || q.ComputeUnits > 1_400_000 {
			return nil, errors.New("compute unit limit outside supported bounds")
		}
		cuLimit = q.ComputeUnits
	}
	maxMicro := (q.MaxFeeLamports - 5000) * 1_000_000 / uint64(cuLimit)
	var microPrice uint64
	if q.ComputeUnits > 0 || q.PriorityFeeMicroLamports > 0 {
		if q.PriorityFeeMicroLamports > maxMicro {
			return nil, errors.New("priority fee exceeds max fee cap")
		}
		microPrice = q.PriorityFeeMicroLamports
	} else {
		microPrice = maxMicro
	}
	cu := make([]byte, 5)
	cu[0] = 2
	binary.LittleEndian.PutUint32(cu[1:], cuLimit)
	price := make([]byte, 9)
	price[0] = 3
	binary.LittleEndian.PutUint64(price[1:], microPrice)
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

	pinned := map[string]bool{q.Owner: true}
	for _, ix := range ixs {
		pinned[ix.ProgramID] = true
	}
	if src, srcErr := TokenAccount(q.Owner, q.InputMint); srcErr == nil {
		pinned[src] = true
	}
	if dst, dstErr := TokenAccount(q.Owner, q.OutputMint); dstErr == nil {
		pinned[dst] = true
	}

	type resolvedLookup struct {
		accountKey   string
		writable     []byte
		writableKeys []string
		readonly     []byte
		readonlyKeys []string
	}
	assigned := map[string]bool{}
	usedLookups := make([]resolvedLookup, 0, len(q.AddressLookupTables))
	for _, tbl := range q.AddressLookupTables {
		entry := resolvedLookup{accountKey: tbl.AccountKey}
		for pos, addr := range tbl.Addresses {
			if _, used := flags[addr]; !used || pinned[addr] || assigned[addr] {
				continue
			}
			assigned[addr] = true
			if flags[addr] {
				entry.writable = append(entry.writable, byte(pos))
				entry.writableKeys = append(entry.writableKeys, addr)
			} else {
				entry.readonly = append(entry.readonly, byte(pos))
				entry.readonlyKeys = append(entry.readonlyKeys, addr)
			}
		}
		if len(entry.writable) > 0 || len(entry.readonly) > 0 {
			usedLookups = append(usedLookups, entry)
		}
	}

	keys := []string{q.Owner}
	rest := []string{}
	for k := range flags {
		if k != q.Owner && !assigned[k] {
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
	nextIdx := len(keys)
	for _, u := range usedLookups {
		for _, k := range u.writableKeys {
			if nextIdx >= 255 {
				return nil, errors.New("route exceeds maximum transaction accounts")
			}
			indexes[k] = byte(nextIdx)
			nextIdx++
		}
	}
	for _, u := range usedLookups {
		for _, k := range u.readonlyKeys {
			if nextIdx >= 255 {
				return nil, errors.New("route exceeds maximum transaction accounts")
			}
			indexes[k] = byte(nextIdx)
			nextIdx++
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
	message = append(message, short(len(usedLookups))...)
	for _, u := range usedLookups {
		tk, _ := Key(u.accountKey)
		message = append(message, tk...)
		message = append(message, short(len(u.writable))...)
		message = append(message, u.writable...)
		message = append(message, short(len(u.readonly))...)
		message = append(message, u.readonly...)
	}
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
