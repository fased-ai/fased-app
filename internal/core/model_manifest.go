package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	modelManifestFileName      = "model-manifest.json"
	modelManifestMaxBytes      = 512 * 1024
	modelManifestMaxModels     = 512
	modelManifestMaxContext    = 10_000_000
	modelManifestMaxOutput     = 1_000_000
	modelManifestMinContext    = 4096
	modelManifestMinOutput     = 256
	modelManifestFetchMaxDepth = 5
)

var allowedManifestCapabilities = map[string]bool{
	"tools":     true,
	"vision":    true,
	"reasoning": true,
	"search":    true,
	"audio":     true,
	"json":      true,
}

type ModelManifestEntry struct {
	Provider      string   `json:"provider"`
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	ContextTokens int64    `json:"contextTokens,omitempty"`
	OutputTokens  int64    `json:"outputTokens,omitempty"`
	Input         int64    `json:"input,omitempty"`
	Cached        int64    `json:"cached,omitempty"`
	Output        int64    `json:"output,omitempty"`
	Currency      string   `json:"currency,omitempty"`
	Reasoning     string   `json:"reasoning,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
}

type ModelManifest struct {
	Version   int                  `json:"version"`
	UpdatedAt int64                `json:"updatedAt"`
	Source    string               `json:"source,omitempty"`
	Models    []ModelManifestEntry `json:"models"`
}

type ModelManifestStatus struct {
	Loaded     bool                 `json:"loaded"`
	Source     string               `json:"source,omitempty"`
	UpdatedAt  int64                `json:"updatedAt,omitempty"`
	SHA256     string               `json:"sha256,omitempty"`
	ModelCount int                  `json:"modelCount"`
	Models     []ModelManifestEntry `json:"models,omitempty"`
}

type cachedManifestRecord struct {
	modUnixNano int64
	size        int64
	sha256      string
	manifest    ModelManifest
	byKey       map[string]ModelManifestEntry
	byID        map[string]ModelManifestEntry
}

var (
	modelManifestMu    sync.Mutex
	modelManifestCache = map[string]cachedManifestRecord{}
)

func init() {
	modelManifestLookup = func(dir, provider, modelID string) (currentModel, bool) {
		return resolveModelFromDirManifest(dir, provider, modelID)
	}
	modelManifestCatalogHook = func(dir string, v AIConnectionView, catalog *ProviderCatalog) {
		enrichCatalogFromDirManifest(dir, v, catalog)
	}
}

func validManifestPrintable(s string, maxRunes int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxRunes {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func ParseAndValidateModelManifest(raw []byte) (ModelManifest, error) {
	if len(raw) == 0 || len(raw) > modelManifestMaxBytes {
		return ModelManifest{}, errors.New("model manifest exceeds 512 KiB size limit or is empty")
	}
	if !uniqueAIJSON(raw, 20000) {
		return ModelManifest{}, errors.New("model manifest JSON rejected (duplicate keys or invalid structure)")
	}
	var m ModelManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return ModelManifest{}, errors.New("invalid model manifest JSON format")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ModelManifest{}, errors.New("model manifest must contain a single JSON object")
	}
	if m.Version != 1 {
		return ModelManifest{}, errors.New("model manifest version must be 1")
	}
	if m.UpdatedAt <= 0 {
		return ModelManifest{}, errors.New("model manifest updatedAt must be a positive Unix timestamp")
	}
	if m.Source != "" && !validManifestPrintable(m.Source, 256) {
		return ModelManifest{}, errors.New("invalid model manifest source")
	}
	if len(m.Models) == 0 || len(m.Models) > modelManifestMaxModels {
		return ModelManifest{}, errors.New("model manifest must include between 1 and 512 models")
	}

	seen := make(map[string]bool, len(m.Models))
	for i := range m.Models {
		e := &m.Models[i]
		e.Provider = strings.ToLower(strings.TrimSpace(e.Provider))
		e.ID = strings.TrimSpace(e.ID)
		e.Name = strings.TrimSpace(e.Name)
		e.Currency = strings.ToUpper(strings.TrimSpace(e.Currency))
		e.Reasoning = strings.ToLower(strings.TrimSpace(e.Reasoning))

		if e.Provider == "" || e.Provider == "local" || e.Provider == "test" {
			return ModelManifest{}, errors.New("invalid model manifest provider")
		}
		if _, ok := providerDefinitionFor(e.Provider); !ok {
			return ModelManifest{}, errors.New("unknown model manifest provider")
		}
		if !validProviderModelID(e.ID) {
			return ModelManifest{}, errors.New("invalid model manifest id")
		}
		key := e.Provider + ":" + e.ID
		if seen[key] {
			return ModelManifest{}, errors.New("duplicate model entry in model manifest")
		}
		seen[key] = true

		if e.Name != "" && !validManifestPrintable(e.Name, 128) {
			return ModelManifest{}, errors.New("invalid model manifest name")
		}
		if e.ContextTokens != 0 && (e.ContextTokens < modelManifestMinContext || e.ContextTokens > modelManifestMaxContext) {
			return ModelManifest{}, errors.New("model manifest contextTokens out of range")
		}
		if e.OutputTokens != 0 && (e.OutputTokens < modelManifestMinOutput || e.OutputTokens > modelManifestMaxOutput) {
			return ModelManifest{}, errors.New("model manifest outputTokens out of range")
		}
		if e.Input < 0 || e.Output < 0 || e.Cached < 0 {
			return ModelManifest{}, errors.New("model manifest pricing must be non-negative")
		}
		if e.Input > 0 && e.Cached > e.Input {
			return ModelManifest{}, errors.New("model manifest cached rate exceeds input rate")
		}
		if e.Currency != "" && e.Currency != "USD" {
			return ModelManifest{}, errors.New("model manifest currency must be USD when specified")
		}
		switch e.Reasoning {
		case "", "none", "low", "medium", "high":
		default:
			return ModelManifest{}, errors.New("invalid model manifest reasoning value")
		}
		if len(e.Capabilities) > 8 {
			return ModelManifest{}, errors.New("too many model manifest capabilities")
		}
		if len(e.Capabilities) > 0 {
			capSeen := make(map[string]bool, len(e.Capabilities))
			cleaned := make([]string, 0, len(e.Capabilities))
			for _, capName := range e.Capabilities {
				c := strings.ToLower(strings.TrimSpace(capName))
				if !allowedManifestCapabilities[c] {
					return ModelManifest{}, errors.New("unknown model manifest capability")
				}
				if !capSeen[c] {
					capSeen[c] = true
					cleaned = append(cleaned, c)
				}
			}
			e.Capabilities = cleaned
		}
	}
	return m, nil
}

func indexManifest(m ModelManifest, modUnixNano, size int64, digest string) cachedManifestRecord {
	byKey := make(map[string]ModelManifestEntry, len(m.Models))
	byID := make(map[string]ModelManifestEntry, len(m.Models))
	for _, e := range m.Models {
		byKey[e.Provider+":"+e.ID] = e
		if _, exists := byID[e.ID]; !exists {
			byID[e.ID] = e
		}
	}
	return cachedManifestRecord{
		modUnixNano: modUnixNano,
		size:        size,
		sha256:      digest,
		manifest:    m,
		byKey:       byKey,
		byID:        byID,
	}
}

func loadDirManifestCached(dir string) (cachedManifestRecord, bool) {
	if strings.TrimSpace(dir) == "" {
		return cachedManifestRecord{}, false
	}
	path := filepath.Join(dir, modelManifestFileName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > modelManifestMaxBytes {
		modelManifestMu.Lock()
		delete(modelManifestCache, dir)
		modelManifestMu.Unlock()
		return cachedManifestRecord{}, false
	}
	modNano := info.ModTime().UnixNano()
	size := info.Size()

	modelManifestMu.Lock()
	cached, ok := modelManifestCache[dir]
	modelManifestMu.Unlock()
	if ok && cached.modUnixNano == modNano && cached.size == size {
		return cached, true
	}

	raw, err := ReadPrivateFile(path)
	if err != nil {
		return cachedManifestRecord{}, false
	}
	parsed, err := ParseAndValidateModelManifest(raw)
	if err != nil {
		return cachedManifestRecord{}, false
	}
	sum := sha256.Sum256(raw)
	rec := indexManifest(parsed, modNano, size, hex.EncodeToString(sum[:]))

	modelManifestMu.Lock()
	modelManifestCache[dir] = rec
	modelManifestMu.Unlock()
	return rec, true
}

func manifestEntryToCurrentModel(e ModelManifestEntry) currentModel {
	name := e.Name
	if name == "" {
		name = e.ID
	}
	currency := e.Currency
	if currency == "" && (e.Input > 0 || e.Output > 0) {
		currency = "USD"
	}
	reasoning := e.Reasoning
	if reasoning == "" {
		reasoning = "none"
	}
	return currentModel{
		Provider:      e.Provider,
		ID:            e.ID,
		Name:          name,
		ContextTokens: e.ContextTokens,
		OutputTokens:  e.OutputTokens,
		Input:         e.Input,
		Cached:        e.Cached,
		Output:        e.Output,
		Currency:      currency,
		Reasoning:     reasoning,
		Source:        "manifest",
	}
}

func resolveModelFromDirManifest(dir, provider, modelID string) (currentModel, bool) {
	id := strings.TrimSpace(modelID)
	prov := strings.ToLower(strings.TrimSpace(provider))
	if id == "" || prov == "local" {
		return currentModel{}, false
	}
	rec, ok := loadDirManifestCached(dir)
	if !ok {
		return currentModel{}, false
	}
	var entry ModelManifestEntry
	var found bool
	if prov != "" {
		entry, found = rec.byKey[prov+":"+id]
	}
	if !found && prov == "" {
		entry, found = rec.byID[id]
	}
	if !found {
		return currentModel{}, false
	}
	base, hasBase := currentProviderModel(id)
	overlay := manifestEntryToCurrentModel(entry)
	if hasBase {
		if overlay.Name == id && base.Name != "" {
			overlay.Name = base.Name
		}
		if overlay.ContextTokens == 0 {
			overlay.ContextTokens = base.ContextTokens
		}
		if overlay.OutputTokens == 0 {
			overlay.OutputTokens = base.OutputTokens
		}
		if overlay.Input == 0 && overlay.Output == 0 {
			overlay.Input = base.Input
			overlay.Cached = base.Cached
			overlay.Output = base.Output
			overlay.Currency = base.Currency
		}
		if entry.Reasoning == "" && base.Reasoning != "" {
			overlay.Reasoning = base.Reasoning
		}
		if overlay.Tier == "" {
			overlay.Tier = base.Tier
		}
	}
	return overlay, true
}

func enrichCatalogFromDirManifest(dir string, v AIConnectionView, catalog *ProviderCatalog) {
	if catalog == nil || dir == "" || v.Provider == "" || v.Provider == "local" {
		return
	}
	rec, ok := loadDirManifestCached(dir)
	if !ok {
		return
	}
	existing := make(map[string]bool, len(catalog.Models))
	for i := range catalog.Models {
		m := &catalog.Models[i]
		existing[m.ID] = true
		entry, found := rec.byKey[v.Provider+":"+m.ID]
		if !found {
			continue
		}
		if entry.Name != "" {
			m.Name = entry.Name
		}
		if m.Policy == nil {
			pol := dynamicModelPolicy(v.Provider, m.ID, m.Method)
			m.Policy = pol
		}
		if m.Policy != nil {
			p := *m.Policy
			if entry.ContextTokens > 0 {
				p.ContextTokens = entry.ContextTokens
			}
			if entry.OutputTokens > 0 {
				p.OutputTokens = entry.OutputTokens
			}
			if entry.Input > 0 && entry.Output > 0 {
				p.InputMicrousdPerMillion = entry.Input
				p.CachedMicrousdPerMillion = entry.Cached
				p.OutputMicrousdPerMillion = entry.Output
			}
			if entry.Reasoning != "" {
				p.Reasoning = entry.Reasoning
			}
			m.Policy = &p
		}
	}
	for _, entry := range rec.manifest.Models {
		if entry.Provider != v.Provider || existing[entry.ID] {
			continue
		}
		pol := dynamicModelPolicy(v.Provider, entry.ID, v.AuthMethod)
		if pol != nil {
			p := *pol
			if entry.ContextTokens > 0 {
				p.ContextTokens = entry.ContextTokens
			}
			if entry.OutputTokens > 0 {
				p.OutputTokens = entry.OutputTokens
			}
			if entry.Input > 0 && entry.Output > 0 {
				p.InputMicrousdPerMillion = entry.Input
				p.CachedMicrousdPerMillion = entry.Cached
				p.OutputMicrousdPerMillion = entry.Output
			}
			if entry.Reasoning != "" {
				p.Reasoning = entry.Reasoning
			}
			pol = &p
		}
		name := entry.Name
		if name == "" {
			name = entry.ID
		}
		avail := "unverified"
		selectable := false
		switch {
		case v.Disabled:
			avail = "disabled"
		case v.Ref == "":
			avail = "disconnected"
		case v.Locked:
			avail = "locked"
		case v.TestOnly:
			avail = "fixture"
			selectable = true
		case !v.LiveCallsEnabled && v.AuthMethod != "cli":
			avail = "disabled"
			selectable = true
		case catalog.State == "ready" || v.AuthMethod == "cli":
			avail = "available"
			selectable = true
		}
		catalog.Models = append(catalog.Models, ProviderModel{
			ID:            entry.ID,
			Name:          name,
			Provider:      v.Provider,
			Method:        v.AuthMethod,
			ConnectionRef: v.Ref,
			Availability:  avail,
			Selectable:    selectable,
			Policy:        pol,
		})
	}
}

func (s *Store) resolveManifestModel(provider, modelID string) (currentModel, bool) {
	if s == nil {
		return currentModel{}, false
	}
	return resolveModelFromDirManifest(s.dir, provider, modelID)
}

func (s *Store) ModelManifestStatus(_ context.Context) ModelManifestStatus {
	if s == nil {
		return ModelManifestStatus{}
	}
	rec, ok := loadDirManifestCached(s.dir)
	if !ok {
		return ModelManifestStatus{}
	}
	models := make([]ModelManifestEntry, len(rec.manifest.Models))
	copy(models, rec.manifest.Models)
	return ModelManifestStatus{
		Loaded:     true,
		Source:     rec.manifest.Source,
		UpdatedAt:  rec.manifest.UpdatedAt,
		SHA256:     rec.sha256,
		ModelCount: len(rec.manifest.Models),
		Models:     models,
	}
}

func (s *Store) SaveModelManifest(ctx context.Context, raw []byte, source string) (ModelManifestStatus, error) {
	if s == nil || s.dir == "" {
		return ModelManifestStatus{}, errors.New("workspace directory unavailable")
	}
	parsed, err := ParseAndValidateModelManifest(raw)
	if err != nil {
		return ModelManifestStatus{}, err
	}
	if s.db != nil {
		if phase, _, pErr := readPause(ctx, s.db); pErr == nil && phase == "paused" {
			return ModelManifestStatus{}, ErrCorePaused
		}
	}
	if strings.TrimSpace(source) != "" {
		if !validManifestPrintable(strings.TrimSpace(source), 256) {
			return ModelManifestStatus{}, errors.New("invalid model manifest source")
		}
		parsed.Source = strings.TrimSpace(source)
	}
	if parsed.Source == "" {
		parsed.Source = "manual"
	}
	encoded, err := json.Marshal(parsed)
	if err != nil || len(encoded)+1 > modelManifestMaxBytes {
		return ModelManifestStatus{}, errors.New("model manifest exceeds 512 KiB size limit")
	}
	if err := WritePrivateJSON(filepath.Join(s.dir, modelManifestFileName), parsed); err != nil {
		return ModelManifestStatus{}, err
	}
	modelManifestMu.Lock()
	delete(modelManifestCache, s.dir)
	modelManifestMu.Unlock()
	signalStoreChange(s)
	return s.ModelManifestStatus(ctx), nil
}

func sanitizePublicManifestURL(u *url.URL) string {
	if u == nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	clean := &url.URL{
		Scheme: strings.ToLower(u.Scheme),
		Host:   u.Host,
		Path:   u.Path,
	}
	return clean.String()
}

func (s *Store) FetchAndSaveModelManifest(ctx context.Context, rawURL string) (ModelManifestStatus, error) {
	if s == nil {
		return ModelManifestStatus{}, errors.New("workspace store unavailable")
	}
	allowTest := s.providerFixture || s.agentV2TransportOverride != nil || s.marketDataTransport != nil
	targetURL, err := validPublicWebURL(rawURL, allowTest)
	if err != nil {
		return ModelManifestStatus{}, errors.New("model manifest URL must be a valid public https:// URL")
	}
	if s.providerFixture && s.agentV2TransportOverride == nil && s.marketDataTransport == nil && strings.HasSuffix(strings.ToLower(targetURL.Hostname()), ".invalid") {
		fixtureRaw := []byte(`{"version":1,"updatedAt":1760000000,"models":[{"provider":"openai","id":"gpt-6.3-manifest-preview","name":"GPT 6.3 Manifest Preview","contextTokens":256000,"outputTokens":16384,"input":3000000,"cached":750000,"output":12000000,"currency":"USD","reasoning":"high","capabilities":["tools","reasoning"]}]}`)
		return s.SaveModelManifest(ctx, fixtureRaw, sanitizePublicManifestURL(targetURL))
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rt := s.agentV2TransportOverride
	if rt == nil {
		rt = s.marketDataTransport
	}
	if rt == nil {
		rt = getPublicMarketLiveTransport()
	}
	client := &http.Client{
		Transport: rt,
		Timeout:   10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= modelManifestFetchMaxDepth {
				return errors.New("too many redirects")
			}
			if _, err := validPublicWebURL(req.URL.String(), allowTest); err != nil {
				return err
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, targetURL.String(), nil)
	if err != nil {
		return ModelManifestStatus{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Fased/"+Version)

	resp, err := client.Do(req)
	if err != nil {
		return ModelManifestStatus{}, errors.New("failed to fetch model manifest")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ModelManifestStatus{}, errors.New("model manifest server returned non-200 status")
	}
	mediaType, _, mediaErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaErr != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
		return ModelManifestStatus{}, errors.New("model manifest response must have application/json Content-Type")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, modelManifestMaxBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > modelManifestMaxBytes {
		return ModelManifestStatus{}, errors.New("model manifest response exceeds 512 KiB size limit")
	}
	return s.SaveModelManifest(ctx, raw, sanitizePublicManifestURL(targetURL))
}

func (s *Store) ClearModelManifest(ctx context.Context) (ModelManifestStatus, error) {
	if s == nil || s.dir == "" {
		return ModelManifestStatus{}, errors.New("workspace directory unavailable")
	}
	if s.db != nil {
		if phase, _, pErr := readPause(ctx, s.db); pErr == nil && phase == "paused" {
			return ModelManifestStatus{}, ErrCorePaused
		}
	}
	path := filepath.Join(s.dir, modelManifestFileName)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ModelManifestStatus{}, err
	}
	modelManifestMu.Lock()
	delete(modelManifestCache, s.dir)
	modelManifestMu.Unlock()
	signalStoreChange(s)
	return ModelManifestStatus{}, nil
}
