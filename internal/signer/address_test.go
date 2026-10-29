package signer

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestPublicAddressCanonicalRoundTrip(t *testing.T) {
	for _, key := range []string{strings.Repeat("00", 32), strings.Repeat("ff", 32), strings.Repeat("00", 8) + strings.Repeat("42", 24), strings.Repeat("01", 32)} {
		address, err := FormatPublicKey(key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParsePublicAddress(address)
		if err != nil || got != key {
			t.Fatal("public round trip", err)
		}
	}
	for _, bad := range []string{"", strings.Repeat("1", 31), strings.Repeat("1", 33), strings.Repeat("z", 44), strings.Repeat("0", 32), " " + strings.Repeat("1", 32), strings.Repeat("1", 32) + "\n"} {
		if _, err := ParsePublicAddress(bad); err == nil {
			t.Fatal("noncanonical address accepted")
		}
	}
	for _, bad := range []string{"", strings.Repeat("FF", 32), hex.EncodeToString(make([]byte, 64))} {
		if _, err := FormatPublicKey(bad); err == nil {
			t.Fatal("noncanonical key accepted")
		}
	}
}
