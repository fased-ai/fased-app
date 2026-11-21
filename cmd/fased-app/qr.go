package main

import (
	"errors"
	"strings"
)

type qrVersionSpec struct {
	version        int
	totalCodewords int
	dataCodewords  int
	ecPerBlock     int
	group1Blocks   int
	group1Data     int
	group2Blocks   int
	group2Data     int
	alignment      []int
	remainderBits  int
}

// ISO/IEC 18004 Error Correction Level L tables for Versions 1..12.
var qrVersionsL = []qrVersionSpec{
	{1, 26, 19, 7, 1, 19, 0, 0, nil, 0},
	{2, 44, 34, 10, 1, 34, 0, 0, []int{6, 18}, 7},
	{3, 70, 55, 15, 1, 55, 0, 0, []int{6, 22}, 7},
	{4, 100, 80, 20, 1, 80, 0, 0, []int{6, 26}, 7},
	{5, 134, 108, 26, 1, 108, 0, 0, []int{6, 30}, 7},
	{6, 172, 136, 18, 2, 68, 0, 0, []int{6, 34}, 7},
	{7, 196, 156, 20, 2, 78, 0, 0, []int{6, 22, 38}, 0},
	{8, 242, 194, 24, 2, 97, 0, 0, []int{6, 24, 42}, 0},
	{9, 292, 232, 30, 2, 116, 0, 0, []int{6, 26, 46}, 0},
	{10, 346, 274, 18, 2, 68, 2, 69, []int{6, 28, 50}, 0},
	{11, 404, 324, 20, 4, 81, 0, 0, []int{6, 30, 54}, 0},
	{12, 466, 370, 24, 2, 92, 2, 93, []int{6, 32, 58}, 0},
}

