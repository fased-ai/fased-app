package signer

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/big"
	"math/bits"
)

const (
	goldilocksMod                  = uint64(0xffffffff00000001)
	goldilocksEps                  = uint64(0xffffffff)
	goldilocksTwoAdicity           = 32
	goldilocksPowerOfTwoGen        = uint64(7277203076849721926)
	fp5W                           = uint64(3)
	fp5DthRoot                     = uint64(1041288259238279555)
	poseidon2Width                 = 12
	poseidon2Rate                  = 8
	poseidon2RoundsF               = 8
	poseidon2RoundsP               = 22
	LighterTxTypeCreateOrder uint8 = 14
	LighterTxTypeCancelOrder uint8 = 15
	LighterTxTypeUpdateLev   uint8 = 20
)

var (
	goldilocksOrderBig = new(big.Int).SetUint64(goldilocksMod)
	ecgfp5Order, _     = new(big.Int).SetString("1067993516717146951041484916571792702745057740581727230159139685185762082554198619328292418486241", 10)
	fp5Zero            = fp5{}
	fp5One             = fp5{1, 0, 0, 0, 0}
	fp5Two             = fp5{2, 0, 0, 0, 0}
	ecgfp5CoeffA       = fp5{2, 0, 0, 0, 0}
	ecgfp5CoeffB       = fp5{0, 263, 0, 0, 0}
	ecgfp5CoeffBMul2   = fp5{0, 526, 0, 0, 0}
	ecgfp5CoeffBMul4   = fp5{0, 1052, 0, 0, 0}
	ecgfp5Neutral      = ecgfp5Point{x: fp5Zero, z: fp5One, u: fp5Zero, t: fp5One}
	ecgfp5Generator    = ecgfp5Point{
		x: fp5{
			12883135586176881569,
			4356519642755055268,
			5248930565894896907,
			2165973894480315022,
			2448410071095648785,
		},
		z: fp5One,
		u: fp5One,
		t: fp5{4, 0, 0, 0, 0},
	}

	poseidon2ExternalConstants = [poseidon2RoundsF][poseidon2Width]uint64{
		{
			15492826721047263190, 11728330187201910315, 8836021247773420868, 16777404051263952451,
			5510875212538051896, 6173089941271892285, 2927757366422211339, 10340958981325008808,
			8541987352684552425, 9739599543776434497, 15073950188101532019, 12084856431752384512,
		},
		{
			4584713381960671270, 8807052963476652830, 54136601502601741, 4872702333905478703,
			5551030319979516287, 12889366755535460989, 16329242193178844328, 412018088475211848,
			10505784623379650541, 9758812378619434837, 7421979329386275117, 375240370024755551,
		},
		{
			3331431125640721931, 15684937309956309981, 578521833432107983, 14379242000670861838,
			17922409828154900976, 8153494278429192257, 15904673920630731971, 11217863998460634216,
			3301540195510742136, 9937973023749922003, 3059102938155026419, 1895288289490976132,
		},
		{
			5580912693628927540, 10064804080494788323, 9582481583369602410, 10186259561546797986,
			247426333829703916, 13193193905461376067, 6386232593701758044, 17954717245501896472,
			1531720443376282699, 2455761864255501970, 11234429217864304495, 4746959618548874102,
		},
		{
			13571697342473846203, 17477857865056504753, 15963032953523553760, 16033593225279635898,
			14252634232868282405, 8219748254835277737, 7459165569491914711, 15855939513193752003,
			16788866461340278896, 7102224659693946577, 3024718005636976471, 13695468978618890430,
		},
		{
			8214202050877825436, 2670727992739346204, 16259532062589659211, 11869922396257088411,
			3179482916972760137, 13525476046633427808, 3217337278042947412, 14494689598654046340,
			15837379330312175383, 8029037639801151344, 2153456285263517937, 8301106462311849241,
		},
		{
			13294194396455217955, 17394768489610594315, 12847609130464867455, 14015739446356528640,
			5879251655839607853, 9747000124977436185, 8950393546890284269, 10765765936405694368,
			14695323910334139959, 16366254691123000864, 15292774414889043182, 10910394433429313384,
		},
		{
			17253424460214596184, 3442854447664030446, 3005570425335613727, 10859158614900201063,
			9763230642109343539, 6647722546511515039, 909012944955815706, 18101204076790399111,
			11588128829349125809, 15863878496612806566, 5201119062417750399, 176665553780565743,
		},
	}

	poseidon2InternalConstants = [poseidon2RoundsP]uint64{
		11921381764981422944, 10318423381711320787, 8291411502347000766, 229948027109387563,
		9152521390190983261, 7129306032690285515, 15395989607365232011, 8641397269074305925,
		17256848792241043600, 6046475228902245682, 12041608676381094092, 12785542378683951657,
		14546032085337914034, 3304199118235116851, 16499627707072547655, 10386478025625759321,
		13475579315436919170, 16042710511297532028, 1411266850385657080, 9024840976168649958,
		14047056970978379368, 838728605080212101,
	}

	poseidon2MatrixDiag12 = [poseidon2Width]uint64{
		0xc3b6c08e23ba9300, 0xd84b5de94a324fb6, 0x0d0c371c5b35b84f, 0x7964f570e7188037,
		0x5daf18bbd996604b, 0x6743bc47b9595257, 0x5528b9362c59bb70, 0xac45e25b7127b68b,
		0xa2077d7dfbb606b5, 0xf3faac6faee378ae, 0x0c6388b51545e883, 0xd27dbb6944917b60,
	}
)

