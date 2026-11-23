package onchain_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
)

func deterministicPubkey(tag string) string {
	h := sha256.Sum256([]byte("fased-alt-test:" + tag))
	return onchain.Base58(h[:])
}

func sharedRouteData(id byte, inAmt, outAmt uint64, slippage, feeBps, safetyBps uint16) string {
	data := make([]byte, 40)
	// Anchor discriminator for shared_accounts_route_v2
	copy(data[0:8], []byte{209, 152, 83, 147, 124, 254, 216, 233})
	data[8] = id
	binary.LittleEndian.PutUint64(data[9:17], inAmt)
	binary.LittleEndian.PutUint64(data[17:25], outAmt)
	binary.LittleEndian.PutUint16(data[25:27], slippage)
	binary.LittleEndian.PutUint16(data[27:29], feeBps)
	binary.LittleEndian.PutUint16(data[29:31], safetyBps)
	binary.LittleEndian.PutUint32(data[31:35], 1)
	copy(data[35:40], []byte{0, 16, 39, 0, 1})
	return base64.StdEncoding.EncodeToString(data)
}

func baseSharedQuote(t *testing.T, id byte, feeSentinel bool) onchain.Quote {
	t.Helper()
	key := ed25519.NewKeyFromSeed([]byte(strings.Repeat("a", 32)))
	owner := onchain.Base58(key.Public().(ed25519.PublicKey))
	source, err := onchain.TokenAccount(owner, onchain.USDC)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := onchain.TokenAccount(owner, onchain.SOL)
	if err != nil {
		t.Fatal(err)
	}
	progAuth, err := onchain.JupiterProgramAuthority(id)
	if err != nil {
		t.Fatal(err)
	}
	progSource, err := onchain.TokenAccount(progAuth, onchain.USDC)
	if err != nil {
		t.Fatal(err)
	}
	progDest, err := onchain.TokenAccount(progAuth, onchain.SOL)
	if err != nil {
		t.Fatal(err)
	}
	accts := []onchain.Meta{
		{Pubkey: progAuth, IsSigner: false, IsWritable: false},
		{Pubkey: owner, IsSigner: true, IsWritable: false},
		{Pubkey: source, IsSigner: false, IsWritable: true},
		{Pubkey: progSource, IsSigner: false, IsWritable: true},
		{Pubkey: progDest, IsSigner: false, IsWritable: true},
		{Pubkey: dest, IsSigner: false, IsWritable: true},
		{Pubkey: onchain.USDC, IsSigner: false, IsWritable: false},
		{Pubkey: onchain.SOL, IsSigner: false, IsWritable: false},
		{Pubkey: onchain.Token, IsSigner: false, IsWritable: false},
		{Pubkey: onchain.Token, IsSigner: false, IsWritable: false},
	}
	if feeSentinel {
		accts = append(accts,
			onchain.Meta{Pubkey: onchain.Jupiter, IsSigner: false, IsWritable: false},
			onchain.Meta{Pubkey: onchain.EventAuthority, IsSigner: false, IsWritable: false},
			onchain.Meta{Pubkey: onchain.Jupiter, IsSigner: false, IsWritable: false},
		)
	} else {
		accts = append(accts,
			onchain.Meta{Pubkey: onchain.EventAuthority, IsSigner: false, IsWritable: false},
			onchain.Meta{Pubkey: onchain.Jupiter, IsSigner: false, IsWritable: false},
		)
	}
	const inAmt uint64 = 12_500_000
	const outAmt uint64 = 100_000_000
	const slippage uint16 = 50
	minOut, err := onchain.Minimum(strconv.FormatUint(outAmt, 10), slippage)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	return onchain.Quote{
		Version:         onchain.Version,
		ID:              strings.Repeat("9", 32),
		ProfileID:       "trading-analyst",
		InstanceID:      strings.Repeat("8", 32),
		AccountID:       "wallet",
		Network:         "mainnet-beta",
		Owner:           owner,
		RPCSHA256:       strings.Repeat("7", 64),
		InputMint:       onchain.USDC,
		OutputMint:      onchain.SOL,
		Amount:          strconv.FormatUint(inAmt, 10),
		Output:          strconv.FormatUint(outAmt, 10),
		MinimumOutput:   minOut,
		SlippageBPS:     slippage,
		MaxFeeLamports:  10000,
		FeeLamports:     5000,
		Blockhash:       onchain.ControlledBlockhash,
		LastValidHeight: 100,
		ObservedSlot:    10,
		CreatedAt:       now,
		ExpiresAt:       now + 45,
		Instructions: []onchain.Instruction{{
			ProgramID: onchain.Jupiter,
			Accounts:  accts,
			Data:      sharedRouteData(id, inAmt, outAmt, slippage, 0, 0),
		}},
	}
}

