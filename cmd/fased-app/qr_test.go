package main

import (
	"fmt"
	"strings"
	"testing"
)

func gfDivCheckSyndrome(cw []byte, ecLen int) bool {
	return len(rsRemainder(cw[:len(cw)-ecLen], ecLen)) == ecLen && func() bool {
		want := rsRemainder(cw[:len(cw)-ecLen], ecLen)
		got := cw[len(cw)-ecLen:]
		for i := range want {
			if want[i] != got[i] {
				return false
			}
		}
		return true
	}()
}

// decodeQRMatrixForTest reads format info, verifies both redundant format copies match,
// removes the data mask, extracts codewords, checks Reed-Solomon parity, and decodes
// the UTF-8 Byte-mode payload back into the original string.
func decodeQRMatrixForTest(m *qrMatrix) (string, error) {
	// Read Copy 1 format bits.
	var copy1 uint32
	pushBit := func(acc *uint32, r, c int) {
		*acc <<= 1
		if m.dark[r][c] {
			*acc |= 1
		}
	}
	for i := 0; i <= 5; i++ {
		pushBit(&copy1, 8, i)
	}
	pushBit(&copy1, 8, 7)
	pushBit(&copy1, 8, 8)
	pushBit(&copy1, 7, 8)
	for i := 9; i < 15; i++ {
		pushBit(&copy1, 14-i, 8)
	}

	var copy2 uint32
	for i := 0; i < 7; i++ {
		pushBit(&copy2, m.size-1-i, 8)
	}
	for i := 0; i < 8; i++ {
		pushBit(&copy2, 8, m.size-8+i)
	}
	if copy1 != copy2 {
		return "", fmt.Errorf("format copies differ: %015b vs %015b", copy1, copy2)
	}
	unmaskedFmt := copy1 ^ 0x5412
	ecLevel := (unmaskedFmt >> 13) & 0x3
	if ecLevel != 0x01 {
		return "", fmt.Errorf("unexpected EC level: %d", ecLevel)
	}
	mask := int((unmaskedFmt >> 10) & 0x7)

	version := (m.size - 17) / 4
	spec := qrVersionsL[version-1]

	var stream []byte
	var cur byte
	bitCount := 0
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
				bit := m.dark[r][c]
				if qrMaskBit(mask, r, c) {
					bit = !bit
				}
				cur <<= 1
				if bit {
					cur |= 1
				}
				bitCount++
				if bitCount == 8 {
					stream = append(stream, cur)
					cur = 0
					bitCount = 0
					if len(stream) == spec.totalCodewords {
						break
					}
				}
			}
			if len(stream) == spec.totalCodewords {
				break
			}
		}
		if len(stream) == spec.totalCodewords {
			break
		}
		upward = !upward
	}
	if len(stream) != spec.totalCodewords {
		return "", fmt.Errorf("decoded %d codewords, want %d", len(stream), spec.totalCodewords)
	}

	// De-interleave blocks and verify Reed-Solomon parity for every block.
	totalBlocks := spec.group1Blocks + spec.group2Blocks
	dataBlocks := make([][]byte, totalBlocks)
	ecBlocks := make([][]byte, totalBlocks)
	for i := 0; i < spec.group1Blocks; i++ {
		dataBlocks[i] = make([]byte, spec.group1Data)
	}
	for i := 0; i < spec.group2Blocks; i++ {
		dataBlocks[spec.group1Blocks+i] = make([]byte, spec.group2Data)
	}
	for i := range ecBlocks {
		ecBlocks[i] = make([]byte, spec.ecPerBlock)
	}
	idx := 0
	maxData := spec.group1Data
	if spec.group2Data > maxData {
		maxData = spec.group2Data
	}
	for col := 0; col < maxData; col++ {
		for b := 0; b < totalBlocks; b++ {
			if col < len(dataBlocks[b]) {
				dataBlocks[b][col] = stream[idx]
				idx++
			}
		}
	}
	for col := 0; col < spec.ecPerBlock; col++ {
		for b := 0; b < totalBlocks; b++ {
			ecBlocks[b][col] = stream[idx]
			idx++
		}
	}
	var joined []byte
	for b := 0; b < totalBlocks; b++ {
		full := append(append([]byte{}, dataBlocks[b]...), ecBlocks[b]...)
		if !gfDivCheckSyndrome(full, spec.ecPerBlock) {
			return "", fmt.Errorf("RS check failed on block %d", b)
		}
		joined = append(joined, dataBlocks[b]...)
	}

	// Read Byte-mode header from joined data codewords.
	readBits := func(bitPos, count int) int {
		v := 0
		for i := 0; i < count; i++ {
			p := bitPos + i
			v = (v << 1) | int((joined[p/8]>>(7-(p%8)))&1)
		}
		return v
	}
	if mode := readBits(0, 4); mode != 4 {
		return "", fmt.Errorf("mode %d != 4", mode)
	}
	countBits := 8
	if version >= 10 {
		countBits = 16
	}
	charCount := readBits(4, countBits)
	out := make([]byte, charCount)
	pos := 4 + countBits
	for i := 0; i < charCount; i++ {
		out[i] = byte(readBits(pos, 8))
		pos += 8
	}
	return string(out), nil
}

func TestQRSpeclFormatVersionAndRoundTripDecode(t *testing.T) {
	t.Parallel()
	if got := qrFormatBits(0); got != 0x77c4 {
		t.Fatalf("format bits for L/mask0 = %#04x, want 0x77c4", got)
	}
	if got := qrVersionBits(7); got != 0x07c94 {
		t.Fatalf("version 7 BCH bits = %#05x, want 0x07c94", got)
	}

	samples := []string{
		"hello",
		"http://127.0.0.1:7331/?instance=" + strings.Repeat("a", 43) + "#pair=" + strings.Repeat("b", 43),
		"https://fased.example.com/?instance=" + strings.Repeat("x", 43) + "#pair=" + strings.Repeat("y", 43),
		strings.Repeat("Q", 300), // Exercises >= v10 with 16-bit byte count and 4 interleaved blocks.
	}
	for _, sample := range samples {
		m, _, err := encodeQRMatrix(sample)
		if err != nil {
			t.Fatalf("encode %q: %v", sample, err)
		}
		// Dark module at (size-8, 8) and 7x7 top-left finder outer border.
		if !m.dark[m.size-8][8] || !m.dark[0][0] || !m.dark[3][3] || m.dark[1][1] {
			t.Fatal("missing QR finder/dark module pattern")
		}
		decoded, err := decodeQRMatrixForTest(m)
		if err != nil {
			t.Fatalf("decode failed for len=%d: %v", len(sample), err)
		}
		if decoded != sample {
			t.Fatalf("round-trip mismatch:\n got: %q\nwant: %q", decoded, sample)
		}
		rendered, err := RenderQR(sample)
		if err != nil || !strings.Contains(rendered, "█") || !strings.Contains(rendered, "\n") {
			t.Fatalf("render failed: %v", err)
		}
	}

	if _, err := RenderQR(strings.Repeat("z", 400)); err == nil {
		t.Fatal("oversized QR payload accepted")
	}
}
