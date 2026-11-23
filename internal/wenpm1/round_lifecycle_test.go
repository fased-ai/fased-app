package wenpm1

import (
	"crypto/rand"
	"encoding/binary"
	"reflect"
	"testing"
)

func testKey(fill byte) Key {
	var k Key
	for i := range k {
		k[i] = fill
	}
	return k
}

func TestControlledFiveRangeRoundCommitEntryRevealClaim(t *testing.T) {
	t.Parallel()
	b := Binding{
		Program: testKey(0x11),
		Ledger:  testKey(0x22),
		Network: testKey(0x33),
		Economy: testKey(0x44),
		Terms:   testKey(0x55),
	}
	owner := testKey(0xaa)
	mint := testKey(0xbb)
	destATA := testKey(0xcc)
	mintAuth := testKey(0xdd)
	var rec0, rec1, rec2 Key
	rec0, rec1, rec2 = testKey(0x61), testKey(0x62), testKey(0x63)

	const slot uint64 = 1042
	// 1. Open instruction round-trip
	openReq := Open{Slot: slot}
	openIx, err := BuildInstruction(b, openReq, map[string]Key{})
	if err != nil {
		t.Fatalf("Open BuildInstruction: %v", err)
	}
	if got, err := DecodeRequest(openIx.Data, b); err != nil || !reflect.DeepEqual(got, openReq) {
		t.Fatalf("Open DecodeRequest mismatch: got=%+v err=%v", got, err)
	}

	// 2. Commit 5 distinct ranges with CSPRNG salts and nonces
	amounts := [5]uint64{100_000_000, 150_000_000, 400_000_000, 200_000_000, 150_000_000}
	var salts [5]Key
	var nonces [5]uint64
	var commits [5]Key
	entries := make([]Entry, 5)
	for i := uint8(0); i < 5; i++ {
		if _, err := rand.Read(salts[i][:]); err != nil {
			t.Fatal(err)
		}
		var nb [8]byte
		if _, err := rand.Read(nb[:]); err != nil {
			t.Fatal(err)
		}
		nonces[i] = binary.LittleEndian.Uint64(nb[:]) | 1
		c, err := Commitment(b, CommitmentValues{
			Slot:      slot,
			Owner:     owner,
			Amount:    amounts[i],
			Nonce:     nonces[i],
			Selection: i,
			Salt:      salts[i],
		})
		if err != nil {
			t.Fatalf("Commitment range %d: %v", i, err)
		}
		commits[i] = c

		commitReq := Commit{Slot: slot, Amount: amounts[i], Nonce: nonces[i], Commitment: c}
		ix, err := BuildInstruction(b, commitReq, map[string]Key{"owner": owner, "system": SystemProgram()})
		if err != nil {
			t.Fatalf("Commit BuildInstruction %d: %v", i, err)
		}
		if dec, err := DecodeRequest(ix.Data, b); err != nil || !reflect.DeepEqual(dec, commitReq) {
			t.Fatalf("Commit DecodeRequest %d: got=%+v err=%v", i, dec, err)
		}
		entries[i] = Entry{
			Owner:      owner,
			Nonce:      nonces[i],
			Amount:     amounts[i],
			Commitment: c,
		}
	}

	// Verify unique salts / commitments across ranges.
	if commits[0] == commits[1] || salts[0] == salts[1] {
		t.Fatal("range commitments or salts collided")
	}

	// 3. Account state with Open + Committed entries -> ReadAccount verifies wire framing
	ledger := Ledger{
		Binding:           b,
		Epoch:             1,
		Recorders:         [3]Key{rec0, rec1, rec2},
		Mint:              mint,
		EligibleStake:     1_000_000_000,
		LifetimeCommitted: 1_000_000_000,
		Rounds: []Round{{
			Slot:       slot,
			Start:      1_795_400_000,
			Entries:    entries,
			TotalStake: 1_000_000_000,
		}},
	}
	encoded, err := EncodeAccount(ledger)
	if err != nil {
		t.Fatalf("EncodeAccount committed: %v", err)
	}
	readBack, err := ReadAccount(b.Ledger, b.Program, encoded, b)
	if err != nil || len(readBack.Rounds) != 1 || len(readBack.Rounds[0].Entries) != 5 {
		t.Fatalf("ReadAccount committed failed: %v", err)
	}

	// 4. Reveal each range after verifying commitment preimage; wrong salt must fail!
	badSalt := salts[2]
	badSalt[0] ^= 0xff
	badCommit, err := Commitment(b, CommitmentValues{
		Slot:      slot,
		Owner:     owner,
		Amount:    amounts[2],
		Nonce:     nonces[2],
		Selection: 2,
		Salt:      badSalt,
	})
	if err != nil || badCommit == commits[2] {
		t.Fatal("wrong salt failed to invalidate commitment")
	}
	for i := uint8(0); i < 5; i++ {
		revReq := Reveal{Slot: slot, Entry: uint32(i), Selection: i, Salt: salts[i]}
		ix, err := BuildInstruction(b, revReq, map[string]Key{})
		if err != nil {
			t.Fatalf("Reveal BuildInstruction %d: %v", i, err)
		}
		if dec, err := DecodeRequest(ix.Data, b); err != nil || !reflect.DeepEqual(dec, revReq) {
			t.Fatalf("Reveal DecodeRequest %d: got=%+v err=%v", i, dec, err)
		}
		sel := i
		readBack.Rounds[0].Entries[i].Selection = &sel
	}

	// 5. Observe + Settle on winning range 2 (400M stake exact win)
	obsReq := Observe{Slot: slot, Sample: 0, Vote: Vote{Price: 96500, SourceTime: 1_795_400_300, Sequence: 1}}
	obsIx, err := BuildInstruction(b, obsReq, map[string]Key{"recorder": rec0})
	if err != nil {
		t.Fatalf("Observe BuildInstruction: %v", err)
	}
	if _, err := DecodeRequest(obsIx.Data, b); err != nil {
		t.Fatalf("Observe DecodeRequest: %v", err)
	}
	settleReq := Settle{Slot: slot}
	if _, err := BuildInstruction(b, settleReq, map[string]Key{}); err != nil {
		t.Fatalf("Settle BuildInstruction: %v", err)
	}

	winningRange := uint8(2)
	readBack.Rounds[0].Settled = true
	readBack.Rounds[0].Outcome = &winningRange
	readBack.Rounds[0].Allocations = [6]uint64{100_000_000, 150_000_000, 400_000_000, 200_000_000, 150_000_000, 0}
	readBack.Rounds[0].ExactWinningStake = 400_000_000
	readBack.Rounds[0].IncorrectStake = 600_000_000
	readBack.Rounds[0].Entries[2].Exact = true
	readBack.Rounds[0].Entries[2].Principal = 400_000_000
	readBack.Rounds[0].Entries[2].SAT = 1_000_000_000
	readBack.Rounds[0].Entries[2].CreditPPM = 1_000_000

	// 6. ClaimSol + ClaimSat across winning entry & ClaimSol (forfeited/settled) check
	claimSolReq := ClaimSol{Slot: slot, Entry: 2}
	if _, err := BuildInstruction(b, claimSolReq, map[string]Key{"beneficiary": owner}); err != nil {
		t.Fatalf("ClaimSol BuildInstruction: %v", err)
	}
	claimSatReq := ClaimSat{Slot: slot, Entry: 2}
	if _, err := BuildInstruction(b, claimSatReq, map[string]Key{
		"mint":        mint,
		"destination": destATA,
		"authority":   mintAuth,
		"token":       TokenProgram(),
	}); err != nil {
		t.Fatalf("ClaimSat BuildInstruction: %v", err)
	}
	readBack.Rounds[0].Entries[2].SOLClaimed = true
	readBack.Rounds[0].Entries[2].SATClaimed = true

	settledWire, err := EncodeAccount(readBack)
	if err != nil {
		t.Fatalf("EncodeAccount settled: %v", err)
	}
	finalLedger, err := ReadAccount(b.Ledger, b.Program, settledWire, b)
	if err != nil {
		t.Fatalf("ReadAccount settled: %v", err)
	}

	// ProjectReceipt & verify exact SOL/SAT claim flags + conservation
	var totalEntryStake uint64
	for i := uint32(0); i < 5; i++ {
		rec, err := ProjectReceipt(finalLedger, slot, i)
		if err != nil {
			t.Fatalf("ProjectReceipt %d: %v", i, err)
		}
		if rec.Selection == nil || *rec.Selection != uint8(i) || !rec.Settled || rec.Outcome == nil || *rec.Outcome != 2 {
			t.Fatalf("unexpected receipt %d: %+v", i, rec)
		}
		totalEntryStake += rec.Amount
	}
	if totalEntryStake != finalLedger.Rounds[0].TotalStake {
		t.Fatalf("stake conservation failed: sum=%d total=%d", totalEntryStake, finalLedger.Rounds[0].TotalStake)
	}
	winRec, err := ProjectReceipt(finalLedger, slot, 2)
	if err != nil || !winRec.Exact || !winRec.SOLClaimed || !winRec.SATClaimed || winRec.SAT != 1_000_000_000 || winRec.Principal != 400_000_000 {
		t.Fatalf("winning claimed receipt invalid: %+v err=%v", winRec, err)
	}
}