var gfExp, gfLog = func() ([512]byte, [256]byte) {
	var exp [512]byte
	var log [256]byte
	v := 1
	for i := 0; i < 255; i++ {
		exp[i] = byte(v)
		log[v] = byte(i)
		v <<= 1
		if v&0x100 != 0 {
			v ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		exp[i] = exp[i-255]
	}
	return exp, log
}()

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func rsGeneratorPoly(ecLen int) []byte {
	gen := make([]byte, ecLen)
	gen[ecLen-1] = 1
	root := byte(1)
	for i := 0; i < ecLen; i++ {
		for j := 0; j < ecLen; j++ {
			gen[j] = gfMul(gen[j], root)
			if j+1 < ecLen {
				gen[j] ^= gen[j+1]
			}
		}
		root = gfMul(root, 0x02)
	}
	return gen
}

func rsRemainder(data []byte, ecLen int) []byte {
	gen := rsGeneratorPoly(ecLen)
	rem := make([]byte, ecLen)
	for _, b := range data {
		factor := b ^ rem[0]
		copy(rem, rem[1:])
		rem[ecLen-1] = 0
		for i := 0; i < ecLen; i++ {
			rem[i] ^= gfMul(gen[i], factor)
		}
	}
	return rem
}

type bitBuffer struct {
	bits []byte
	len  int
}

func (b *bitBuffer) push(val uint32, count int) {
	for i := count - 1; i >= 0; i-- {
		byteIdx := b.len / 8
		if byteIdx >= len(b.bits) {
			b.bits = append(b.bits, 0)
		}
		if (val>>i)&1 != 0 {
			b.bits[byteIdx] |= 1 << (7 - (b.len % 8))
		}
		b.len++
	}
}

func selectQRVersion(payloadLen int) (qrVersionSpec, error) {
	for _, spec := range qrVersionsL {
		countBits := 8
		if spec.version >= 10 {
			countBits = 16
		}
		neededBits := 4 + countBits + payloadLen*8
		if neededBits <= spec.dataCodewords*8 {
			return spec, nil
		}
	}
	return qrVersionSpec{}, errors.New("QR payload exceeds maximum capacity")
}

func encodeQRData(payload []byte, spec qrVersionSpec) []byte {
	var buf bitBuffer
	buf.push(0x4, 4) // Byte mode
	countBits := 8
	if spec.version >= 10 {
		countBits = 16
	}
	buf.push(uint32(len(payload)), countBits)
	for _, ch := range payload {
		buf.push(uint32(ch), 8)
	}
	maxBits := spec.dataCodewords * 8
	term := 4
	if maxBits-buf.len < term {
		term = maxBits - buf.len
	}
	if term > 0 {
		buf.push(0, term)
	}
	if rem := buf.len % 8; rem != 0 {
		buf.push(0, 8-rem)
	}
	padBytes := []byte{0xec, 0x11}
	for i := 0; len(buf.bits) < spec.dataCodewords; i++ {
		buf.push(uint32(padBytes[i%2]), 8)
	}
	return buf.bits[:spec.dataCodewords]
}

func interleaveQRCodewords(data []byte, spec qrVersionSpec) []byte {
	type block struct {
		data []byte
		ec   []byte
	}
	blocks := make([]block, 0, spec.group1Blocks+spec.group2Blocks)
	pos := 0
	for i := 0; i < spec.group1Blocks; i++ {
		d := data[pos : pos+spec.group1Data]
		pos += spec.group1Data
		blocks = append(blocks, block{data: d, ec: rsRemainder(d, spec.ecPerBlock)})
	}
	for i := 0; i < spec.group2Blocks; i++ {
		d := data[pos : pos+spec.group2Data]
		pos += spec.group2Data
		blocks = append(blocks, block{data: d, ec: rsRemainder(d, spec.ecPerBlock)})
	}

	out := make([]byte, 0, spec.totalCodewords)
	maxData := spec.group1Data
	if spec.group2Data > maxData {
		maxData = spec.group2Data
	}
	for col := 0; col < maxData; col++ {
		for _, blk := range blocks {
			if col < len(blk.data) {
				out = append(out, blk.data[col])
			}
		}
	}
	for col := 0; col < spec.ecPerBlock; col++ {
		for _, blk := range blocks {
			out = append(out, blk.ec[col])
		}
	}
	return out
}

type qrMatrix struct {
	size int
	dark [][]bool
	fn   [][]bool
}

func newQRMatrix(size int) *qrMatrix {
	dark := make([][]bool, size)
	fn := make([][]bool, size)
	for i := 0; i < size; i++ {
		dark[i] = make([]bool, size)
		fn[i] = make([]bool, size)
	}
	return &qrMatrix{size: size, dark: dark, fn: fn}
}

func (m *qrMatrix) setFunction(r, c int, isDark bool) {
	if r < 0 || r >= m.size || c < 0 || c >= m.size {
		return
	}
	m.dark[r][c] = isDark
	m.fn[r][c] = true
}

func (m *qrMatrix) placeFinder(topRow, leftCol int) {
	for dr := -1; dr <= 7; dr++ {
		for dc := -1; dc <= 7; dc++ {
			r, c := topRow+dr, leftCol+dc
			if r < 0 || r >= m.size || c < 0 || c >= m.size {
				continue
			}
			isDark := false
			if dr >= 0 && dr <= 6 && dc >= 0 && dc <= 6 {
				isDark = dr == 0 || dr == 6 || dc == 0 || dc == 6 || (dr >= 2 && dr <= 4 && dc >= 2 && dc <= 4)
			}
			m.setFunction(r, c, isDark)
		}
	}
}

func (m *qrMatrix) placeAlignment(centerRow, centerCol int) {
	for dr := -2; dr <= 2; dr++ {
		for dc := -2; dc <= 2; dc++ {
			absR := dr
			if absR < 0 {
				absR = -absR
			}
			absC := dc
			if absC < 0 {
				absC = -absC
			}
			isDark := absR == 2 || absC == 2 || (dr == 0 && dc == 0)
			m.setFunction(centerRow+dr, centerCol+dc, isDark)
		}
	}
}

func qrFormatBits(mask int) uint32 {
	data := uint32((0x01 << 3) | (mask & 0x7)) // EC Level L = 01
	rem := data << 10
	const gen = uint32(0x537)
	for i := 4; i >= 0; i-- {
		if (rem>>(i+10))&1 != 0 {
			rem ^= gen << i
		}
	}
	return ((data << 10) | rem) ^ 0x5412
}

func qrVersionBits(version int) uint32 {
	rem := uint32(version) << 12
	const gen = uint32(0x1f25)
	for i := 5; i >= 0; i-- {
		if (rem>>(i+12))&1 != 0 {
			rem ^= gen << i
		}
	}
	return (uint32(version) << 12) | rem
}

func (m *qrMatrix) drawFormat(mask int) {
	bits := qrFormatBits(mask)
	bitAt := func(idx int) bool { return (bits>>idx)&1 != 0 }

	for i := 0; i <= 5; i++ {
		m.setFunction(8, i, bitAt(14-i))
	}
	m.setFunction(8, 7, bitAt(8))
	m.setFunction(8, 8, bitAt(7))
	m.setFunction(7, 8, bitAt(6))
	for i := 9; i < 15; i++ {
		m.setFunction(14-i, 8, bitAt(14-i))
	}

	for i := 0; i < 7; i++ {
		m.setFunction(m.size-1-i, 8, bitAt(14-i))
	}
	for i := 0; i < 8; i++ {
		m.setFunction(8, m.size-8+i, bitAt(7-i))
	}
	m.setFunction(m.size-8, 8, true) // Dark module
}

func (m *qrMatrix) drawVersion(version int) {
	if version < 7 {
		return
	}
	bits := qrVersionBits(version)
	for i := 0; i < 18; i++ {
		dark := (bits>>i)&1 != 0
		a := m.size - 11 + (i % 3)
		b := i / 3
		m.setFunction(a, b, dark)
		m.setFunction(b, a, dark)
	}
}

func (m *qrMatrix) drawFunctionPatterns(spec qrVersionSpec) {
	for i := 0; i < m.size; i++ {
		m.setFunction(6, i, i%2 == 0)
		m.setFunction(i, 6, i%2 == 0)
	}
	m.placeFinder(0, 0)
	m.placeFinder(0, m.size-7)
	m.placeFinder(m.size-7, 0)
	for i, r := range spec.alignment {
		for j, c := range spec.alignment {
			if (i == 0 && j == 0) || (i == 0 && j == len(spec.alignment)-1) || (i == len(spec.alignment)-1 && j == 0) {
				continue
			}
			m.placeAlignment(r, c)
		}
	}
	m.drawFormat(0) // Reserve format slots before placing data bits.
	m.drawVersion(spec.version)
}

func (m *qrMatrix) placeCodewords(codewords []byte, remainderBits int) {
	totalBits := len(codewords)*8 + remainderBits
	bitIdx := 0
	upward := true
	for right := m.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for step := 0; step < m.size; step++ {
			r := step
			if upward {
				r = m.size - 1 - step
			}
			for dx := 0; dx < 2; dx++ {
				c := right - dx
				if m.fn[r][c] {
					continue
				}
				dark := false
				if bitIdx < len(codewords)*8 {
					dark = (codewords[bitIdx/8]>>(7-(bitIdx%8)))&1 != 0
				}
				if bitIdx < totalBits {
					bitIdx++
				}
				m.dark[r][c] = dark
			}
		}
		upward = !upward
	}
}

