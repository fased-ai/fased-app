package update

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func FuzzStrictJSONBounded(f *testing.F) {
	f.Add([]byte(`{"version":"0.1.36","ok":true}`))
	f.Add([]byte(`{"a":1,"a":2}`))
	f.Add([]byte(`{"a\u0062":1,"ab":2}`))
	f.Add([]byte(`{"a":1} {"b":2}`))
	f.Add([]byte(strings.Repeat("[", 25) + strings.Repeat("]", 25)))
	f.Add([]byte{0xff, 0xfe, 0x00})
	f.Add([]byte(strings.Repeat(" ", 2000)))
	f.Fuzz(func(t *testing.T, data []byte) {
		var dst map[string]any
		errSmall := StrictJSONBounded(data, &dst, 1024)
		if errSmall == nil && (len(data) == 0 || len(data) > 1024) {
			t.Fatalf("StrictJSONBounded violated bound for len=%d", len(data))
		}
		var dstAny any
		_ = StrictJSON(data, &dstAny)
	})
}

func FuzzParseDesktopManifest(f *testing.F) {
	valid, _ := json.Marshal(desktopManifestFixture())
	f.Add(valid, "0.1.36")
	f.Add(bytes.Replace(valid, []byte("https://github.com/"), []byte("https://evil.example/"), 1), "0.1.36")
	f.Add([]byte(`{"schemaVersion":1,"version":"0.1.37","assets":{}}`), "0.1.36")
	f.Add([]byte(`{"schemaVersion":1,"version":"0.1.37","version":"0.1.38"}`), "0.1.37")
	f.Add([]byte(`not-json`), "v0.1.36-desktop")
	f.Fuzz(func(t *testing.T, data []byte, current string) {
		m, err := ParseDesktopManifest(data)
		if err == nil {
			if m.SchemaVersion != 1 || m.Version == "" || len(m.Assets) == 0 {
				t.Fatalf("ParseDesktopManifest accepted malformed manifest: %+v", m)
			}
			for name, asset := range m.Assets {
				if name == "" || !strings.HasPrefix(asset.URL, desktopReleaseDownloadBase) {
					t.Fatalf("ParseDesktopManifest admitted untrusted asset %q url=%q", name, asset.URL)
				}
			}
		}
		_, _ = DesktopUpdateAvailable(current, m)
	})
}
