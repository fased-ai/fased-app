package onchain

import "testing"

func FuzzSolanaMessageAndWithdrawalValidation(f *testing.F) {
	var feePayer [32]byte
	feePayer[0] = 1
	var bh [32]byte
	bh[0] = 2
	if msg, err := BuildDevnetComputeMemoMessage(feePayer, bh, 200_000, 1000, "fased-fuzz"); err == nil {
		f.Add(msg)
	}
	f.Add([]byte{0x80, 1, 0, 1, 0})
	f.Add([]byte{0x01, 0x00, 0x01, 0x01})

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 8192 {
			return
		}
		_, _, _ = decodeShort(raw)
		_ = ValidateDevnetMessage(raw, feePayer)
		_ = ValidateSolanaMainnetMessage(raw, feePayer)
		_, _ = ValidateWithdrawalMessage(raw, feePayer)
		if len(raw) <= 64 {
			_, _ = Key(string(raw))
			_, _ = Uint(string(raw))
		}
	})
}