type u128 struct {
	hi, lo uint64
}

func addU128(a, b u128) u128 {
	lo, c := bits.Add64(a.lo, b.lo, 0)
	return u128{hi: a.hi + b.hi + c, lo: lo}
}

func addU128U64(a u128, b uint64) u128 {
	lo, c := bits.Add64(a.lo, b, 0)
	return u128{hi: a.hi + c, lo: lo}
}

func addU64Pair(a, b uint64) u128 {
	lo, c := bits.Add64(a, b, 0)
	return u128{hi: c, lo: lo}
}

func mulU64(a, b uint64) u128 {
	hi, lo := bits.Mul64(a, b)
	return u128{hi: hi, lo: lo}
}

func gCanon(x uint64) uint64 {
	if x >= goldilocksMod {
		return x - goldilocksMod
	}
	return x
}

func gAdd(a, b uint64) uint64 {
	sum, over := bits.Add64(a, b, 0)
	sum, over = bits.Add64(sum, goldilocksEps&-over, 0)
	sum += goldilocksEps & -over
	return sum
}

func gAddCanon(a uint64, rhs uint64) uint64 {
	sum, over := bits.Add64(a, rhs, 0)
	return sum + (goldilocksEps & -over)
}

func gSub(a, b uint64) uint64 {
	diff, borrow := bits.Sub64(a, b, 0)
	diff, borrow = bits.Sub64(diff, goldilocksEps&-borrow, 0)
	diff -= goldilocksEps & -borrow
	return diff
}

func gNeg(a uint64) uint64 {
	c := gCanon(a)
	if c == 0 {
		return 0
	}
	return goldilocksMod - c
}

func gReduce128(lo, hi uint64) uint64 {
	hiHi := hi >> 32
	hiLo := hi & goldilocksEps
	t0, borrow := bits.Sub64(lo, hiHi, 0)
	t0 -= goldilocksEps & -borrow
	t1 := hiLo * goldilocksEps
	sum, over := bits.Add64(t0, t1, 0)
	return sum + (goldilocksEps & -over)
}

func gReduce96(x u128) uint64 {
	t1 := x.hi * goldilocksEps
	sum, over := bits.Add64(x.lo, t1, 0)
	return sum + (goldilocksEps & -over)
}

func gMul(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	return gReduce128(lo, hi)
}

func gSquare(a uint64) uint64 { return gMul(a, a) }

func gExpPow2(x uint64, n uint) uint64 {
	z := x
	for i := uint(0); i < n; i++ {
		z = gSquare(z)
	}
	return z
}

func gExp(x, exponent uint64) uint64 {
	cur := x
	prod := uint64(1)
	for exponent > 0 {
		if exponent&1 == 1 {
			prod = gMul(prod, cur)
		}
		cur = gSquare(cur)
		exponent >>= 1
	}
	return prod
}

func gInvOrZero(self uint64) uint64 {
	if gCanon(self) == 0 {
		return 0
	}
	t2 := gMul(gSquare(self), self)
	t3 := gMul(gSquare(t2), self)
	t6 := gMul(gExpPow2(t3, 3), t3)
	t12 := gMul(gExpPow2(t6, 6), t6)
	t24 := gMul(gExpPow2(t12, 12), t12)
	t30 := gMul(gExpPow2(t24, 6), t6)
	t31 := gMul(gSquare(t30), self)
	t63 := gMul(gExpPow2(t31, 32), t31)
	return gMul(gSquare(t63), self)
}

func gIsQR(x uint64) bool {
	if gCanon(x) == 0 {
		return true
	}
	pow := (goldilocksMod - 1) >> 1
	return gCanon(gExp(x, pow)) == 1
}

func gSqrt(self uint64) (uint64, bool) {
	if gCanon(self) == 0 {
		return 0, true
	}
	if !gIsQR(self) {
		return 0, false
	}
	cSelf := gCanon(self)
	t := (goldilocksMod - 1) / (1 << goldilocksTwoAdicity)
	z := goldilocksPowerOfTwoGen
	w := gExp(cSelf, (t-1)/2)
	x := gMul(cSelf, w)
	b := gMul(x, w)
	v := goldilocksTwoAdicity
	for gCanon(b) != 1 {
		k := 0
		b2k := b
		for gCanon(b2k) != 1 {
			b2k = gSquare(b2k)
			k++
		}
		j := v - k - 1
		w = z
		for n := 0; n < j; n++ {
			w = gSquare(w)
		}
		z = gSquare(w)
		b = gMul(b, z)
		x = gMul(x, w)
		v = k
	}
	return gCanon(x), true
}

