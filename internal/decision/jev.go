package decision

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

const (
	integrationsDirName = "integrations"
	keyFileName         = "integrations.key"
	decisionDBName      = "decision.sqlite"
	secretAAD           = "fased-decision-jev/v1"
	maxResponseBytes    = 1 << 20 // 1 MiB.
)

var cgnatPrefix = mustParseCIDR("100.64.0.0/10")

func mustParseCIDR(raw string) *net.IPNet {
	_, parsed, err := net.ParseCIDR(raw)
	if err != nil {
		panic(err)
	}
	return parsed
}

// Config parameterizes the System-1 decision Service.
type Config struct {
	DataDir       string
	FixtureMode   bool
	Now           func() time.Time
	HTTPTransport http.RoundTripper // Tests only.
}

// Service manages encrypted Jev API configuration and typed evaluation with
// automatic deterministic fallback when offline or unconfigured.
type Service struct {
	dir         string
	key         []byte
	db          *sql.DB
	httpClient  *http.Client
	fixtureMode bool
	now         func() time.Time
	mu          sync.Mutex
	closed      bool
}

func sqliteFileURI(path string) string {
	slashPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	escaped := strings.ReplaceAll(slashPath, "?", "%3F")
	escaped = strings.ReplaceAll(escaped, "#", "%23")
	return "file://" + escaped
}

func loadOrCreateKey(dir string) ([]byte, error) {
	keyPath := filepath.Join(dir, keyFileName)
	if raw, err := os.ReadFile(keyPath); err == nil {
		if len(raw) != 32 {
			return nil, errors.New("corrupted integrations key length")
		}
		return raw, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".integrations.key.*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(key); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	linkErr := os.Link(tmpPath, keyPath)
	if linkErr == nil {
		return key, nil
	}
	if os.IsExist(linkErr) {
		raw, readErr := os.ReadFile(keyPath)
		if readErr != nil || len(raw) != 32 {
			return nil, errors.New("failed to read initialized integrations key")
		}
		return raw, nil
	}

	// Fallback for filesystems that do not support hardlinks.
	f, err := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		_, writeErr := f.Write(key)
		closeErr := f.Close()
		if writeErr != nil {
			_ = os.Remove(keyPath)
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return key, nil
	}
	if os.IsExist(err) {
		for attempt := 0; attempt < 5; attempt++ {
			raw, readErr := os.ReadFile(keyPath)
			if readErr == nil && len(raw) == 32 {
				return raw, nil
			}
			time.Sleep(10 * time.Millisecond)
		}
		return nil, errors.New("failed to read initialized integrations key")
	}
	return nil, err
}

func sealSecret(key []byte, plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), []byte(secretAAD))
	out := make([]byte, len(nonce)+len(sealed))
	copy(out, nonce)
	copy(out[len(nonce):], sealed)
	return out, nil
}

func openSecret(key, blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(blob) < gcm.NonceSize() {
		return "", errors.New("corrupted sealed credential")
	}
	pt, err := gcm.Open(nil, blob[:gcm.NonceSize()], blob[gcm.NonceSize():], []byte(secretAAD))
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

func isPublicUnicastIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		if cgnatPrefix.Contains(ip) || (ip[0] == 198 && (ip[1] == 18 || ip[1] == 19)) {
			return false
		}
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return false
	}
	return ip.IsGlobalUnicast()
}

func dialPublicAddress(ctx context.Context, dialer *net.Dialer, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicUnicastIP(ip) {
			return nil, fmt.Errorf("blocked non-public IP %s", ip)
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, r := range resolved {
		if isPublicUnicastIP(r.IP) {
			if conn, dErr := dialer.DialContext(ctx, network, net.JoinHostPort(r.IP.String(), port)); dErr == nil {
				return conn, nil
			}
		}
	}
	return nil, fmt.Errorf("no public unicast IP available for %q", host)
}

// NewService initializes isolated encrypted Jev decision storage under <dataDir>/integrations/.
func NewService(cfg Config) (*Service, error) {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, errors.New("decision service requires non-empty DataDir")
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	dir := filepath.Join(cfg.DataDir, integrationsDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create integrations dir: %w", err)
	}
	key, err := loadOrCreateKey(dir)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, decisionDBName)
	dsn := sqliteFileURI(dbPath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	const ddl = `
CREATE TABLE IF NOT EXISTS jev_config (
	id INTEGER PRIMARY KEY CHECK(id=1),
	model TEXT NOT NULL,
	key_ciphertext BLOB NOT NULL,
	updated_at INTEGER NOT NULL
);`
	if _, err := db.Exec(ddl); err != nil {
		_ = db.Close()
		return nil, err
	}

	transport := cfg.HTTPTransport
	if transport == nil {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialPublicAddress(ctx, dialer, network, addr)
			},
			ForceAttemptHTTP2: true,
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS13},
		}
	}

	return &Service{
		dir: dir,
		key: key,
		db:  db,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		fixtureMode: cfg.FixtureMode,
		now:         nowFn,
	}, nil
}

