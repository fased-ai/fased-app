package onchain

import (
	"encoding/binary"
	"math/big"
	"testing"
)

func TestLV1XAccountCodecsAndBootstrapVesting(t *testing.T) {
	t.Parallel()

	var k1, k2, k3 [32]byte
	k1[0], k2[0], k3[0] = 1, 2, 3

	// 1. StakingState round-trip (including optional streaming tail).
	var stBody []byte
	stBody = append(stBody, k1[:]...)
	stBody = append(stBody, u64LEBytes(500_000_000_000)...)
	stBody = append(stBody, u64LEBytes(25_000_000_000)...)
	acc := make([]byte, 16)
	binary.LittleEndian.PutUint64(acc[:8], 42)
	binary.LittleEndian.PutUint64(acc[8:], 1)
	stBody = append(stBody, acc...)
	stBody = append(stBody, u64LEBytes(1_000_000)...)
	stBody = append(stBody, u64LEBytes(900_000)...)
	stBody = append(stBody, u64LEBytes(120_000)...)
	stBody = append(stBody, i64LEBytes(1750000000)...)
	stBody = append(stBody, i64LEBytes(1750086400)...)
	stFrame, err := writeLV1XFrame(LV1XMagicStakingState, stBody, LV1XSpaceStakingState)
	if err != nil {
		t.Fatalf("write StakingState: %v", err)
	}
	stDec, err := DecodeLV1XStakingState(stFrame)
	if err != nil {
		t.Fatalf("DecodeLV1XStakingState: %v", err)
	}
	wantAcc := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(42))
	if stDec.TotalActiveStake != 500_000_000_000 || stDec.StreamRemainingBtc != 120_000 || stDec.AccBtcPerSatE18.Cmp(wantAcc) != 0 {
		t.Fatalf("unexpected StakingState: %+v", stDec)
	}

	// Non-canonical zero padding MUST fail.
	badPad := append([]byte(nil), stFrame...)
	badPad[len(badPad)-1] = 1
	if _, err := DecodeLV1XStakingState(badPad); err == nil {
		t.Fatal("expected noncanonical zero padding failure")
	}

	// 2. StakePosition decode.
	var posBody []byte
	posBody = append(posBody, k1[:]...)
	posBody = append(posBody, k2[:]...)
	posBody = append(posBody, u64LEBytes(20_000_000_000)...)
	posBody = append(posBody, u64LEBytes(10_000_000_000)...)
	posBody = append(posBody, i64LEBytes(1750001000)...)
	posBody = append(posBody, make([]byte, 16)...)
	posBody = append(posBody, u64LEBytes(55_000)...)
	posFrame, _ := writeLV1XFrame(LV1XMagicStakePosition, posBody, LV1XSpaceStakePosition)
	posDec, err := DecodeLV1XStakePosition(posFrame)
	if err != nil || posDec.UnclaimedBtc != 55_000 || posDec.ActiveStake != 20_000_000_000 {
		t.Fatalf("DecodeLV1XStakePosition: %+v err=%v", posDec, err)
	}

	// 3. TreasuryState & SessionState decode.
	var trBody []byte
	trBody = append(trBody, k1[:]...)
	trBody = append(trBody, u64LEBytes(1_000)...)
	trBody = append(trBody, u64LEBytes(2_000)...)
	trBody = append(trBody, u64LEBytes(3_000_000)...)
	trBody = append(trBody, u64LEBytes(4_000)...)
	trFrame, _ := writeLV1XFrame(LV1XMagicTreasuryState, trBody, LV1XSpaceTreasuryState)
	trDec, err := DecodeLV1XTreasuryState(trFrame)
	if err != nil || trDec.TotalBtcBought != 3_000_000 {
		t.Fatalf("DecodeLV1XTreasuryState: %+v err=%v", trDec, err)
	}

	var seBody []byte
	seBody = append(seBody, k1[:]...)
	seBody = append(seBody, k2[:]...)
	seBody = append(seBody, u64LEBytes(77)...)
	seBody = append(seBody, k3[:]...)
	seBody = append(seBody, u64LEBytes(10)...)
	seBody = append(seBody, u64LEBytes(20)...)
	seBody = append(seBody, i64LEBytes(1800000000)...)
	seBody = append(seBody, u64LEBytes(50_000_000)...)
	seBody = append(seBody, u64LEBytes(500_000_000)...)
	seBody = append(seBody, u64LEBytes(250_000_000)...)
	seBody = append(seBody, u64LEBytes(100_000_000)...)
	seBody = append(seBody, u64LEBytes(0)...)
	seBody = append(seBody, 1, 0)
	seFrame, _ := writeLV1XFrame(LV1XMagicSessionState, seBody, LV1XSpaceSessionState)
	seDec, err := DecodeLV1XSessionState(seFrame)
	if err != nil || seDec.Reference != 77 || !seDec.ReturnToBudget || seDec.Stopped {
		t.Fatalf("DecodeLV1XSessionState: %+v err=%v", seDec, err)
	}

	// 4. Bootstrap vesting curve: TGE 25% + linear remainder over vestingDuration.
	if got := LV1XVestedBootstrapSat(10_000, 0, 1000, 2000, 1000, 2500); got != 0 {
		t.Fatalf("phase 0 vesting expected 0, got %d", got)
	}
	if got := LV1XVestedBootstrapSat(10_000, 1, 1000, 1500, 1000, 2500); got != 6250 {
		t.Fatalf("50%% elapsed vesting expected 6250, got %d", got)
	}
	if got := LV1XVestedBootstrapSat(10_000, 3, 1000, 1000, 1000, 2500); got != 10_000 {
		t.Fatalf("draining phase vesting expected 10000, got %d", got)
	}
}
