package core

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestQuantLabHTTPAndMCPIntegration(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = true

	ts, token, csrf := startV2HTTPTestServer(t, s)

	// Unauthenticated request refused cleanly.
	unauth := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/lab/scripts", "", "", "", "", "application/json")
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated /api/v1/lab/scripts, got %d", unauth.StatusCode)
	}

	// List 4 seeded templates cleanly.
	listResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/lab/scripts", "", token, "", "", "application/json")
	if listResp.StatusCode != http.StatusOK {
		listResp.Body.Close()
		t.Fatalf("GET /api/v1/lab/scripts expected 200, got %d", listResp.StatusCode)
	}
	var scriptsBody struct {
		Count int `json:"count"`
	}
	_ = json.NewDecoder(listResp.Body).Decode(&scriptsBody)
	listResp.Body.Close()
	if scriptsBody.Count != 4 {
		t.Fatalf("expected 4 seeded scripts, got %d", scriptsBody.Count)
	}

	// CSRF enforced on PUT /api/v1/lab/scripts and built-in delete refused.
	noCSRF := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/lab/scripts", ts.URL, token, "", `{"id":"x","name":"X","source":"plot(\"p\", close);"}`, "application/json")
	noCSRF.Body.Close()
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 missing CSRF, got %d", noCSRF.StatusCode)
	}

	delBuiltIn := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/lab/scripts/ema-rsi-trend", ts.URL, token, csrf, "", "")
	delBuiltIn.Body.Close()
	if delBuiltIn.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 deleting built-in script, got %d", delBuiltIn.StatusCode)
	}

	// Save custom FasedScript, reject look-ahead shift(close, -1) on evaluate.
	customBody, _ := json.Marshal(map[string]any{
		"id":       "btc-momentum-test",
		"name":     "BTC Momentum Test",
		"category": "trend",
		"source":   "const f = input(\"fast\", 6, 4, 10, 2);\nconst ef = ema(close, f);\nconst es = ema(close, 14);\nplot(\"EF\", ef, \"accent\", \"overlay\");\nstrategy({ long: crossover(ef, es), exitLong: crossunder(ef, es), stopLossPct: 2.5 });",
	})
	putResp := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/lab/scripts", ts.URL, token, csrf, string(customBody), "application/json")
	if putResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(putResp.Body)
		putResp.Body.Close()
		t.Fatalf("PUT custom script expected 200, got %d: %s", putResp.StatusCode, raw)
	}
	putResp.Body.Close()

	badEval := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/lab/evaluate", ts.URL, token, csrf, `{"source":"plot(\"bad\", shift(close, -1));"}`, "application/json")
	badEval.Body.Close()
	if badEval.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on look-ahead shift(close, -1), got %d", badEval.StatusCode)
	}

	// Evaluate indicator -> JSON null-safe plots.
	evalResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/lab/evaluate", ts.URL, token, csrf, `{"scriptId":"btc-momentum-test","venue":"hyperliquid","symbol":"BTC","interval":"1h","limit":90}`, "application/json")
	if evalResp.StatusCode != http.StatusOK {
		evalResp.Body.Close()
		t.Fatalf("POST /api/v1/lab/evaluate expected 200, got %d", evalResp.StatusCode)
	}
	evalRaw, _ := io.ReadAll(evalResp.Body)
	evalResp.Body.Close()
	if !strings.Contains(string(evalRaw), `"plots"`) || !strings.Contains(string(evalRaw), `"barCount":90`) {
		t.Fatalf("unexpected evaluate response: %s", evalRaw)
	}

	// Run causal next-bar-open backtest cleanly.
	btResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/lab/backtest", ts.URL, token, csrf, `{"scriptId":"btc-momentum-test","config":{"venue":"hyperliquid","symbol":"BTC","interval":"1h","limit":200}}`, "application/json")
	if btResp.StatusCode != http.StatusOK {
		btResp.Body.Close()
		t.Fatalf("POST /api/v1/lab/backtest expected 200, got %d", btResp.StatusCode)
	}
	btResp.Body.Close()

	// Run full 4-Gate validation (1000 bar perms + 200 WF perms) -> persists run record.
	valResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/lab/validate", ts.URL, token, csrf, `{"scriptId":"btc-momentum-test","config":{"venue":"hyperliquid","symbol":"BTC","interval":"1h","limit":240,"seed":123}}`, "application/json")
	if valResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(valResp.Body)
		valResp.Body.Close()
		t.Fatalf("POST /api/v1/lab/validate expected 200, got %d: %s", valResp.StatusCode, raw)
	}
	var valRec struct {
		ID     string `json:"id"`
		Report struct {
			ExecutionPolicy string `json:"executionPolicy"`
			Gate2           struct {
				Iterations int `json:"iterations"`
			} `json:"gate2"`
			Gate4 struct {
				Iterations int `json:"iterations"`
			} `json:"gate4"`
		} `json:"report"`
	}
	_ = json.NewDecoder(valResp.Body).Decode(&valRec)
	valResp.Body.Close()
	if valRec.ID == "" || valRec.Report.ExecutionPolicy != "paper_shadow_only" || valRec.Report.Gate2.Iterations != 1000 || valRec.Report.Gate4.Iterations != 200 {
		t.Fatalf("unexpected validation run response: %+v", valRec)
	}

	// GET runs list & single run detail.
	runsList := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/lab/runs?limit=10", "", token, "", "", "application/json")
	if runsList.StatusCode != http.StatusOK {
		runsList.Body.Close()
		t.Fatalf("GET /api/v1/lab/runs expected 200, got %d", runsList.StatusCode)
	}
	runsList.Body.Close()

	runDetail := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/lab/runs/"+valRec.ID, "", token, "", "", "application/json")
	if runDetail.StatusCode != http.StatusOK {
		runDetail.Body.Close()
		t.Fatalf("GET /api/v1/lab/runs/%s expected 200, got %d", valRec.ID, runDetail.StatusCode)
	}
	runDetail.Body.Close()

	// MCP tools & fased://lab/scripts resource work cleanly.
	for _, call := range []string{
		`{"jsonrpc":"2.0","id":60,"method":"tools/call","params":{"name":"list_lab_scripts","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":61,"method":"tools/call","params":{"name":"run_fasedscript_backtest","arguments":{"scriptId":"ema-rsi-trend","venue":"hyperliquid","symbol":"BTC","interval":"1h","limit":120}}}`,
		`{"jsonrpc":"2.0","id":62,"method":"tools/call","params":{"name":"run_four_gate_validation","arguments":{"scriptId":"ema-rsi-trend","venue":"hyperliquid","symbol":"BTC","interval":"1h","limit":180,"permutations":100,"wfPermutations":50}}}`,
		`{"jsonrpc":"2.0","id":63,"method":"resources/read","params":{"uri":"fased://lab/scripts"}}`,
	} {
		resp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/mcp", ts.URL, token, csrf, call, "application/json")
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("MCP call expected 200: %s -> %d", call, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Delete custom script -> 200.
	delResp := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/lab/scripts/btc-momentum-test", ts.URL, token, csrf, "", "")
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE custom script expected 200, got %d", delResp.StatusCode)
	}
}
