package core

import (
	"encoding/hex"
	"os"
	"testing"

	"golang.org/x/crypto/argon2"
)

func init() {
	if testing.Testing() && os.Getenv("FASED_FAST_TEST_KDF") == "1" {
		kdfTime, kdfMemory, kdfThreads = 1, 1024, 1
	}
}

func TestProductionKDFParameters(t *testing.T) {
	t.Parallel()
	if prodKDFTime != 3 || prodKDFMemory != 64*1024 || prodKDFThreads != 2 {
		t.Fatalf("unexpected production KDF parameters: t=%d m=%d p=%d", prodKDFTime, prodKDFMemory, prodKDFThreads)
	}
	if os.Getenv("FASED_FAST_TEST_KDF") != "1" {
		if kdfTime != prodKDFTime || kdfMemory != prodKDFMemory || kdfThreads != prodKDFThreads {
			t.Fatalf("production KDF parameters overridden without opt-in env: t=%d m=%d p=%d", kdfTime, kdfMemory, kdfThreads)
		}
	}
	got := argon2.IDKey([]byte("fased-production-kdf-golden"), []byte("0123456789abcdef"), prodKDFTime, prodKDFMemory, prodKDFThreads, 32)
	const want = "248fa69ff535752dee3981879eda4d57446bf602ad00664a286515d2b141a3e9"
	if hex.EncodeToString(got) != want {
		t.Fatalf("argon2 golden vector mismatch: got %x, want %s", got, want)
	}
}