func gPowers(e uint64, count int) [5]uint64 {
	var ret [5]uint64
	ret[0] = 1
	for i := 1; i < count; i++ {
		ret[i] = gMul(ret[i-1], e)
	}
	return ret
}

type fp5 [5]uint64

func (e fp5) toLEBytes() [40]byte {
	var out [40]byte
	for i := 0; i < 5; i++ {
		binary.LittleEndian.PutUint64(out[i*8:], gCanon(e[i]))
	}
	return out
}

func fp5FromCanonicalLEBytes(in []byte) (fp5, error) {
	if len(in) != 40 {
		return fp5{}, errors.New("expected 40-byte canonical Fp5 element")
	}
	var e fp5
	for i := 0; i < 5; i++ {
		limb := binary.LittleEndian.Uint64(in[i*8:])
		if limb >= goldilocksMod {
			return fp5{}, errors.New("non-canonical Goldilocks limb in Fp5 element")
		}
		e[i] = limb
	}
	return e, nil
}

func fp5IsZero(e fp5) bool {
	return gCanon(e[0]) == 0 && gCanon(e[1]) == 0 && gCanon(e[2]) == 0 && gCanon(e[3]) == 0 && gCanon(e[4]) == 0
}

func fp5Neg(e fp5) fp5 {
	return fp5{gNeg(e[0]), gNeg(e[1]), gNeg(e[2]), gNeg(e[3]), gNeg(e[4])}
}

func fp5Add(a, b fp5) fp5 {
	return fp5{gAdd(a[0], b[0]), gAdd(a[1], b[1]), gAdd(a[2], b[2]), gAdd(a[3], b[3]), gAdd(a[4], b[4])}
}

func fp5Sub(a, b fp5) fp5 {
	return fp5{gSub(a[0], b[0]), gSub(a[1], b[1]), gSub(a[2], b[2]), gSub(a[3], b[3]), gSub(a[4], b[4])}
}

func fp5Double(a fp5) fp5 { return fp5Add(a, a) }

func fp5ScalarMul(a fp5, s uint64) fp5 {
	return fp5{gMul(a[0], s), gMul(a[1], s), gMul(a[2], s), gMul(a[3], s), gMul(a[4], s)}
}

type acc192 struct{ lo, mid, hi uint64 }

func (s acc192) add3(x0, x1, x2 uint64) (res acc192) {
	var c uint64
	res.lo, c = bits.Add64(s.lo, x0, 0)
	res.mid, c = bits.Add64(s.mid, x1, c)
	res.hi = s.hi + x2 + c
	return res
}

func (s acc192) addProduct(a, b uint64) acc192 {
	hi, lo := bits.Mul64(a, b)
	return s.add3(lo, hi, 0)
}

func (s acc192) addProduct2(a, b uint64) acc192 {
	hi, lo := bits.Mul64(a, b)
	return s.add3(lo<<1, (hi<<1)|(lo>>63), hi>>63)
}

func (s acc192) addProduct3(a, b uint64) acc192 {
	hi, lo := bits.Mul64(a, b)
	d0, d1, d2 := lo<<1, (hi<<1)|(lo>>63), hi>>63
	var c uint64
	d0, c = bits.Add64(d0, lo, 0)
	d1, c = bits.Add64(d1, hi, c)
	d2 += c
	return s.add3(d0, d1, d2)
}

func (s acc192) addProduct6(a, b uint64) acc192 {
	hi, lo := bits.Mul64(a, b)
	q0, q1, q2 := lo<<2, (hi<<2)|(lo>>62), hi>>62
	r0, r1, r2 := lo<<1, (hi<<1)|(lo>>63), hi>>63
	var c uint64
	q0, c = bits.Add64(q0, r0, 0)
	q1, c = bits.Add64(q1, r1, c)
	q2 += r2 + c
	return s.add3(q0, q1, q2)
}

func (s acc192) reduce() uint64 {
	rLow := gReduce128(s.lo, s.mid)
	r2 := gReduce128(s.hi<<32, s.hi>>32)
	res, borrow := bits.Sub64(rLow, r2, 0)
	return res - (goldilocksEps & -borrow)
}

func fp5Mul(a, b fp5) fp5 {
	a0, a1, a2, a3, a4 := a[0], a[1], a[2], a[3], a[4]
	b0, b1, b2, b3, b4 := b[0], b[1], b[2], b[3], b[4]
	var s0, s1, s2, s3, s4 acc192

	s0 = s0.addProduct(a0, b0).addProduct3(a1, b4).addProduct3(a2, b3).addProduct3(a3, b2).addProduct3(a4, b1)
	s1 = s1.addProduct(a0, b1).addProduct(a1, b0).addProduct3(a2, b4).addProduct3(a3, b3).addProduct3(a4, b2)
	s2 = s2.addProduct(a0, b2).addProduct(a1, b1).addProduct(a2, b0).addProduct3(a3, b4).addProduct3(a4, b3)
	s3 = s3.addProduct(a0, b3).addProduct(a1, b2).addProduct(a2, b1).addProduct(a3, b0).addProduct3(a4, b4)
	s4 = s4.addProduct(a0, b4).addProduct(a1, b3).addProduct(a2, b2).addProduct(a3, b1).addProduct(a4, b0)

	return fp5{s0.reduce(), s1.reduce(), s2.reduce(), s3.reduce(), s4.reduce()}
}