func TestJupiterProgramAuthorityPDA(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id   byte
		want string
	}{
		{0, "GGztQqQ6pCPaJQnNpXBgELr5cs3WwDakRbh1iEMzjgSJ"},
		{5, "CapuXNQoDviLvU1PxFiizLgPNQCxrsag1uMeyk6zLVps"},
	}
	for _, tc := range cases {
		got, err := onchain.JupiterProgramAuthority(tc.id)
		if err != nil || got != tc.want {
			t.Fatalf("JupiterProgramAuthority(%d)=%q err=%v want %q", tc.id, got, err, tc.want)
		}
	}
}

func TestSharedAccountsRouteV2Validation(t *testing.T) {
	t.Parallel()
	for _, feeSentinel := range []bool{false, true} {
		q := baseSharedQuote(t, 5, feeSentinel)
		if err := onchain.ValidateInstructions(q); err != nil {
			t.Fatalf("ValidateInstructions(sentinel=%v): %v", feeSentinel, err)
		}
		if _, err := onchain.Message(q); err != nil {
			t.Fatalf("Message(sentinel=%v): %v", feeSentinel, err)
		}
	}

	mutatedCases := []struct {
		name   string
		mutate func(q *onchain.Quote)
	}{
		{
			name: "wrong-program-authority",
			mutate: func(q *onchain.Quote) {
				q.Instructions[0].Accounts[0].Pubkey = deterministicPubkey("rogue-auth")
			},
		},
		{
			name: "writable-program-authority",
			mutate: func(q *onchain.Quote) {
				q.Instructions[0].Accounts[0].IsWritable = true
			},
		},
		{
			name: "wrong-program-source-ata",
			mutate: func(q *onchain.Quote) {
				q.Instructions[0].Accounts[3].Pubkey = deterministicPubkey("rogue-src")
			},
		},
		{
			name: "wrong-program-dest-ata",
			mutate: func(q *onchain.Quote) {
				q.Instructions[0].Accounts[4].Pubkey = deterministicPubkey("rogue-dst")
			},
		},
		{
			name: "wrong-owner",
			mutate: func(q *onchain.Quote) {
				q.Instructions[0].Accounts[1].Pubkey = deterministicPubkey("rogue-owner")
			},
		},
		{
			name: "nonzero-platform-fee",
			mutate: func(q *onchain.Quote) {
				q.Instructions[0].Data = sharedRouteData(5, 12_500_000, 100_000_000, 50, 10, 0)
			},
		},
		{
			name: "spoofed-event-authority",
			mutate: func(q *onchain.Quote) {
				q.Instructions[0].Accounts[10].Pubkey = deterministicPubkey("rogue-event-auth")
			},
		},
	}

	for _, tc := range mutatedCases {
		t.Run(tc.name, func(t *testing.T) {
			q := baseSharedQuote(t, 0, false)
			tc.mutate(&q)
			if err := onchain.ValidateInstructions(q); err == nil {
				t.Fatalf("accepted tampered shared_accounts_route_v2 (%s)", tc.name)
			}
		})
	}
}

type parsedV0Instruction struct {
	ProgramIDIndex byte
	AccountIndices []byte
	Data           []byte
}

type parsedV0Lookup struct {
	AccountKey      string
	WritableIndices []byte
	ReadonlyIndices []byte
}

type parsedV0Message struct {
	Header         [4]byte
	StaticKeys     []string
	Blockhash      string
	Instructions   []parsedV0Instruction
	AddressLookups []parsedV0Lookup
}

