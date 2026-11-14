package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func financialTestContext() financialObject {
	return financialObject{"schema": "wen-mining-read-context/2", "execution": "disabled", "source": "local-rpc-fixture", "revision": strings.Repeat("a", 64), "validation": financialValidation, "identity": financialObject{"genesis": "fixture-network", "binding": financialObject{"program": strings.Repeat("1", 64), "ledger": strings.Repeat("2", 64), "network": strings.Repeat("3", 64), "economy": strings.Repeat("4", 64), "terms": strings.Repeat("5", 64)}, "assetMint": strings.Repeat("6", 64), "runtimeAddress": strings.Repeat("7", 64), "expectedProgram": financialObject{"hash": strings.Repeat("8", 64)}}}
}
func financialTestConfig(t *testing.T, s *Server, origin string, c financialObject) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "financial.json")
	b, _ := json.Marshal(financialObject{"schema": "fased-wen-financial-config/1", "origin": origin, "context": c})
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func financialTestEnvelope(c financialObject) financialObject {
	return financialObject{"execution": "disabled", "identity": c["identity"], "source": c["source"], "validation": financialValidation, "trust": "pinned-finalized-rpc-not-state-proof", "observedAtMs": json.Number(strconv.FormatInt(time.Now().UnixMilli(), 10)), "expiresAtMs": json.Number(strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10))}
}
func financialTestRound(c financialObject, wallet string) financialObject {
	r := financialTestEnvelope(c)
	r["schema"] = "wen-mining-deployment-read/2"
	r["slot"] = "100"
	r["coverage"] = financialObject{"kind": "settled-round", "complete": true, "walletHistoryComplete": false, "wallet": wallet, "roundSlot": "42", "includedFamilies": []any{}}
	return r
}
func TestWENFinancialConfiguration(t *testing.T) {
	h := &HTTPHandler{}
	s := &Server{HTTP: &http.Server{Handler: h}}
	c := financialTestContext()
	if e := s.ConfigureWENFinancial(""); e != nil || h.wenFinancial != nil {
		t.Fatal("default enabled")
	}
	for _, origin := range []string{"https://127.0.0.1:10", "http://localhost:10", "http://127.0.0.1:10/", "http://127.0.0.1:10?", "http://127.0.0.1:010", "http://u@127.0.0.1:10"} {
		if s.ConfigureWENFinancial(financialTestConfig(t, s, origin, c)) == nil {
			t.Fatal("accepted", origin)
		}
	}
	p := financialTestConfig(t, s, "http://127.0.0.1:1234", c)
	if e := os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
	if s.ConfigureWENFinancial(p) == nil {
		t.Fatal("broad config")
	}
	os.Chmod(p, 0600)
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(p, link)
	if s.ConfigureWENFinancial(link) == nil {
		t.Fatal("symlink")
	}
	before, _ := os.ReadFile(p)
	if e := s.ConfigureWENFinancial(p); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(p)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("configuration rewritten")
	}
	if s.ConfigureWENFinancial(p) == nil {
		t.Fatal("rebind allowed")
	}
}
func TestWENFinancialBoundaryRecovery(t *testing.T) {
	store := fixtureStore(t)
	token := claimFixture(t, store)
	h, e := NewHTTPHandler(store, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	s := &Server{HTTP: &http.Server{Handler: h}}
	c := financialTestContext()
	wallet := strings.Repeat("9", 64)
	input := `{"wallet":"` + wallet + `","roundSlot":"42"}`
	calls := 0
	mode := "good"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-CSRF-Token") != "" {
			t.Error("forwarded authority")
		}
		if mode == "disconnect" {
			w.WriteHeader(503)
			return
		}
		if mode == "redirect" {
			http.Redirect(w, r, "http://127.0.0.1:1", 302)
			return
		}
		if mode == "oversized" {
			w.Write([]byte(strings.Repeat("x", WENFinancialResponseLimit+1)))
			return
		}
		if r.URL.Path == "/__wen/mining-read/context" {
			cc := financialTestContext()
			if mode == "context" {
				cc["revision"] = strings.Repeat("b", 64)
			}
			json.NewEncoder(w).Encode(cc)
			return
		}
		out := financialTestRound(c, wallet)
		switch mode {
		case "stale":
			out["expiresAtMs"] = json.Number("1")
		case "wrong-wallet":
			financialMap(out["coverage"])["wallet"] = strings.Repeat("8", 64)
		case "regress":
			out["slot"] = "99"
		case "marker":
			delete(out, "validation")
		case "malformed":
			w.Write([]byte("{"))
			return
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer up.Close()
	request := func(method, action, body, auth, csrf string) int {
		return apiRequest(h, method, "/api/v1/wen/financial/"+action, body, auth, "http://127.0.0.1:7331", csrf).Code
	}
	if code := request("GET", "context", "", token, ""); code != 503 {
		t.Fatal(code)
	}
	if e = s.ConfigureWENFinancial(financialTestConfig(t, s, up.URL, c)); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		method, action, body, auth, csrf string
		code                             int
	}{{"GET", "context", "", "", "", 401}, {"POST", "read", input, token, "", 403}, {"POST", "read", `{"wallet":"` + wallet + `","roundSlot":"42","url":"http://x"}`, token, csrfToken(token), 400}, {"POST", "read", `{"wallet":"a","wallet":"b","roundSlot":"42"}`, token, csrfToken(token), 400}, {"GET", "read", "", token, "", 405}, {"POST", "receipt", `{"wallet":"` + wallet + `","roundSlot":"42","signature":"` + strings.Repeat("1", 88) + `","operation":"Submit","entry":0}`, token, csrfToken(token), 400}} {
		if got := request(tc.method, tc.action, tc.body, tc.auth, tc.csrf); got != tc.code {
			t.Fatalf("%s: %d != %d", tc.action, got, tc.code)
		}
	}
	if calls != 0 {
		t.Fatal("rejected input forwarded")
	}
	if got := request("POST", "read", input, token, csrfToken(token)); got != 200 {
		t.Fatal("valid", got)
	}
	for _, m := range []string{"stale", "wrong-wallet", "regress", "marker", "malformed", "context", "disconnect", "redirect", "oversized"} {
		mode = m
		if got := request("POST", "read", input, token, csrfToken(token)); got != 503 {
			t.Fatalf("%s %d", m, got)
		}
	}
	mode = "good"
	if got := request("POST", "read", input, token, csrfToken(token)); got != 200 {
		t.Fatal("recovery", got)
	}
}
func TestWENFinancialOriginalAndHistoryGuards(t *testing.T) {
	c := financialTestContext()
	b := &wenFinancialBridge{context: c, floors: map[string]uint64{}, originals: map[string]string{}}
	input := financialObject{"wallet": strings.Repeat("9", 64), "roundSlot": "42", "signature": strings.Repeat("1", 88), "operation": "ClaimSol", "entry": json.Number("0")}
	original := func() financialObject {
		r := financialTestEnvelope(c)
		r["schema"] = "wen-mining-wallet-receipt/2"
		for k, v := range input {
			r[k] = v
		}
		r["status"] = "finalized-success"
		r["slot"] = "90"
		r["currentAccountSlot"] = "100"
		r["originalTransactionVerified"] = true
		r["currentAccountStateVerified"] = true
		r["feeVerified"] = true
		r["receiptProofVerified"] = true
		r["messageSha256"] = strings.Repeat("a", 64)
		r["paid"] = true
		r["amounts"] = financialObject{"principalLamports": "10"}
		return r
	}
	if e := b.accept("receipt", input, original()); e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"status", "messageSha256", "amounts", "networkFeeLamports", "payer", "effects"} {
		r := original()
		switch key {
		case "status":
			r[key] = "unknown"
		case "amounts":
			r[key] = financialObject{"principalLamports": "11"}
		default:
			r[key] = "changed"
		}
		if b.accept("receipt", input, r) == nil {
			t.Fatal("accepted changed original", key)
		}
	}
	r := original()
	r["currentAccountSlot"] = "101"
	r["cumulativeBtcPaidAtoms"] = "500"
	if e := b.accept("receipt", input, r); e != nil {
		t.Fatal("fresh original", e)
	}
	if b.accept("receipt", input, original()) == nil {
		t.Fatal("regressed proof snapshot")
	}
	sel := financialObject{"wallet": input["wallet"], "roundSlot": "42"}
	if b.accept("read", sel, financialTestRound(c, financialString(input["wallet"]))) == nil {
		t.Fatal("read regressed behind receipt")
	}
	snapshot := financialTestRound(c, financialString(input["wallet"]))
	snapshot["slot"] = "102"
	if e := b.accept("read", sel, snapshot); e != nil {
		t.Fatal(e)
	}
	if b.accept("receipt", input, r) == nil {
		t.Fatal("receipt regressed behind read")
	}
	hist := financialTestEnvelope(c)
	hist["schema"] = "wen-mining-history-read/2"
	hist["wallet"] = input["wallet"]
	hist["includeBtc"] = false
	hist["coverage"] = financialObject{"kind": "indexed-rounds", "atomic": false, "indexComplete": false, "walletHistoryComplete": false}
	hist["items"] = []any{financialObject{"roundSlot": "42", "status": "gap"}}
	if b.accept("history", financialObject{"wallet": input["wallet"], "includeBtc": false, "cursor": nil}, hist) == nil {
		t.Fatal("known round became gap")
	}
}
func TestWENFinancialClientResponseBound(t *testing.T) {
	oversized := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		size := 40000
		if oversized {
			size = WENFinancialResponseLimit
		}
		json.NewEncoder(w).Encode(map[string]string{"data": strings.Repeat("x", size)})
	}))
	defer up.Close()
	c := &Client{Endpoint: Endpoint{URL: up.URL}, HTTP: up.Client()}
	var body json.RawMessage
	if e := c.Do(t.Context(), "GET", "/api/v1/wen/financial/context", nil, &body); e != nil || len(body) < 40000 {
		t.Fatal("financial client retained old 32K cap", e)
	}
	oversized = true
	if c.Do(t.Context(), "GET", "/api/v1/wen/financial/context", nil, &body) == nil {
		t.Fatal("unbounded client")
	}
}