func fp5Square(a fp5) fp5 {
	a0, a1, a2, a3, a4 := a[0], a[1], a[2], a[3], a[4]
	var s0, s1, s2, s3, s4 acc192

	s0 = s0.addProduct(a0, a0).addProduct6(a1, a4).addProduct6(a2, a3)
	s1 = s1.addProduct2(a0, a1).addProduct6(a2, a4).addProduct3(a3, a3)
	s2 = s2.addProduct2(a0, a2).addProduct(a1, a1).addProduct6(a3, a4)
	s3 = s3.addProduct2(a0, a3).addProduct2(a1, a2).addProduct3(a4, a4)
	s4 = s4.addProduct2(a0, a4).addProduct2(a1, a3).addProduct(a2, a2)

	return fp5{s0.reduce(), s1.reduce(), s2.reduce(), s3.reduce(), s4.reduce()}
}

func fp5ExpPow2(x fp5, n uint) fp5 {
	z := x
	for i := uint(0); i < n; i++ {
		z = fp5Square(z)
	}
	return z
}

func fp5RepeatedFrobenius(x fp5, count int) fp5 {
	count %= 5
	if count == 0 {
		return x
	}
	z0 := fp5DthRoot
	for i := 1; i < count; i++ {
		z0 = gMul(fp5DthRoot, z0)
	}
	pows := gPowers(z0, 5)
	return fp5{
		gMul(x[0], pows[0]),
		gMul(x[1], pows[1]),
		gMul(x[2], pows[2]),
		gMul(x[3], pows[3]),
		gMul(x[4], pows[4]),
	}
}

func fp5Frobenius(x fp5) fp5 { return fp5RepeatedFrobenius(x, 1) }

func fp5InvOrZero(a fp5) fp5 {
	if fp5IsZero(a) {
		return fp5Zero
	}
	d := fp5Frobenius(a)
	e := fp5Mul(d, fp5Frobenius(d))
	f := fp5Mul(e, fp5RepeatedFrobenius(e, 2))
	added := gAdd(gAdd(gMul(a[1], f[4]), gMul(a[2], f[3])), gAdd(gMul(a[3], f[2]), gMul(a[4], f[1])))
	gg := gAdd(gMul(a[0], f[0]), gMul(fp5W, added))
	return fp5ScalarMul(f, gInvOrZero(gg))
}

func fp5Div(a, b fp5) fp5 { return fp5Mul(a, fp5InvOrZero(b)) }

func fp5Legendre(x fp5) uint64 {
	frob1 := fp5Frobenius(x)
	frob2 := fp5Frobenius(frob1)
	frob12 := fp5Mul(frob1, frob2)
	frob212 := fp5RepeatedFrobenius(frob12, 2)
	xr := fp5Mul(fp5Mul(x, frob12), frob212)[0]
	xr31 := gExpPow2(xr, 31)
	xr63 := gExpPow2(xr31, 32)
	return gCanon(gMul(xr63, gInvOrZero(xr31)))
}

func fp5Sqrt(x fp5) (fp5, bool) {
	v := fp5ExpPow2(x, 31)
	d := fp5Mul(fp5Mul(x, fp5ExpPow2(v, 32)), fp5InvOrZero(v))
	e := fp5Frobenius(fp5Mul(d, fp5RepeatedFrobenius(d, 2)))
	f := fp5Square(e)
	added := gAdd(gAdd(gMul(x[1], f[4]), gMul(x[2], f[3])), gAdd(gMul(x[3], f[2]), gMul(x[4], f[1])))
	gg := gAdd(gMul(x[0], f[0]), gMul(fp5W, added))
	s, ok := gSqrt(gg)
	if !ok {
		return fp5Zero, false
	}
	return fp5Mul(fp5{s, 0, 0, 0, 0}, fp5InvOrZero(e)), true
}

func fp5Sgn0(x fp5) bool {
	sign := false
	zero := true
	for _, limb := range x {
		canon := gCanon(limb)
		signI := (canon & 1) == 0
		zeroI := canon == 0
		sign = sign || (zero && signI)
		zero = zero && zeroI
	}
	return sign
}

func fp5CanonicalSqrt(x fp5) (fp5, bool) {
	s, ok := fp5Sqrt(x)
	if !ok {
		return fp5Zero, false
	}
	if fp5Sgn0(s) {
		return fp5Neg(s), true
	}
	return s, true
}

func poseidon2Sbox(s *[poseidon2Width]uint64) {
	for i := 0; i < poseidon2Width; i++ {
		p := s[i]
		p2 := gSquare(p)
		p4 := gSquare(p2)
		s[i] = gMul(gMul(p, p2), p4)
	}
}

