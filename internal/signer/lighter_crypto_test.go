package signer

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"testing"
)

func limbs5ToLE40Big(limbs [5]uint64) *big.Int {
	var be [40]byte
	for i := 0; i < 5; i++ {
		binary.BigEndian.PutUint64(be[i*8:], limbs[4-i])
	}
	return new(big.Int).SetBytes(be[:])
}

func limbs5ToLE40Bytes(limbs [5]uint64) [40]byte {
	var out [40]byte
	for i := 0; i < 5; i++ {
		binary.LittleEndian.PutUint64(out[i*8:], limbs[i])
	}
	return out
}

func TestLighterComparativeSchnorrVectors(t *testing.T) {
	t.Parallel()
	// Official comparative vectors from github.com/elliottech/poseidon_crypto/signature/schnorr/schnorr_test.go
	sks := [][5]uint64{
		{12235002942052073545, 1175977464658719998, 8536934969147463310, 6524687619313720391, 2922072024880609112},
		{14609471659974493146, 15558617123161593410, 853367204868339037, 17594253198278631904, 368396584122947478},
		{846395111423676945, 1354180063821346280, 5751371120309175011, 4898038106472090654, 1076345918732914302},
	}
	hashedMessages := [][5]uint64{
		{8398652514106806347, 11069112711939986896, 9732488227085561369, 18076754337204438535, 17155407358725346236},
		{14569490467507212064, 2707063505563578676, 7506743487465742335, 12569771346154554175, 4305083698940175790},
		{17529153479246803593, 1743712677205511695, 4834285972617397460, 5486672566342530358, 7254989001695704129},
	}
	ks := [][5]uint64{
		{5245666847777449560, 15178169970799106939, 4403065012435293749, 15306540389399388999, 8935555081913173844},
		{1980123857560067020, 10696795398834097509, 3211831869376171671, 6194822139276031840, 3482023782412490864},
		{10299597990997564957, 8547298489021408803, 12250978550108858722, 5282281975236198197, 5328603554431393061},
	}
	expectedSs := [][5]uint64{
		{6950590877883398434, 17178336263794770543, 11012823478139181320, 16445091359523510936, 5882925226143600273},
		{15189311883262425203, 16924634885527914505, 11098200095411565797, 11441434601417451505, 2245797172600273048},
		{1747989245728027396, 18083435619737379521, 18276259610811995786, 15101757397705334408, 5007814817019340642},
	}
	expectedEs := [][5]uint64{
		{4544744459434870309, 4180764085957612004, 3024669018778978615, 15433417688859446606, 6775027260348937828},
		{4905460437060282008, 9275377852059362729, 10383772785796962929, 6858067464918579610, 7078247668913970626},
		{4911725746357568132, 12205663641120664338, 16433506899074513700, 14763562571101437023, 2547950465160283358},
	}

	for i := range sks {
		sk := limbs5ToLE40Big(sks[i])
		k := limbs5ToLE40Big(ks[i])
		sig, err := LighterSignHashedMessageDeterministic(hashedMessages[i], sk, k)
		if err != nil {
			t.Fatalf("vector %d sign failed: %v", i, err)
		}
		for j := 0; j < 5; j++ {
			gotS := binary.LittleEndian.Uint64(sig[j*8:])
			gotE := binary.LittleEndian.Uint64(sig[40+j*8:])
			if gotS != expectedSs[i][j] {
				t.Fatalf("vector %d sig.S[%d] = %d, want %d", i, j, gotS, expectedSs[i][j])
			}
			if gotE != expectedEs[i][j] {
				t.Fatalf("vector %d sig.E[%d] = %d, want %d", i, j, gotE, expectedEs[i][j])
			}
		}
		pk := LighterPubKeyFromScalar(sk)
		msgBytes := limbs5ToLE40Bytes(hashedMessages[i])
		if err := LighterVerifySignature(pk[:], msgBytes[:], sig[:]); err != nil {
			t.Fatalf("vector %d verify failed: %v", i, err)
		}
	}
}

func TestLighterKeyDerivationAndNonCanonicalRejection(t *testing.T) {
	t.Parallel()
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	sk, err := DeriveLighterScalarFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	defer sk.SetInt64(0)

	pk := LighterPubKeyFromScalar(sk)
	digest, limbs, err := LighterCreateOrderHash(300, 1, 1_760_000_000_000, 42, 0, 0, 777, 1000, 250000, 0, 0, 1, 0, 0, 1_760_003_600_000)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := LighterSignHashedMessage(limbs, sk)
	if err != nil {
		t.Fatal(err)
	}
	if err := LighterVerifySignature(pk[:], digest[:], sig[:]); err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	// Mutating signature rejects.
	badSig := sig
	badSig[0] ^= 0xff
	if err := LighterVerifySignature(pk[:], digest[:], badSig[:]); err == nil {
		t.Fatal("expected modified signature failure")
	}

	// Non-canonical s >= curve order rejects.
	nonCanon := sig
	orderBytes := scalarToLE40(new(big.Int).Sub(ecgfp5Order, big.NewInt(1)))
	copy(nonCanon[:40], orderBytes[:])
	// Force all bytes 0xff which exceeds ecgfp5Order (< 2^319).
	for i := 0; i < 40; i++ {
		nonCanon[i] = 0xff
	}
	if err := LighterVerifySignature(pk[:], digest[:], nonCanon[:]); err == nil {
		t.Fatal("expected non-canonical scalar rejection")
	}

	// Deterministic tx hash stability check.
	digest2, _, err := LighterCreateOrderHash(300, 1, 1_760_000_000_000, 42, 0, 0, 777, 1000, 250000, 0, 0, 1, 0, 0, 1_760_003_600_000)
	if err != nil || hex.EncodeToString(digest[:]) != hex.EncodeToString(digest2[:]) {
		t.Fatalf("tx hash nondeterministic: %x vs %x", digest, digest2)
	}
}
