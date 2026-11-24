package appicon

import (
	_ "embed"
	"encoding/binary"
)

//go:embed appicon.png
var PNG []byte

// ICNS wraps the 256x256 PNG icon in a standard Apple .icns (ic08) container
// supported by macOS 10.7+ without requiring external icon conversion tools.
func ICNS() []byte {
	const headerSize = 8
	chunkLen := headerSize + len(PNG)
	totalLen := headerSize + chunkLen
	out := make([]byte, totalLen)
	copy(out[0:4], "icns")
	binary.BigEndian.PutUint32(out[4:8], uint32(totalLen))
	copy(out[8:12], "ic08")
	binary.BigEndian.PutUint32(out[12:16], uint32(chunkLen))
	copy(out[16:], PNG)
	return out
}