func poseidon2ExternalLinearLayer(s *[poseidon2Width]uint64, rc *[poseidon2Width]uint64) {
	chunkMDS := func(v0, v1, v2, v3 uint64) (u128, u128, u128, u128) {
		u0 := u128{lo: v0}
		u1 := u128{lo: v1}
		u2 := u128{lo: v2}
		u3 := u128{lo: v3}
		t01 := addU128(u0, u1)
		t23 := addU128(u2, u3)
		t := addU128(t01, t23)
		n0 := addU128(addU128(t, t01), u1)
		n1 := addU128(addU128(t, u1), addU128(u2, u2))
		n2 := addU128(addU128(t, t23), u3)
		n3 := addU128(addU128(t, u3), addU128(u0, u0))
		return n0, n1, n2, n3
	}
	n0, n1, n2, n3 := chunkMDS(s[0], s[1], s[2], s[3])
	n4, n5, n6, n7 := chunkMDS(s[4], s[5], s[6], s[7])
	n8, n9, n10, n11 := chunkMDS(s[8], s[9], s[10], s[11])

	sum0 := addU128(n0, addU128(n4, n8))
	sum1 := addU128(n1, addU128(n5, n9))
	sum2 := addU128(n2, addU128(n6, n10))
	sum3 := addU128(n3, addU128(n7, n11))

	put := func(n, sum u128, idx int) {
		val := addU128(n, sum)
		if rc != nil {
			val = addU128U64(val, rc[idx])
		}
		s[idx] = gReduce96(val)
	}
	put(n0, sum0, 0)
	put(n4, sum0, 4)
	put(n8, sum0, 8)
	put(n1, sum1, 1)
	put(n5, sum1, 5)
	put(n9, sum1, 9)
	put(n2, sum2, 2)
	put(n6, sum2, 6)
	put(n10, sum2, 10)
	put(n3, sum3, 3)
	put(n7, sum3, 7)
	put(n11, sum3, 11)
}

func poseidon2PartialRounds(state *[poseidon2Width]uint64) {
	state[0] = gAddCanon(state[0], poseidon2InternalConstants[0])
	for r := 0; r < poseidon2RoundsP; r++ {
		p := state[0]
		p2 := gSquare(p)
		p4 := gSquare(p2)
		s0 := gMul(gMul(p, p2), p4)

		r0 := addU64Pair(state[1], state[2])
		r1 := addU64Pair(state[3], state[4])
		r2 := addU64Pair(state[5], state[6])
		r3 := addU64Pair(state[7], state[8])
		r4 := addU64Pair(state[9], state[10])
		q0 := addU128(r0, r1)
		q1 := addU128(r2, r3)
		q2 := addU128U64(r4, state[11])
		restSum := addU128(addU128(q0, q1), q2)
		sum := addU128U64(restSum, s0)

		rc0 := poseidon2ExternalConstants[4][0]
		if r+1 < poseidon2RoundsP {
			rc0 = poseidon2InternalConstants[r+1]
		}
		state[0] = gReduce128(
			addU128U64(addU128(mulU64(s0, poseidon2MatrixDiag12[0]), sum), rc0).lo,
			addU128U64(addU128(mulU64(s0, poseidon2MatrixDiag12[0]), sum), rc0).hi,
		)
		for i := 1; i < poseidon2Width; i++ {
			v := addU128(mulU64(state[i], poseidon2MatrixDiag12[i]), sum)
			if r+1 >= poseidon2RoundsP {
				v = addU128U64(v, poseidon2ExternalConstants[4][i])
			}
			state[i] = gReduce128(v.lo, v.hi)
		}
	}
}

func poseidon2Permute(input *[poseidon2Width]uint64) {
	poseidon2ExternalLinearLayer(input, &poseidon2ExternalConstants[0])
	poseidon2Sbox(input)
	poseidon2ExternalLinearLayer(input, &poseidon2ExternalConstants[1])
	poseidon2Sbox(input)
	poseidon2ExternalLinearLayer(input, &poseidon2ExternalConstants[2])
	poseidon2Sbox(input)
	poseidon2ExternalLinearLayer(input, &poseidon2ExternalConstants[3])
	poseidon2Sbox(input)
	poseidon2ExternalLinearLayer(input, nil)

	poseidon2PartialRounds(input)

	poseidon2Sbox(input)
	poseidon2ExternalLinearLayer(input, &poseidon2ExternalConstants[5])
	poseidon2Sbox(input)
	poseidon2ExternalLinearLayer(input, &poseidon2ExternalConstants[6])
	poseidon2Sbox(input)
	poseidon2ExternalLinearLayer(input, &poseidon2ExternalConstants[7])
	poseidon2Sbox(input)
	poseidon2ExternalLinearLayer(input, nil)
}