func qrMaskBit(mask, r, c int) bool {
	switch mask {
	case 0:
		return (r+c)%2 == 0
	case 1:
		return r%2 == 0
	case 2:
		return c%3 == 0
	case 3:
		return (r+c)%3 == 0
	case 4:
		return (r/2+c/3)%2 == 0
	case 5:
		return (r*c)%2+(r*c)%3 == 0
	case 6:
		return ((r*c)%2+(r*c)%3)%2 == 0
	default:
		return ((r+c)%2+(r*c)%3)%2 == 0
	}
}

func (m *qrMatrix) applyMask(mask int) *qrMatrix {
	masked := newQRMatrix(m.size)
	for r := 0; r < m.size; r++ {
		copy(masked.fn[r], m.fn[r])
		for c := 0; c < m.size; c++ {
			v := m.dark[r][c]
			if !m.fn[r][c] && qrMaskBit(mask, r, c) {
				v = !v
			}
			masked.dark[r][c] = v
		}
	}
	masked.drawFormat(mask)
	return masked
}

func (m *qrMatrix) penalty() int {
	score := 0
	// Rule 1: runs of >=5 same color in rows and columns.
	for r := 0; r < m.size; r++ {
		runColor := m.dark[r][0]
		runLen := 1
		for c := 1; c < m.size; c++ {
			if m.dark[r][c] == runColor {
				runLen++
			} else {
				if runLen >= 5 {
					score += 3 + (runLen - 5)
				}
				runColor = m.dark[r][c]
				runLen = 1
			}
		}
		if runLen >= 5 {
			score += 3 + (runLen - 5)
		}
	}
	for c := 0; c < m.size; c++ {
		runColor := m.dark[0][c]
		runLen := 1
		for r := 1; r < m.size; r++ {
			if m.dark[r][c] == runColor {
				runLen++
			} else {
				if runLen >= 5 {
					score += 3 + (runLen - 5)
				}
				runColor = m.dark[r][c]
				runLen = 1
			}
		}
		if runLen >= 5 {
			score += 3 + (runLen - 5)
		}
	}
	// Rule 2: 2x2 blocks of same color.
	for r := 0; r < m.size-1; r++ {
		for c := 0; c < m.size-1; c++ {
			d := m.dark[r][c]
			if d == m.dark[r][c+1] && d == m.dark[r+1][c] && d == m.dark[r+1][c+1] {
				score += 3
			}
		}
	}
	// Rule 3: 1:1:3:1:1 finder-like ratios with 4 light modules on either side.
	matchPattern := func(line []bool) int {
		hits := 0
		for i := 0; i <= len(line)-11; i++ {
			isA := line[i] && !line[i+1] && line[i+2] && line[i+3] && line[i+4] && !line[i+5] && line[i+6] && !line[i+7] && !line[i+8] && !line[i+9] && !line[i+10]
			isB := !line[i] && !line[i+1] && !line[i+2] && !line[i+3] && line[i+4] && !line[i+5] && line[i+6] && line[i+7] && line[i+8] && !line[i+9] && line[i+10]
			if isA || isB {
				hits += 40
			}
		}
		return hits
	}
	colBuf := make([]bool, m.size)
	for r := 0; r < m.size; r++ {
		score += matchPattern(m.dark[r])
	}
	for c := 0; c < m.size; c++ {
		for r := 0; r < m.size; r++ {
			colBuf[r] = m.dark[r][c]
		}
		score += matchPattern(colBuf)
	}
	// Rule 4: balance of dark vs total modules.
	darkCount := 0
	for r := 0; r < m.size; r++ {
		for c := 0; c < m.size; c++ {
			if m.dark[r][c] {
				darkCount++
			}
		}
	}
	total := m.size * m.size
	k := (darkCount*100)/total - 50
	if k < 0 {
		k = -k
	}
	score += (k / 5) * 10
	return score
}

