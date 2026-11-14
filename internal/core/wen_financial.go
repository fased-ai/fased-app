package core

// This bridge trusts an owner-pinned local WEN validator, not arbitrary RPC JSON.
// It performs no financial calculation, signing, storage import or execution.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const WENFinancialResponseLimit = 2 * 1024 * 1024
const financialValidation = "wen-mining-read-client/2"
const financialGuardLimit = 4096

var financialHex = regexp.MustCompile(`^[0-9a-f]{64}$`)
var financialSignature = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{80,90}$`)
var financialDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

type financialObject = map[string]any
type wenFinancialBridge struct {
	mu        sync.Mutex
	origin    string
	context   financialObject
	floors    map[string]uint64
	originals map[string]string
}

func financialJSON(data []byte) (financialObject, error) {
	var value financialObject
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if e := d.Decode(&value); e != nil || value == nil {
		return nil, errors.New("invalid financial JSON")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, errors.New("trailing financial JSON")
	}
	// Reject duplicate object members as well: independent consumers must see the same input.
	if e := financialUnique(json.NewDecoder(bytes.NewReader(data))); e != nil {
		return nil, e
	}
	return value, nil
}
func financialUnique(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	if delim == '{' {
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return errors.New("duplicate financial member")
			}
			seen[s] = true
			if e = financialUnique(d); e != nil {
				return e
			}
		}
	} else if delim == '[' {
		for d.More() {
			if e = financialUnique(d); e != nil {
				return e
			}
		}
	} else {
		return errors.New("invalid delimiter")
	}
	_, e = d.Token()
	return e
}
func financialString(v any) string       { s, _ := v.(string); return s }
func financialMap(v any) financialObject { m, _ := v.(map[string]any); return m }
func financialUint(v any) (uint64, bool) {
	s, ok := v.(string)
	if !ok || !financialDecimal.MatchString(s) {
		return 0, false
	}
	n, e := strconv.ParseUint(s, 10, 64)
	return n, e == nil
}
func financialInt(v any) (int64, bool) {
	s, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	n, e := strconv.ParseInt(string(s), 10, 64)
	return n, e == nil && n >= 0 && n <= 9007199254740991
}
func financialCanonical(v any) string { b, _ := json.Marshal(v); return string(b) }
func financialFields(v financialObject, required, optional []string) bool {
	allowed := map[string]bool{}
	for _, k := range required {
		if _, ok := v[k]; !ok {
			return false
		}
		allowed[k] = true
	}
	for _, k := range optional {
		allowed[k] = true
	}
	for k := range v {
		if !allowed[k] {
			return false
		}
	}
	return true
}

// ConfigureWENFinancial must run before Serve. The config contains public pins only.
func (s *Server) ConfigureWENFinancial(path string) error {
	if path == "" {
		return nil
	}
	h, ok := s.HTTP.Handler.(*HTTPHandler)
	if !ok || h.wenFinancial != nil {
		return errors.New("financial handler unavailable or already configured")
	}
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() > 65536 {
		return errors.New("financial configuration must be a bounded regular owner file")
	}
	// Windows protectPath mutates ACLs; this read-only opt-in must never do that.
	if runtime.GOOS == "windows" {
		return errors.New("financial config owner verification is not yet supported on Windows")
	}
	if e = protectPath(path, info); e != nil {
		return e
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	actual, e := f.Stat()
	if e != nil || !os.SameFile(info, actual) {
		return errors.New("financial config changed")
	}
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(b) > 65536 {
		return errors.New("financial config limit")
	}
	config, e := financialJSON(b)
	if e != nil {
		return e
	}
	if !financialFields(config, []string{"schema", "origin", "context"}, nil) || config["schema"] != "fased-wen-financial-config/1" {
		return errors.New("invalid financial config")
	}
	origin := financialString(config["origin"])
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" {
		return errors.New("financial origin must be exact loopback HTTP")
	}
	p, e := strconv.Atoi(u.Port())
	if e != nil || p < 1 || p > 65535 || origin != "http://127.0.0.1:"+strconv.Itoa(p) {
		return errors.New("invalid financial origin port")
	}
	c := financialMap(config["context"])
	id := financialMap(c["identity"])
	binding := financialMap(id["binding"])
	if !financialFields(c, []string{"schema", "execution", "source", "identity", "revision", "validation"}, nil) || c["schema"] != "wen-mining-read-context/2" || c["execution"] != "disabled" || c["validation"] != financialValidation || (c["source"] != "rpc-verified" && c["source"] != "local-rpc-fixture") || !financialHex.MatchString(financialString(c["revision"])) {
		return errors.New("invalid pinned financial context")
	}
	for _, k := range []string{"program", "ledger", "network", "economy", "terms"} {
		if !financialHex.MatchString(financialString(binding[k])) {
			return errors.New("missing financial binding")
		}
	}
	for _, k := range []string{"assetMint", "runtimeAddress"} {
		if !financialHex.MatchString(financialString(id[k])) {
			return errors.New("missing financial identity")
		}
	}
	if len(financialMap(id["expectedProgram"])) == 0 || financialString(id["genesis"]) == "" {
		return errors.New("missing program and network pins")
	}
	h.wenFinancial = &wenFinancialBridge{origin: origin, context: c, floors: map[string]uint64{}, originals: map[string]string{}}
	return nil
}

var financialFamilies = []string{"includeBtc", "includeStaking", "includeCarry", "includeDaily", "includeTreasury", "includeOperating"}

func financialSelection(action string, v financialObject) bool {
	req := []string{}
	optional := []string{}
	switch action {
	case "read":
		req = []string{"wallet", "roundSlot"}
		optional = financialFamilies
	case "history":
		req = []string{"wallet", "includeBtc", "cursor"}
		optional = financialFamilies[1:]
	case "receipt":
		req = []string{"wallet", "roundSlot", "signature", "operation", "entry"}
	case "operating-receipt":
		req = []string{"roundSlot", "signature", "operation"}
		if v["operation"] != "collect" {
			return false
		}
	case "treasury-receipt":
		req = []string{"roundSlot", "signature", "operation"}
		switch v["operation"] {
		case "convert-input":
			req = append(req, "side", "nonce")
			n, ok := financialInt(v["side"])
			if !ok || n > 1 {
				return false
			}
			if _, ok = financialUint(v["nonce"]); !ok {
				return false
			}
		case "execute-buyback", "execute-liquidity", "expire-order":
			req = append(req, "day", "hour", "attempt")
			if _, ok := financialUint(v["day"]); !ok {
				return false
			}
			h, ok := financialInt(v["hour"])
			a, aok := financialInt(v["attempt"])
			if !ok || !aok || h < 1 || h > 23 || a < 1 || a > 3 {
				return false
			}
		default:
			return false
		}
	default:
		return false
	}
	if !financialFields(v, req, optional) {
		return false
	}
	if action == "read" || action == "history" || action == "receipt" {
		wallet := financialString(v["wallet"])
		if !financialHex.MatchString(wallet) || strings.Trim(wallet, "0") == "" {
			return false
		}
	}
	if action != "history" {
		if _, ok := financialUint(v["roundSlot"]); !ok {
			return false
		}
	}
	if action == "read" || action == "history" {
		for _, k := range financialFamilies {
			if val, ok := v[k]; ok {
				if _, ok = val.(bool); !ok {
					return false
				}
			}
		}
	}
	if action == "history" {
		if c := v["cursor"]; c != nil {
			str, ok := c.(string)
			if !ok || len(str) == 0 || len(str) > 1024 || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(str) {
				return false
			}
		}
	}
	if strings.Contains(action, "receipt") {
		if !financialSignature.MatchString(financialString(v["signature"])) {
			return false
		}
	}
	if action == "receipt" {
		if _, ok := financialInt(v["entry"]); !ok {
			return false
		}
		switch v["operation"] {
		case "ClaimSol", "ClaimSat", "ClaimBtc", "RecoverSol", "StakingClaimBtc", "StakingRecoverSol", "CarryClaimBtc", "CarryRefundBtc", "CarryRefundSol", "DailyPrizeBtc", "DailyPrizeSol", "DailyRefundBtc", "DailyRefundSol":
		default:
			return false
		}
	}
	return true
}
func (h *HTTPHandler) wenFinancialAPI(w http.ResponseWriter, r *http.Request, token, action string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		writeError(w, 400, "financial query not accepted")
		return
	}
	var input financialObject
	var body []byte
	if action == "context" {
		if r.Method != "GET" {
			writeError(w, 405, "GET required")
			return
		}
	} else {
		if r.Method != "POST" {
			writeError(w, 405, "POST required")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var e error
		body, e = io.ReadAll(io.LimitReader(r.Body, 4097))
		if e != nil || len(body) > 4096 {
			writeError(w, 400, "financial input limit")
			return
		}
		input, e = financialJSON(body)
		if e != nil || !financialSelection(action, input) {
			writeError(w, 400, "invalid financial selection")
			return
		}
	}
	bridge := h.wenFinancial
	if bridge == nil {
		writeError(w, 503, "financial reads not configured")
		return
	}
	// One inspection owns guard updates; queued work is refused, never retried.
	if !bridge.mu.TryLock() {
		writeError(w, 503, "financial inspection busy")
		return
	}
	defer bridge.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("financial redirect refused") }}
	fetch := func(path string, data []byte) ([]byte, financialObject, error) {
		method := "GET"
		if data != nil {
			method = "POST"
		}
		q, e := http.NewRequestWithContext(ctx, method, bridge.origin+"/__wen/mining-read"+path, bytes.NewReader(data))
		if e != nil {
			return nil, nil, e
		}
		if data != nil {
			q.Header.Set("Content-Type", "application/json")
		}
		res, e := client.Do(q)
		if e != nil {
			return nil, nil, e
		}
		defer res.Body.Close()
		b, e := io.ReadAll(io.LimitReader(res.Body, WENFinancialResponseLimit+1))
		if e != nil || len(b) > WENFinancialResponseLimit || res.StatusCode != 200 {
			return nil, nil, errors.New("financial service unavailable")
		}
		obj, e := financialJSON(b)
		return b, obj, e
	}
	raw, c, e := fetch("/context", nil)
	if e != nil || !reflect.DeepEqual(c, bridge.context) {
		writeError(w, 503, "financial context changed or unavailable")
		return
	}
	if action != "context" {
		path := "/" + action
		if action == "read" {
			path = ""
		}
		var response financialObject
		raw, response, e = fetch(path, body)
		if e == nil {
			e = bridge.accept(action, input, response)
		}
		if e != nil {
			writeError(w, 503, "financial read unavailable, stale or regressed")
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

func (b *wenFinancialBridge) accept(action string, input, r financialObject) error {
	floors := make(map[string]uint64, len(b.floors))
	for k, v := range b.floors {
		floors[k] = v
	}
	originals := make(map[string]string, len(b.originals))
	for k, v := range b.originals {
		originals[k] = v
	}
	if e := b.validate(action, input, r, floors, originals); e != nil {
		return e
	}
	if len(floors) > financialGuardLimit || len(originals) > financialGuardLimit {
		return errors.New("financial inspection capacity reached; restart to reverify")
	}
	b.floors = floors
	b.originals = originals
	return nil
}
func (b *wenFinancialBridge) validate(action string, input, r financialObject, floors map[string]uint64, originals map[string]string) error {
	bad := func() error { return errors.New("invalid financial envelope or scope") }
	// The marker attests the pinned local WEN canonical validator ran. It is not an RPC state proof.
	if r["validation"] != financialValidation || r["execution"] != "disabled" || r["source"] != b.context["source"] || !reflect.DeepEqual(r["identity"], b.context["identity"]) || r["trust"] != "pinned-finalized-rpc-not-state-proof" {
		return bad()
	}
	observed, ok := financialInt(r["observedAtMs"])
	expires, eok := financialInt(r["expiresAtMs"])
	now := time.Now().UnixMilli()
	if !ok || !eok || observed > now+10000 || expires <= now || expires <= observed || expires-observed > 120000 {
		return bad()
	}
	schema := map[string]string{"read": "wen-mining-deployment-read/2", "history": "wen-mining-history-read/2", "receipt": "wen-mining-wallet-receipt/2", "treasury-receipt": "wen-mining-treasury-receipt/2", "operating-receipt": "wen-mining-operating-receipt/2"}[action]
	if r["schema"] != schema {
		return bad()
	}
	floor := func(key string, value any) bool {
		n, valid := financialUint(value)
		if !valid || n < floors[key] {
			return false
		}
		floors[key] = n
		return true
	}
	roundKey := financialString(input["wallet"]) + "/" + financialString(input["roundSlot"])
	if action == "read" {
		c := financialMap(r["coverage"])
		if c["wallet"] != input["wallet"] || c["roundSlot"] != input["roundSlot"] || c["complete"] != true || c["kind"] != "settled-round" || c["walletHistoryComplete"] != false || !floor("round/"+roundKey, r["slot"]) || !floor("state/"+financialString(input["roundSlot"]), r["slot"]) {
			return bad()
		}
		families, _ := c["includedFamilies"].([]any)
		names := []string{"mining-payout", "staking-rewards", "carry", "daily", "mining-treasury", "operating"}
		fields := []string{"btc", "staking", "carry", "daily", "treasury", "operating"}
		for i, k := range financialFamilies {
			has := false
			for _, f := range families {
				if f == names[i] {
					has = true
				}
			}
			wanted := input[k] == true
			if has != wanted || (r[fields[i]] != nil) != wanted {
				return bad()
			}
			if wanted && (i == 4 || i == 5) && !floor("shared/"+fields[i]+"/"+financialString(input["roundSlot"]), r["slot"]) {
				return bad()
			}
		}
	} else if action == "history" {
		if r["wallet"] != input["wallet"] || r["includeBtc"] != input["includeBtc"] {
			return bad()
		}
		for _, k := range financialFamilies[1:] {
			if (input[k] == true) != (r[k] == true) {
				return bad()
			}
		}
		items, ok := r["items"].([]any)
		if !ok || len(items) > 2 {
			return bad()
		}
		c := financialMap(r["coverage"])
		if c["kind"] != "indexed-rounds" || c["atomic"] != false || c["indexComplete"] != false || c["walletHistoryComplete"] != false {
			return bad()
		}
		for _, item := range items {
			i := financialMap(item)
			slot := i["roundSlot"]
			if _, ok := financialUint(slot); !ok {
				return bad()
			}
			selection := financialObject{"wallet": input["wallet"], "roundSlot": slot}
			for _, k := range financialFamilies {
				selection[k] = input[k]
			}
			if i["status"] == "verified" {
				snapshot := financialMap(i["snapshot"])
				if snapshot == nil {
					return bad()
				}
				// The enclosing canonical marker covers every nested snapshot.
				snapshot["validation"] = financialValidation
				if e := b.validate("read", selection, snapshot, floors, originals); e != nil {
					return e
				}
			} else if i["status"] != "gap" {
				return bad()
			} else if _, seen := floors["round/"+financialString(input["wallet"])+"/"+financialString(slot)]; seen {
				return bad()
			}
		}
	} else {
		for k, v := range input {
			if !reflect.DeepEqual(r[k], v) {
				return bad()
			}
		}
		key := action + "/" + financialCanonical(input)
		status := financialString(r["status"])
		final := status == "finalized-success" || status == "finalized-failed" || (action == "receipt" && status == "finalized-no-payment")
		if !final {
			if status != "pending" && status != "unknown" {
				return bad()
			}
			if originals[key] != "" {
				return errors.New("finalized original regressed")
			}
			return nil
		}
		if r["originalTransactionVerified"] != true || r["currentAccountStateVerified"] != true || r["feeVerified"] != true || !financialHex.MatchString(financialString(r["messageSha256"])) {
			return bad()
		}
		tx, tok := financialUint(r["slot"])
		current, cok := financialUint(r["currentAccountSlot"])
		if !tok || !cok || tx > current || !floor("receipt/"+key, r["currentAccountSlot"]) || !floor("state/"+financialString(input["roundSlot"]), r["currentAccountSlot"]) {
			return bad()
		}
		if action == "receipt" && !floor("round/"+roundKey, r["currentAccountSlot"]) {
			return bad()
		}
		stable := financialObject{}
		for _, k := range []string{"status", "slot", "messageSha256", "paid", "disposition", "amounts", "effects", "payer", "networkFeeLamports", "btcMint", "originalTransactionVerified", "feeVerified", "receiptProofVerified"} {
			if v, ok := r[k]; ok {
				stable[k] = v
			}
		}
		digest := sha256.Sum256([]byte(financialCanonical(stable)))
		fingerprint := hex.EncodeToString(digest[:])
		if old := originals[key]; old != "" && old != fingerprint {
			return errors.New("finalized original changed")
		}
		originals[key] = fingerprint
	}
	return nil
}