// LighterPoseidon2HashToFp5 hashes Goldilocks field elements using Poseidon2-Goldilocks-Plonky2
// (width 12, rate 8, no padding) and outputs a canonical 5-limb quintic extension element.
func LighterPoseidon2HashToFp5(elems []uint64) [5]uint64 {
	var perm [poseidon2Width]uint64
	for i := 0; i < len(elems); i += poseidon2Rate {
		for j := 0; j < poseidon2Rate && i+j < len(elems); j++ {
			perm[j] = elems[i+j]
		}
		poseidon2Permute(&perm)
	}
	return [5]uint64{
		gCanon(perm[0]),
		gCanon(perm[1]),
		gCanon(perm[2]),
		gCanon(perm[3]),
		gCanon(perm[4]),
	}
}

type ecgfp5Point struct {
	x, z, u, t fp5
}

func (p ecgfp5Point) isNeutral() bool { return fp5IsZero(p.u) }

func (p ecgfp5Point) encode() fp5 {
	res := fp5Div(p.t, p.u)
	for i := 0; i < 5; i++ {
		res[i] = gCanon(res[i])
	}
	return res
}

func ecgfp5Decode(w fp5) (ecgfp5Point, bool) {
	e := fp5Sub(fp5Square(w), ecgfp5CoeffA)
	delta := fp5Sub(fp5Square(e), ecgfp5CoeffBMul4)
	r, c := fp5CanonicalSqrt(delta)
	if !c {
		r = fp5Zero
	}
	x1 := fp5Div(fp5Add(e, r), fp5Two)
	x2 := fp5Div(fp5Sub(e, r), fp5Two)
	x := x1
	if fp5Legendre(x1) == 1 {
		x = x2
	}
	if !c {
		return ecgfp5Neutral, fp5IsZero(w)
	}
	return ecgfp5Point{x: x, z: fp5One, u: fp5One, t: w}, true
}

func (p ecgfp5Point) add(rhs ecgfp5Point) ecgfp5Point {
	t1 := fp5Mul(p.x, rhs.x)
	t2 := fp5Mul(p.z, rhs.z)
	t3 := fp5Mul(p.u, rhs.u)
	t4 := fp5Mul(p.t, rhs.t)
	t5 := fp5Sub(fp5Mul(fp5Add(p.x, p.z), fp5Add(rhs.x, rhs.z)), fp5Add(t1, t2))
	t6 := fp5Sub(fp5Mul(fp5Add(p.u, p.t), fp5Add(rhs.u, rhs.t)), fp5Add(t3, t4))
	t7 := fp5Add(t1, fp5Mul(t2, ecgfp5CoeffB))
	t8 := fp5Mul(t4, t7)
	t9 := fp5Mul(t3, fp5Add(fp5Mul(t5, ecgfp5CoeffBMul2), fp5Double(t7)))
	t10 := fp5Mul(fp5Add(t4, fp5Double(t3)), fp5Add(t5, t7))

	return ecgfp5Point{
		x: fp5Mul(fp5Sub(t10, t8), ecgfp5CoeffB),
		z: fp5Sub(t8, t9),
		u: fp5Mul(t6, fp5Sub(fp5Mul(t2, ecgfp5CoeffB), t1)),
		t: fp5Add(t8, t9),
	}
}

func (p ecgfp5Point) double() ecgfp5Point {
	t1 := fp5Mul(p.z, p.t)
	t2 := fp5Mul(t1, p.t)
	x1 := fp5Square(t2)
	z1 := fp5Mul(t1, p.u)
	t3 := fp5Square(p.u)
	w1 := fp5Sub(t2, fp5Mul(t3, fp5Double(fp5Add(p.x, p.z))))
	t4 := fp5Square(z1)

	xNew := fp5Mul(t4, ecgfp5CoeffBMul4)
	zNew := fp5Square(w1)
	uNew := fp5Sub(fp5Square(fp5Add(w1, z1)), fp5Add(t4, zNew))
	tNew := fp5Sub(fp5Double(x1), fp5Add(fp5Mul(t4, fp5{4, 0, 0, 0, 0}), zNew))
	return ecgfp5Point{x: xNew, z: zNew, u: uNew, t: tNew}
}

func selectFp5(bit uint64, a, b fp5) fp5 {
	mask := -bit
	return fp5{
		a[0] ^ (mask & (a[0] ^ b[0])),
		a[1] ^ (mask & (a[1] ^ b[1])),
		a[2] ^ (mask & (a[2] ^ b[2])),
		a[3] ^ (mask & (a[3] ^ b[3])),
		a[4] ^ (mask & (a[4] ^ b[4])),
	}
}

func selectECgFp5Point(bit uint64, a, b ecgfp5Point) ecgfp5Point {
	return ecgfp5Point{
		x: selectFp5(bit, a.x, b.x),
		z: selectFp5(bit, a.z, b.z),
		u: selectFp5(bit, a.u, b.u),
		t: selectFp5(bit, a.t, b.t),
	}
}

func scalarToLE40(s *big.Int) [40]byte {
	var out [40]byte
	if s == nil {
		return out
	}
	canon := new(big.Int).Mod(s, ecgfp5Order)
	var be [40]byte
	canon.FillBytes(be[:])
	for i := 0; i < 40; i++ {
		out[i] = be[39-i]
	}
	return out
}

