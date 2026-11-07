package desktop

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fased-ai/fased-app/internal/ownerclient"
)

func TestNativeWalletOwnerReceivesOnlyPublicIntent(t *testing.T) {
	h := NewHandler(t.TempDir(), fstest.MapFS{})
	var opened string
	h.SetWalletOwnerOpener(func(link string) error { opened = link; return nil })
	link, err := ownerclient.Link(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), "devnet", "setup", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		origin, body string
		code         int
	}{
		{"http://wails.localhost", `{"link":"` + link + `"}`, 200},
		{"https://evil.example", `{"link":"` + link + `"}`, 403},
		{"http://wails.localhost", `{"link":"` + link + `","seed":"secret"}`, 400},
		{"http://wails.localhost", `{"link":"` + link + `&command=bad"}`, 400},
	} {
		opened = ""
		r := httptest.NewRequest("POST", "http://wails.localhost/api/v1/installation/wallet-owner", strings.NewReader(test.body))
		r.Header.Set("Origin", test.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.code {
			t.Fatal(w.Code, w.Body.String())
		}
		if test.code != 200 && opened != "" {
			t.Fatal("untrusted intent opened owner window")
		}
		var result map[string]bool
		if test.code == 200 && (json.Unmarshal(w.Body.Bytes(), &result) != nil || !result["opened"]) {
			t.Fatal("window not opened")
		}
	}
}
