package localinstall

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

func encodeTaskXML(s string) []byte {
	s = strings.Replace(s, `encoding="UTF-8"`, `encoding="UTF-16"`, 1)
	words := utf16.Encode([]rune(s))
	b := make([]byte, 2+len(words)*2)
	b[0] = 255
	b[1] = 254
	for i, w := range words {
		binary.LittleEndian.PutUint16(b[2+i*2:], w)
	}
	return b
}
func decodeTaskXML(b []byte) []byte {
	if len(b) > 3 && ((b[0] == 255 && b[1] == 254) || (b[1] == 0 && b[3] == 0)) {
		if b[0] == 255 && b[1] == 254 {
			b = b[2:]
		}
		words := make([]uint16, len(b)/2)
		for i := range words {
			words[i] = binary.LittleEndian.Uint16(b[i*2:])
		}
		b = []byte(string(utf16.Decode(words)))
	}
	// schtasks /Query may emit UTF-8 bytes with a UTF-16 declaration. Normalize
	// only the declaration after decoding, before the strict identity parse.
	s := strings.TrimSpace(strings.TrimPrefix(string(b), "\ufeff"))
	s = strings.Replace(s, `encoding="UTF-16"`, `encoding="UTF-8"`, 1)
	s = strings.Replace(s, `encoding="utf-16"`, `encoding="UTF-8"`, 1)
	return []byte(s)
}