func scalarFromCanonicalLE40(b []byte) (*big.Int, error) {
	if len(b) != 40 {
		return nil, errors.New("expected 40-byte ECgFp5 scalar")
	}
	var be [40]byte
	for i := 0; i < 40; i++ {
		be[i] = b[39-i]
	}
	v := new(big.Int).SetBytes(be[:])
	if v.Cmp(ecgfp5Order) >= 0 {
		return nil, errors.New("non-canonical ECgFp5 scalar")
	}
	return v, nil
}

func scalarFromFp5(e fp5) *big.Int {
	b := e.toLEBytes()
	var be [40]byte
	for i := 0; i < 40; i++ {
		be[i] = b[39-i]
	}
	return new(big.Int).Mod(new(big.Int).SetBytes(be[:]), ecgfp5Order)
}

func (p ecgfp5Point) scalarMulConstTime(s *big.Int) ecgfp5Point {
	var sc [5]uint64
	if s != nil {
		canon := new(big.Int).Mod(s, ecgfp5Order)
		le := scalarToLE40(canon)
		for i := 0; i < 5; i++ {
			sc[i] = binary.LittleEndian.Uint64(le[i*8:])
		}
		clear(le[:])
		canon.SetInt64(0)
	}
	acc := ecgfp5Neutral
	for bit := 319; bit >= 0; bit-- {
		acc = acc.double()
		b := (sc[bit/64] >> uint(bit%64)) & 1
		added := acc.add(p)
		acc = selectECgFp5Point(b, acc, added)
	}
	clear(sc[:])
	return acc
}

// DeriveLighterScalarFromSeed derives a domain-separated ECgFp5 private scalar
// strictly bound to Lighter L2 without sharing key bits with Solana or EVM keys.
func DeriveLighterScalarFromSeed(seed []byte) (*big.Int, error) {
	if len(seed) != 32 {
		return nil, errors.New("valid 32-byte seed required")
	}
	for counter := range 16 {
		mac1 := hmac.New(sha256.New, seed)
		_, _ = mac1.Write([]byte("fased-signer-lighter-v1-a:"))
		_, _ = mac1.Write([]byte{byte(counter)})
		part1 := mac1.Sum(nil)

		mac2 := hmac.New(sha256.New, seed)
		_, _ = mac2.Write([]byte("fased-signer-lighter-v1-b:"))
		_, _ = mac2.Write([]byte{byte(counter)})
		part2 := mac2.Sum(nil)

		var raw [40]byte
		copy(raw[:32], part1)
		copy(raw[32:], part2[:8])
		clear(part1)
		clear(part2)

		sk := new(big.Int).Mod(new(big.Int).SetBytes(raw[:]), ecgfp5Order)
		clear(raw[:])
		if sk.Sign() > 0 {
			return sk, nil
		}
	}
	return nil, errors.New("failed to derive non-zero Lighter scalar")
}

// LighterPubKeyFromScalar returns the canonical 40-byte little-endian ECgFp5 public key.
func LighterPubKeyFromScalar(sk *big.Int) [40]byte {
	return ecgfp5Generator.scalarMulConstTime(sk).encode().toLEBytes()
}

// LighterSignHashedMessageDeterministic produces a Schnorr-ECgFp5 signature (LE40(s)||LE40(e))
// given private scalar sk and explicit nonce scalar k.
func LighterSignHashedMessageDeterministic(hashedMsg [5]uint64, sk, k *big.Int) ([80]byte, error) {
	var out [80]byte
	if sk == nil || sk.Sign() <= 0 || k == nil || k.Sign() <= 0 {
		return out, errors.New("non-zero ECgFp5 scalar and nonce required")
	}
	r := ecgfp5Generator.scalarMulConstTime(k).encode()
	var pre [10]uint64
	copy(pre[:5], r[:])
	for i := 0; i < 5; i++ {
		pre[5+i] = gCanon(hashedMsg[i])
	}
	e := scalarFromFp5(fp5(LighterPoseidon2HashToFp5(pre[:])))

	// s = (k - e * sk) mod n
	esk := new(big.Int).Mul(e, sk)
	s := new(big.Int).Sub(k, esk)
	s.Mod(s, ecgfp5Order)
	esk.SetInt64(0)

	sBytes := scalarToLE40(s)
	eBytes := scalarToLE40(e)
	s.SetInt64(0)
	e.SetInt64(0)
	copy(out[:40], sBytes[:])
	copy(out[40:], eBytes[:])
	return out, nil
}

// LighterSignHashedMessage signs a canonical 40-byte Poseidon2 tx hash using fresh CSPRNG nonce.
func LighterSignHashedMessage(hashedMsg [5]uint64, sk *big.Int) ([80]byte, error) {
	if sk == nil || sk.Sign() <= 0 {
		return [80]byte{}, errors.New("non-zero Lighter scalar required")
	}
	for {
		k, err := rand.Int(rand.Reader, ecgfp5Order)
		if err != nil {
			return [80]byte{}, err
		}
		if k.Sign() > 0 {
			sig, err := LighterSignHashedMessageDeterministic(hashedMsg, sk, k)
			k.SetInt64(0)
			return sig, err
		}
	}
}