func encodeQRMatrix(text string) (*qrMatrix, int, error) {
	payload := []byte(text)
	spec, err := selectQRVersion(len(payload))
	if err != nil {
		return nil, 0, err
	}
	data := encodeQRData(payload, spec)
	codewords := interleaveQRCodewords(data, spec)
	base := newQRMatrix(17 + 4*spec.version)
	base.drawFunctionPatterns(spec)
	base.placeCodewords(codewords, spec.remainderBits)

	bestMask := 0
	best := base.applyMask(0)
	bestScore := best.penalty()
	for mask := 1; mask < 8; mask++ {
		cand := base.applyMask(mask)
		if score := cand.penalty(); score < bestScore {
			bestScore = score
			bestMask = mask
			best = cand
		}
	}
	return best, bestMask, nil
}

// RenderQR encodes text as an ISO/IEC 18004 Level-L QR code and renders it
// using Unicode half-blocks with a 2-module light quiet zone for dark terminals.
func RenderQR(text string) (string, error) {
	m, _, err := encodeQRMatrix(text)
	if err != nil {
		return "", err
	}
	const quiet = 2
	total := m.size + quiet*2
	isLight := func(r, c int) bool {
		if r < 0 || r >= m.size || c < 0 || c >= m.size {
			return true
		}
		return !m.dark[r][c]
	}
	var out strings.Builder
	for r := -quiet; r < m.size+quiet; r += 2 {
		for c := -quiet; c < total-quiet; c++ {
			top := isLight(r, c)
			bot := true
			if r+1 < total-quiet {
				bot = isLight(r+1, c)
			}
			switch {
			case top && bot:
				out.WriteRune('█')
			case top && !bot:
				out.WriteRune('▀')
			case !top && bot:
				out.WriteRune('▄')
			default:
				out.WriteByte(' ')
			}
		}
		out.WriteByte('\n')
	}
	return out.String(), nil
}