func readShortU16(b []byte, off *int) int {
	val, shift := 0, 0
	for {
		cur := int(b[*off])
		*off++
		val |= (cur & 0x7f) << shift
		if cur&0x80 == 0 {
			return val
		}
		shift += 7
	}
}

func parseV0Message(t *testing.T, raw []byte) parsedV0Message {
	t.Helper()
	var out parsedV0Message
	if len(raw) < 4 || raw[0] != 0x80 {
		t.Fatalf("not a Solana v0 message: len=%d prefix=%x", len(raw), raw[0])
	}
	copy(out.Header[:], raw[:4])
	off := 4
	nStatic := readShortU16(raw, &off)
	out.StaticKeys = make([]string, nStatic)
	for i := 0; i < nStatic; i++ {
		out.StaticKeys[i] = onchain.Base58(raw[off : off+32])
		off += 32
	}
	out.Blockhash = onchain.Base58(raw[off : off+32])
	off += 32
	nIx := readShortU16(raw, &off)
	out.Instructions = make([]parsedV0Instruction, nIx)
	for i := 0; i < nIx; i++ {
		progIdx := raw[off]
		off++
		nAcct := readShortU16(raw, &off)
		accts := append([]byte(nil), raw[off:off+nAcct]...)
		off += nAcct
		nData := readShortU16(raw, &off)
		data := append([]byte(nil), raw[off:off+nData]...)
		off += nData
		out.Instructions[i] = parsedV0Instruction{
			ProgramIDIndex: progIdx,
			AccountIndices: accts,
			Data:           data,
		}
	}
	nLookups := readShortU16(raw, &off)
	out.AddressLookups = make([]parsedV0Lookup, nLookups)
	for i := 0; i < nLookups; i++ {
		tblKey := onchain.Base58(raw[off : off+32])
		off += 32
		nw := readShortU16(raw, &off)
		w := append([]byte(nil), raw[off:off+nw]...)
		off += nw
		nr := readShortU16(raw, &off)
		r := append([]byte(nil), raw[off:off+nr]...)
		off += nr
		out.AddressLookups[i] = parsedV0Lookup{
			AccountKey:      tblKey,
			WritableIndices: w,
			ReadonlyIndices: r,
		}
	}
	if off != len(raw) {
		t.Fatalf("trailing bytes after v0 message parse: off=%d len=%d", off, len(raw))
	}
	return out
}

