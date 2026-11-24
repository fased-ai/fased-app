package appicon

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestAppIconPNGAndICNS(t *testing.T) {
	t.Parallel()
	if len(PNG) < 24 || !bytes.Equal(PNG[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		t.Fatalf("embedded PNG missing valid signature (len=%d)", len(PNG))
	}
	icns := ICNS()
	if len(icns) != 16+len(PNG) {
		t.Fatalf("unexpected ICNS length: got %d want %d", len(icns), 16+len(PNG))
	}
	if string(icns[0:4]) != "icns" || string(icns[8:12]) != "ic08" {
		t.Fatalf("invalid ICNS headers: %q / %q", icns[0:4], icns[8:12])
	}
	if got := binary.BigEndian.Uint32(icns[4:8]); int(got) != len(icns) {
		t.Fatalf("ICNS total length mismatch: got %d want %d", got, len(icns))
	}
	if got := binary.BigEndian.Uint32(icns[12:16]); int(got) != 8+len(PNG) {
		t.Fatalf("ICNS chunk length mismatch: got %d want %d", got, 8+len(PNG))
	}
	if !bytes.Equal(icns[16:], PNG) {
		t.Fatal("ICNS payload does not match PNG bytes")
	}
}