// LighterVerifySignature verifies a 40-byte canonical public key, 40-byte message hash,
// and 80-byte Schnorr signature on ECgFp5.
func LighterVerifySignature(pubKey40, hashedMsg40, sig80 []byte) error {
	pkElem, err := fp5FromCanonicalLEBytes(pubKey40)
	if err != nil {
		return err
	}
	msgElem, err := fp5FromCanonicalLEBytes(hashedMsg40)
	if err != nil {
		return err
	}
	if len(sig80) != 80 {
		return errors.New("invalid Lighter signature length")
	}
	s, err := scalarFromCanonicalLE40(sig80[:40])
	if err != nil {
		return err
	}
	e, err := scalarFromCanonicalLE40(sig80[40:])
	if err != nil {
		return err
	}
	pkPoint, ok := ecgfp5Decode(pkElem)
	if !ok {
		return errors.New("invalid Lighter ECgFp5 public key point")
	}
	sG := ecgfp5Generator.scalarMulConstTime(s)
	ePK := pkPoint.scalarMulConstTime(e)
	r := sG.add(ePK)
	if r.isNeutral() {
		return errors.New("Lighter signature point is neutral")
	}
	rEnc := r.encode()
	var pre [10]uint64
	copy(pre[:5], rEnc[:])
	copy(pre[5:], msgElem[:])
	eCheck := scalarFromFp5(fp5(LighterPoseidon2HashToFp5(pre[:])))
	if eCheck.Cmp(e) != 0 {
		return errors.New("Lighter Schnorr signature verification failed")
	}
	return nil
}

func goldilocksInt64(v int64) (uint64, error) {
	if v < 0 {
		return 0, errors.New("negative Goldilocks L2 tx field not permitted")
	}
	return gCanon(uint64(v)), nil
}

// LighterCreateOrderHash computes the exact Poseidon2-Plonky2 L2CreateOrderTxInfo digest.
func LighterCreateOrderHash(chainID uint32, nonce, expiredAt, accountIndex int64, apiKeyIndex uint8, marketIndex, clientOrderIndex, baseAmount int64, price uint32, isAsk, orderType, tif, reduceOnly uint8, triggerPrice uint32, orderExpiry int64) ([40]byte, [5]uint64, error) {
	vals := []int64{
		int64(chainID), int64(LighterTxTypeCreateOrder), nonce, expiredAt,
		accountIndex, int64(apiKeyIndex), marketIndex, clientOrderIndex,
		baseAmount, int64(price), int64(isAsk), int64(orderType),
		int64(tif), int64(reduceOnly), int64(triggerPrice), orderExpiry,
	}
	elems := make([]uint64, len(vals))
	for i, v := range vals {
		gVal, err := goldilocksInt64(v)
		if err != nil {
			return [40]byte{}, [5]uint64{}, err
		}
		elems[i] = gVal
	}
	h := LighterPoseidon2HashToFp5(elems)
	return fp5(h).toLEBytes(), h, nil
}

// LighterCancelOrderHash computes the exact Poseidon2-Plonky2 L2CancelOrderTxInfo digest.
func LighterCancelOrderHash(chainID uint32, nonce, expiredAt, accountIndex int64, apiKeyIndex uint8, marketIndex, orderIndex int64) ([40]byte, [5]uint64, error) {
	vals := []int64{
		int64(chainID), int64(LighterTxTypeCancelOrder), nonce, expiredAt,
		accountIndex, int64(apiKeyIndex), marketIndex, orderIndex,
	}
	elems := make([]uint64, len(vals))
	for i, v := range vals {
		gVal, err := goldilocksInt64(v)
		if err != nil {
			return [40]byte{}, [5]uint64{}, err
		}
		elems[i] = gVal
	}
	h := LighterPoseidon2HashToFp5(elems)
	return fp5(h).toLEBytes(), h, nil
}

// LighterUpdateLeverageHash computes the exact Poseidon2-Plonky2 L2UpdateLeverageTxInfo digest.
func LighterUpdateLeverageHash(chainID uint32, nonce, expiredAt, accountIndex int64, apiKeyIndex uint8, marketIndex int64, initialMarginFraction uint16, marginMode uint8) ([40]byte, [5]uint64, error) {
	vals := []int64{
		int64(chainID), int64(LighterTxTypeUpdateLev), nonce, expiredAt,
		accountIndex, int64(apiKeyIndex), marketIndex, int64(initialMarginFraction), int64(marginMode),
	}
	elems := make([]uint64, len(vals))
	for i, v := range vals {
		gVal, err := goldilocksInt64(v)
		if err != nil {
			return [40]byte{}, [5]uint64{}, err
		}
		elems[i] = gVal
	}
	h := LighterPoseidon2HashToFp5(elems)
	return fp5(h).toLEBytes(), h, nil
}