func TestV0MessageALTSerializationAndStaticPreservation(t *testing.T) {
	t.Parallel()
	// Without ALTs, output bytes remain identical even if empty slice is present.
	qNoALT := baseSharedQuote(t, 5, false)
	msgNil, err := onchain.Message(qNoALT)
	if err != nil {
		t.Fatal(err)
	}
	qUnusedALT := qNoALT
	qUnusedALT.AddressLookupTables = []onchain.AddressLookupTable{{
		AccountKey: deterministicPubkey("unused-table"),
		Addresses:  []string{deterministicPubkey("unrelated-key")},
	}}
	msgUnused, err := onchain.Message(qUnusedALT)
	if err != nil || !bytes.Equal(msgNil, msgUnused) {
		t.Fatalf("unused ALT changed message bytes: err=%v eq=%v", err, bytes.Equal(msgNil, msgUnused))
	}

	// Build a multi-hop route with 52 remaining accounts (>48 inline limit without ALTs).
	qLarge := baseSharedQuote(t, 5, false)
	var dexWKeys, dexRKeys []string
	var tbl1Addrs, tbl2Addrs []string
	// Intentionally include owner, Jupiter, source ATA in ALT to prove pinned static preservation!
	source, _ := onchain.TokenAccount(qLarge.Owner, qLarge.InputMint)
	dest, _ := onchain.TokenAccount(qLarge.Owner, qLarge.OutputMint)
	tbl1Addrs = append(tbl1Addrs, qLarge.Owner, onchain.Jupiter, source, dest)
	for i := 0; i < 42; i++ {
		wKey := deterministicPubkey("hop-writable-" + strconv.Itoa(i))
		dexWKeys = append(dexWKeys, wKey)
		qLarge.Instructions[0].Accounts = append(qLarge.Instructions[0].Accounts, onchain.Meta{
			Pubkey:     wKey,
			IsSigner:   false,
			IsWritable: true,
		})
		if i < 25 {
			tbl1Addrs = append(tbl1Addrs, wKey)
		} else {
			tbl2Addrs = append(tbl2Addrs, wKey)
		}
	}
	for i := 0; i < 10; i++ {
		rKey := deterministicPubkey("hop-readonly-" + strconv.Itoa(i))
		dexRKeys = append(dexRKeys, rKey)
		qLarge.Instructions[0].Accounts = append(qLarge.Instructions[0].Accounts, onchain.Meta{
			Pubkey:     rKey,
			IsSigner:   false,
			IsWritable: false,
		})
		if i < 5 {
			tbl1Addrs = append(tbl1Addrs, rKey)
		} else {
			tbl2Addrs = append(tbl2Addrs, rKey)
		}
	}
	// Fails without ALTs because total unique accounts > 48.
	if _, err := onchain.Message(qLarge); err == nil {
		t.Fatal("expected >48 accounts without ALTs to exceed inline account limit")
	}

	// Attach resolved ALTs -> succeeds cleanly and stays <= 1232 packet bytes.
	tbl1Key := deterministicPubkey("lookup-table-1")
	tbl2Key := deterministicPubkey("lookup-table-2")
	qLarge.AddressLookupTables = []onchain.AddressLookupTable{
		{AccountKey: tbl1Key, Addresses: tbl1Addrs},
		{AccountKey: tbl2Key, Addresses: tbl2Addrs},
	}
	rawMsg, err := onchain.Message(qLarge)
	if err != nil {
		t.Fatalf("ALT-compressed Message failed: %v", err)
	}
	if len(rawMsg)+65 > 1232 {
		t.Fatalf("ALT transaction too large: %d bytes", len(rawMsg)+65)
	}

	parsed := parseV0Message(t, rawMsg)
	if len(parsed.StaticKeys) > 48 {
		t.Fatalf("static keys=%d exceeded 48", len(parsed.StaticKeys))
	}
	// Verify pinned keys remain in StaticKeys.
	staticSet := map[string]bool{}
	for _, k := range parsed.StaticKeys {
		staticSet[k] = true
	}
	for _, wantStatic := range []string{qLarge.Owner, onchain.Compute, onchain.Jupiter, source, dest} {
		if !staticSet[wantStatic] {
			t.Fatalf("mandatory key %s moved into ALT instead of static keys", wantStatic)
		}
	}
	// Reconstruct full loaded account table in exact Solana v0 order:
	// staticKeys + all writable lookup keys + all readonly lookup keys.
	if len(parsed.AddressLookups) != 2 {
		t.Fatalf("expected 2 used lookups, got %d", len(parsed.AddressLookups))
	}
	tblByAddr := map[string][]string{tbl1Key: tbl1Addrs, tbl2Key: tbl2Addrs}
	fullKeys := append([]string{}, parsed.StaticKeys...)
	var altReadonly []string
	for _, lu := range parsed.AddressLookups {
		addrs := tblByAddr[lu.AccountKey]
		for _, wi := range lu.WritableIndices {
			fullKeys = append(fullKeys, addrs[wi])
		}
		for _, ri := range lu.ReadonlyIndices {
			altReadonly = append(altReadonly, addrs[ri])
		}
	}
	altWritableEnd := len(fullKeys)
	fullKeys = append(fullKeys, altReadonly...)

	// Confirm every instruction account index resolves back to exact expected account.
	swapIx := parsed.Instructions[2]
	if fullKeys[swapIx.ProgramIDIndex] != onchain.Jupiter {
		t.Fatalf("swap program resolved to %s", fullKeys[swapIx.ProgramIDIndex])
	}
	for idx, accIdx := range swapIx.AccountIndices {
		want := qLarge.Instructions[0].Accounts[idx].Pubkey
		if got := fullKeys[accIdx]; got != want {
			t.Fatalf("swap account[%d] resolved to %s want %s", idx, got, want)
		}
		if qLarge.Instructions[0].Accounts[idx].IsWritable && int(accIdx) >= altWritableEnd && int(accIdx) >= len(parsed.StaticKeys) {
			t.Fatalf("writable account[%d] placed in readonly ALT range", idx)
		}
	}
	// Confirm unused dexWKeys/dexRKeys moved out of StaticKeys.
	if staticSet[dexWKeys[0]] || staticSet[dexRKeys[0]] {
		t.Fatal("ALT-eligible DEX accounts remained in static keys")
	}
}