// Close closes SQLite handles and clears cached key material cleanly.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for i := range s.key {
		s.key[i] = 0
	}
	if s.db != nil {
		err := s.db.Close()
		s.db = nil
		return err
	}
	return nil
}

func (s *Service) loadConfig(ctx context.Context) (model string, cipherBlob []byte, err error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return "", nil, errors.New("decision service closed")
	}
	err = s.db.QueryRowContext(ctx, `SELECT model, key_ciphertext FROM jev_config WHERE id = 1`).Scan(&model, &cipherBlob)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultModel, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}
	return model, cipherBlob, nil
}

// Status reports whether a Jev API key is configured without returning secrets.
func (s *Service) Status(ctx context.Context) (Status, error) {
	model, blob, err := s.loadConfig(ctx)
	if err != nil {
		return Status{}, err
	}
	return Status{
		Configured:   len(blob) > 0,
		Model:        model,
		Endpoint:     DefaultEndpoint,
		FallbackMode: "deterministic",
	}, nil
}

// SaveConfig validates and stores an encrypted Jev key and/or model override.
func (s *Service) SaveConfig(ctx context.Context, upd ConfigUpdate) (Status, error) {
	curModel, curBlob, err := s.loadConfig(ctx)
	if err != nil {
		return Status{}, err
	}
	nextModel := curModel
	if strings.TrimSpace(upd.Model) != "" {
		validated, mErr := ValidateModel(upd.Model)
		if mErr != nil {
			return Status{}, mErr
		}
		nextModel = validated
	}
	nextBlob := curBlob
	if upd.ClearKey {
		nextBlob = []byte{}
	} else if trimmedKey := strings.TrimSpace(upd.APIKey); trimmedKey != "" {
		if len(trimmedKey) < 4 || len(trimmedKey) > 512 {
			return Status{}, errors.New("Jev API key must be 4-512 characters")
		}
		sealed, sErr := sealSecret(s.key, trimmedKey)
		if sErr != nil {
			return Status{}, sErr
		}
		nextBlob = sealed
	}
	if nextBlob == nil {
		nextBlob = []byte{}
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO jev_config (id, model, key_ciphertext, updated_at)
VALUES (1, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	model = excluded.model,
	key_ciphertext = excluded.key_ciphertext,
	updated_at = excluded.updated_at
`, nextModel, nextBlob, s.now().UTC().Unix())
	if err != nil {
		return Status{}, err
	}
	return Status{
		Configured:   len(nextBlob) > 0,
		Model:        nextModel,
		Endpoint:     DefaultEndpoint,
		FallbackMode: "deterministic",
	}, nil
}

// Evaluate runs typed System-1 classification against configured TypeSafe Jev
// or falls back deterministically when offline or unconfigured.
func (s *Service) Evaluate(ctx context.Context, req EvaluateRequest) (EvaluateResponse, error) {
	start := s.now()
	if strings.TrimSpace(req.Model) == "" {
		if storedModel, _, err := s.loadConfig(ctx); err == nil && storedModel != "" {
			req.Model = storedModel
		}
	}
	if err := ValidateRequest(&req); err != nil {
		return EvaluateResponse{}, err
	}

	_, cipherBlob, err := s.loadConfig(ctx)
	if err != nil {
		return EvaluateResponse{}, err
	}

	if s.fixtureMode || len(cipherBlob) == 0 {
		return EvaluateFallback(req, start)
	}

	apiKey, err := openSecret(s.key, cipherBlob)
	if err != nil || strings.TrimSpace(apiKey) == "" {
		return EvaluateFallback(req, start)
	}

	res, err := s.callRemoteJev(ctx, apiKey, req, start)
	if err != nil {
		return EvaluateResponse{}, err
	}
	return res, nil
}

func (s *Service) callRemoteJev(ctx context.Context, apiKey string, req EvaluateRequest, start time.Time) (EvaluateResponse, error) {
	u, err := url.Parse(DefaultEndpoint)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "api.typesafe.ai") {
		return EvaluateResponse{}, errors.New("invalid pinned Jev endpoint")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return EvaluateResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, DefaultEndpoint, bytes.NewReader(body))
	if err != nil {
		return EvaluateResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return EvaluateResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return EvaluateResponse{}, errors.New("Jev upstream redirect blocked")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return EvaluateResponse{}, err
	}
	if len(raw) > maxResponseBytes {
		return EvaluateResponse{}, errors.New("Jev response exceeded 1 MiB bound")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return EvaluateResponse{}, fmt.Errorf("Jev API returned HTTP %d", resp.StatusCode)
	}

	parsed, err := ParseJevResponse(raw)
	if err != nil {
		return EvaluateResponse{}, err
	}
	parsed.Provider = "typesafe-jev"
	parsed.Fallback = false
	parsed.LatencyMs = maxInt64(0, s.now().Sub(start).Milliseconds())
	return parsed, nil
}

// ParseJevResponse parses and validates a TypeSafe Jev JSON response safely.
func ParseJevResponse(raw []byte) (EvaluateResponse, error) {
	var parsed EvaluateResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return EvaluateResponse{}, fmt.Errorf("decode Jev response: %w", err)
	}
	if len(parsed.Answers) == 0 {
		return EvaluateResponse{}, errors.New("Jev response contained no answers")
	}
	for key, ans := range parsed.Answers {
		ans.Type = strings.ToLower(strings.TrimSpace(ans.Type))
		if math.IsNaN(ans.Confidence) || math.IsInf(ans.Confidence, 0) {
			ans.Confidence = 0
		}
		if ans.Confidence < 0 {
			ans.Confidence = 0
		} else if ans.Confidence > 1 {
			ans.Confidence = 1
		}
		if ans.Noul != nil {
			v := *ans.Noul
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return EvaluateResponse{}, fmt.Errorf("non-finite noul score for %q", key)
			}
			clamped := clampFloat(v, 0, 1)
			ans.Noul = &clamped
			if ans.Confidence == 0 {
				ans.Confidence = clampFloat(math.Abs(clamped-0.5)*2, 0, 1)
			}
		}
		if ans.Probabilities != nil {
			cleaned := make(map[string]float64, len(ans.Probabilities))
			for pk, pv := range ans.Probabilities {
				if math.IsNaN(pv) || math.IsInf(pv, 0) {
					continue
				}
				cleaned[pk] = clampFloat(pv, 0, 1)
			}
			ans.Probabilities = cleaned
		}
		parsed.Answers[key] = ans
	}
	return parsed, nil
}

// EvaluateFallback computes deterministic, <1ms offline typed System-1 scores.
func EvaluateFallback(req EvaluateRequest, start time.Time) (EvaluateResponse, error) {
	if err := ValidateRequest(&req); err != nil {
		return EvaluateResponse{}, err
	}
	stateTokens := tokenize(req.State)
	stateLower := strings.ToLower(req.State)

	answers := make(map[string]Answer, len(req.Questions))
	for key, spec := range req.Questions {
		switch spec.Type {
		case TypeNoul:
			val := deterministicNoulScore(stateLower, stateTokens, spec.Instructions)
			conf := clampFloat(math.Max(0.55, math.Abs(val-0.5)*2), 0, 1)
			answers[key] = Answer{
				Type:       TypeNoul,
				Noul:       &val,
				Confidence: round3(conf),
			}
		case TypeChoice:
			choice, probs, conf := deterministicChoiceScore(stateLower, stateTokens, spec)
			answers[key] = Answer{
				Type:          TypeChoice,
				Choice:        choice,
				Probabilities: probs,
				Confidence:    round3(conf),
			}
		case TypeScore:
			sc, legend, probs, conf := deterministicRubricScore(stateLower, stateTokens, spec)
			answers[key] = Answer{
				Type:          TypeScore,
				Score:         &sc,
				Legend:        legend,
				Probabilities: probs,
				Confidence:    round3(conf),
			}
		}
	}

	return EvaluateResponse{
		Provider: "deterministic-fallback",
		Model:    req.Model,
		Answers:  answers,
		Usage: Usage{
			InputTokens:  maxInt(1, len(req.State)/4),
			OutputTokens: len(req.Questions) * 8,
		},
		LatencyMs: maxInt64(0, time.Since(start).Milliseconds()),
		Fallback:  true,
	}, nil
}

var positiveSignals = map[string]float64{
	"urgent": 0.25, "critical": 0.25, "halt": 0.22, "fail": 0.2, "failing": 0.2,
	"verified": 0.18, "bullish": 0.22, "breakout": 0.18, "inflow": 0.15, "high": 0.12,
	"strong": 0.15, "surge": 0.18, "confirm": 0.15, "positive": 0.15,
}

var negativeSignals = map[string]float64{
	"stale": 0.2, "illiquid": 0.22, "bearish": 0.2, "exploit": 0.25, "drain": 0.22,
	"rug": 0.25, "noise": 0.15, "unchanged": 0.15, "low": 0.1,
}

func deterministicNoulScore(stateLower string, stateTokens map[string]int, instructions string) float64 {
	instTokens := tokenize(instructions)
	var overlap int
	for tok := range instTokens {
		if stateTokens[tok] > 0 {
			overlap++
		}
	}
	base := 0.42
	if len(instTokens) > 0 {
		base += 0.35 * (float64(overlap) / float64(len(instTokens)))
	}
	for word, weight := range positiveSignals {
		if strings.Contains(stateLower, word) && (strings.Contains(strings.ToLower(instructions), word) || word == "urgent" || word == "critical" || word == "fail" || word == "failing") {
			base += weight
		}
	}
	for word, weight := range negativeSignals {
		if strings.Contains(stateLower, word) {
			base -= weight * 0.5
		}
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(stateLower + "|" + instructions))
	jitter := float64(h.Sum32()%9) * 0.01
	return round3(clampFloat(base+jitter, 0.05, 0.95))
}

func deterministicChoiceScore(stateLower string, stateTokens map[string]int, spec QuestionSpec) (string, map[string]float64, float64) {
	scores := make([]float64, len(spec.Choices))
	var sum float64
	for i, ch := range spec.Choices {
		chLower := strings.ToLower(ch)
		chTokens := tokenize(chLower)
		s := 1.0
		if strings.Contains(stateLower, chLower) {
			s += 3.0
		}
		for tok := range chTokens {
			if stateTokens[tok] > 0 {
				s += 1.5
			}
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(stateLower + "|" + chLower))
		s += float64(h.Sum32()%100) * 0.005
		scores[i] = s
		sum += s
	}
	probs := make(map[string]float64, len(spec.Choices))
	bestIdx := 0
	for i, ch := range spec.Choices {
		p := scores[i] / sum
		probs[ch] = round3(p)
		if scores[i] > scores[bestIdx] {
			bestIdx = i
		}
	}
	return spec.Choices[bestIdx], probs, probs[spec.Choices[bestIdx]]
}

func deterministicRubricScore(stateLower string, stateTokens map[string]int, spec QuestionSpec) (int, string, map[string]float64, float64) {
	n := len(spec.Rubric)
	rawWeights := make([]float64, n)
	var sum float64
	for i, r := range spec.Rubric {
		rLower := strings.ToLower(r)
		w := 1.0
		if strings.Contains(stateLower, rLower) {
			w += 2.5
		}
		for tok := range tokenize(rLower) {
			if stateTokens[tok] > 0 {
				w += 1.2
			}
		}
		if strings.Contains(stateLower, "bullish") || strings.Contains(stateLower, "verified") || strings.Contains(stateLower, "urgent") {
			w += float64(i) * 0.25
		}
		rawWeights[i] = w
		sum += w
	}
	probs := make(map[string]float64, n)
	bestIdx := 0
	for i := 0; i < n; i++ {
		if rawWeights[i] > rawWeights[bestIdx] {
			bestIdx = i
		}
		probs[strconv.Itoa(i+1)] = round3(rawWeights[i] / sum)
	}
	scoreVal := bestIdx + 1
	return scoreVal, spec.Rubric[bestIdx], probs, probs[strconv.Itoa(scoreVal)]
}

func tokenize(text string) map[string]int {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make(map[string]int, len(words))
	sort.Strings(words)
	for _, w := range words {
		if len(w) >= 2 {
			out[w]++
		}
	}
	return out
}

func clampFloat(v, minV, maxV float64) float64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