type altMockTransport struct {
	quote            onchain.Quote
	tables           map[string]mockALTAccount
	buildLookupAddrs []string
}

type mockALTAccount struct {
	owner      string
	executable bool
	raw        []byte
	missing    bool
}

func (m *altMockTransport) ControlledFixture() bool { return true }

func encodeALTState(accType uint32, deactivationSlot uint64, addrs []string) []byte {
	out := make([]byte, 56+len(addrs)*32)
	binary.LittleEndian.PutUint32(out[0:4], accType)
	binary.LittleEndian.PutUint64(out[4:12], deactivationSlot)
	binary.LittleEndian.PutUint64(out[12:20], 9)
	out[20] = 0
	for i, a := range addrs {
		k, _ := onchain.Key(a)
		copy(out[56+i*32:56+(i+1)*32], k)
	}
	return out
}

func (m *altMockTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "api.jup.ag" && r.URL.Path == "/swap/v2/build" {
		q := m.quote
		res := map[string]any{
			"inputMint":                   q.InputMint,
			"outputMint":                  q.OutputMint,
			"inAmount":                    q.Amount,
			"outAmount":                   q.Output,
			"slippageBps":                 q.SlippageBPS,
			"transactionVersion":          0,
			"setupInstructions":           []onchain.Instruction{},
			"swapInstruction":             q.Instructions[0],
			"cleanupInstruction":          nil,
			"otherInstructions":           []any{},
			"tipInstruction":              nil,
			"addressLookupTableAddresses": m.buildLookupAddrs,
		}
		b, _ := json.Marshal(res)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(b)), Request: r}, nil
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 65536))
	var call struct {
		ID     uint64            `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(raw, &call)
	var result any
	switch call.Method {
	case "getGenesisHash":
		result = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
	case "getLatestBlockhash":
		result = map[string]any{"context": map[string]any{"slot": 10}, "value": map[string]any{"blockhash": onchain.ControlledBlockhash, "lastValidBlockHeight": 100}}
	case "getAccountInfo":
		var addr string
		_ = json.Unmarshal(call.Params[0], &addr)
		if tbl, ok := m.tables[addr]; ok {
			if tbl.missing {
				result = map[string]any{"context": map[string]any{"slot": 10}, "value": nil}
			} else {
				result = map[string]any{
					"context": map[string]any{"slot": 10},
					"value": map[string]any{
						"owner":      tbl.owner,
						"executable": tbl.executable,
						"data":       []string{base64.StdEncoding.EncodeToString(tbl.raw), "base64"},
					},
				}
			}
			break
		}
		// Return token account for q.Owner
		mint := m.quote.InputMint
		dest, _ := onchain.TokenAccount(m.quote.Owner, m.quote.OutputMint)
		if addr == dest {
			mint = m.quote.OutputMint
		}
		mKey, _ := onchain.Key(mint)
		oKey, _ := onchain.Key(m.quote.Owner)
		data := make([]byte, 165)
		copy(data[:32], mKey)
		copy(data[32:64], oKey)
		binary.LittleEndian.PutUint64(data[64:72], 1_000_000_000_000)
		data[108] = 1
		result = map[string]any{
			"context": map[string]any{"slot": 10},
			"value": map[string]any{
				"owner":      onchain.Token,
				"executable": false,
				"data":       []string{base64.StdEncoding.EncodeToString(data), "base64"},
			},
		}
	case "simulateTransaction":
		result = map[string]any{"context": map[string]any{"slot": 10}, "value": map[string]any{"err": nil, "unitsConsumed": 150000}}
	case "getRecentPrioritizationFees":
		result = []any{}
	case "getFeeForMessage":
		result = map[string]any{"context": map[string]any{"slot": 10}, "value": 5000}
	case "getBalance":
		result = map[string]any{"context": map[string]any{"slot": 10}, "value": 10_000_000_000}
	}
	return jsonRPCResponse(r, call.ID, result), nil
}

func TestResolveAddressLookupTableAndPrepare(t *testing.T) {
	t.Parallel()
	activeAddr := deterministicPubkey("alt-active")
	wrongOwnerAddr := deterministicPubkey("alt-wrong-owner")
	execAddr := deterministicPubkey("alt-exec")
	wrongTypeAddr := deterministicPubkey("alt-uninit")
	deactAddr := deterministicPubkey("alt-deactivated")
	badSizeAddr := deterministicPubkey("alt-truncated")
	oversizeAddr := deterministicPubkey("alt-oversize")
	missingAddr := deterministicPubkey("alt-missing")

	entryA := deterministicPubkey("alt-entry-a")
	entryB := deterministicPubkey("alt-entry-b")

	tooMany := make([]string, 257)
	for i := range tooMany {
		tooMany[i] = deterministicPubkey("overflow-" + strconv.Itoa(i))
	}

	q := baseSharedQuote(t, 5, false)
	// Add 1 non-pinned DEX account covered by activeAddr ALT.
	q.Instructions[0].Accounts = append(q.Instructions[0].Accounts, onchain.Meta{
		Pubkey:     entryA,
		IsSigner:   false,
		IsWritable: false,
	})

	rt := &altMockTransport{
		quote:            q,
		buildLookupAddrs: []string{activeAddr},
		tables: map[string]mockALTAccount{
			activeAddr: {
				owner: onchain.AddressLookupTableProgram,
				raw:   encodeALTState(1, math.MaxUint64, []string{entryA, entryB}),
			},
			wrongOwnerAddr: {
				owner: onchain.System,
				raw:   encodeALTState(1, math.MaxUint64, []string{entryA}),
			},
			execAddr: {
				owner:      onchain.AddressLookupTableProgram,
				executable: true,
				raw:        encodeALTState(1, math.MaxUint64, []string{entryA}),
			},
			wrongTypeAddr: {
				owner: onchain.AddressLookupTableProgram,
				raw:   encodeALTState(0, math.MaxUint64, []string{entryA}),
			},
			deactAddr: {
				owner: onchain.AddressLookupTableProgram,
				raw:   encodeALTState(1, 500, []string{entryA}),
			},
			badSizeAddr: {
				owner: onchain.AddressLookupTableProgram,
				raw:   make([]byte, 57),
			},
			oversizeAddr: {
				owner: onchain.AddressLookupTableProgram,
				raw:   encodeALTState(1, math.MaxUint64, tooMany),
			},
			missingAddr: {missing: true},
		},
	}

	client, err := onchain.NewControlledClient(rt)
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := client.ResolveAddressLookupTable(t.Context(), activeAddr)
	if err != nil {
		t.Fatalf("ResolveAddressLookupTable active failed: %v", err)
	}
	wantTbl := onchain.AddressLookupTable{
		AccountKey: activeAddr,
		Addresses:  []string{entryA, entryB},
	}
	if !reflect.DeepEqual(tbl, wantTbl) {
		t.Fatalf("resolved ALT mismatch: got=%+v want=%+v", tbl, wantTbl)
	}

	for _, bad := range []string{wrongOwnerAddr, execAddr, wrongTypeAddr, deactAddr, badSizeAddr, oversizeAddr, missingAddr} {
		if _, err := client.ResolveAddressLookupTable(t.Context(), bad); err == nil {
			t.Fatalf("expected ResolveAddressLookupTable(%s) to fail", bad)
		}
	}

	prepared, err := client.Prepare(t.Context(), q)
	if err != nil {
		t.Fatalf("Prepare with shared_accounts_route_v2 + ALT failed: %v", err)
	}
	if len(prepared.AddressLookupTables) != 1 || !reflect.DeepEqual(prepared.AddressLookupTables[0], wantTbl) {
		t.Fatalf("Prepare did not bind resolved ALTs into quote: %+v", prepared.AddressLookupTables)
	}
	if err := onchain.ValidateQuote(prepared, q.CreatedAt); err != nil {
		t.Fatalf("ValidateQuote with resolved ALT failed: %v", err)
	}
}
