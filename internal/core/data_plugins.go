package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/httpapi"
	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/providersecrets"
	"github.com/fased-ai/fased-app/internal/vault"
	"github.com/fased-ai/fased-app/internal/worldintel"
)

const (
	PluginCatExchanges   = "exchanges"
	PluginCatNews        = "news"
	PluginCatPredictions = "predictions"
	PluginCatRPCs        = "rpcs"
)

type sealedPluginSecret struct {
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
	Masked     string `json:"masked,omitempty"`
}

type dataPluginRecord struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Category        string              `json:"category"`
	Kind            string              `json:"kind,omitempty"`
	Region          string              `json:"region,omitempty"`
	Cluster         string              `json:"cluster,omitempty"`
	Endpoint        string              `json:"endpoint,omitempty"`
	Enabled         bool                `json:"enabled"`
	DataFeedEnabled bool                `json:"dataFeedEnabled"`
	TradingEnabled  bool                `json:"tradingEnabled"`
	SupportsFeed    bool                `json:"supportsFeed"`
	SupportsTrading bool                `json:"supportsTrading"`
	DefaultChart    bool                `json:"defaultChart,omitempty"`
	BuiltIn         bool                `json:"builtIn"`
	Custom          bool                `json:"custom,omitempty"`
	AllowLocalhost  bool                `json:"allowLocalhost,omitempty"`
	SealedAPIKey    *sealedPluginSecret `json:"sealedApiKey,omitempty"`
	SealedAPISecret *sealedPluginSecret `json:"sealedApiSecret,omitempty"`
	LastCheckedAt   int64               `json:"lastCheckedAt,omitempty"`
	LastStatus      string              `json:"lastStatus,omitempty"`
	LastLatencyMs   int64               `json:"lastLatencyMs,omitempty"`
	LastError       string              `json:"lastError,omitempty"`
}

type dataPluginsDoc struct {
	Version int                `json:"version"`
	Plugins []dataPluginRecord `json:"plugins"`
}

// DataPluginView is the safe HTTP-facing representation of a pluggable data/feed/RPC source.
// Raw API keys and secrets are never serialized into JSON responses.
type DataPluginView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Category        string `json:"category"`
	Kind            string `json:"kind,omitempty"`
	Region          string `json:"region,omitempty"`
	Cluster         string `json:"cluster,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	Enabled         bool   `json:"enabled"`
	DataFeedEnabled bool   `json:"dataFeedEnabled"`
	TradingEnabled  bool   `json:"tradingEnabled"`
	SupportsFeed    bool   `json:"supportsFeed"`
	SupportsTrading bool   `json:"supportsTrading"`
	DefaultChart    bool   `json:"defaultChart,omitempty"`
	BuiltIn         bool   `json:"builtIn"`
	Custom          bool   `json:"custom,omitempty"`
	AllowLocalhost  bool   `json:"allowLocalhost,omitempty"`
	HasAPIKey       bool   `json:"hasApiKey"`
	HasAPISecret    bool   `json:"hasApiSecret"`
	MaskedAPIKey    string `json:"maskedApiKey,omitempty"`
	LastCheckedAt   int64  `json:"lastCheckedAt,omitempty"`
	LastStatus      string `json:"lastStatus,omitempty"`
	LastLatencyMs   int64  `json:"lastLatencyMs,omitempty"`
	LastError       string `json:"lastError,omitempty"`
}

type DataPluginsSummary struct {
	ActiveTotal       int `json:"activeTotal"`
	ActiveExchanges   int `json:"activeExchanges"`
	ActiveNews        int `json:"activeNews"`
	ActivePredictions int `json:"activePredictions"`
	ActiveRPCs        int `json:"activeRpcs"`
}

type DataPluginsResponse struct {
	Plugins []DataPluginView   `json:"plugins"`
	Summary DataPluginsSummary `json:"summary"`
}

type DataPluginPatch struct {
	Enabled         *bool   `json:"enabled,omitempty"`
	DataFeedEnabled *bool   `json:"dataFeedEnabled,omitempty"`
	TradingEnabled  *bool   `json:"tradingEnabled,omitempty"`
	DefaultChart    *bool   `json:"defaultChart,omitempty"`
	Endpoint        *string `json:"endpoint,omitempty"`
	APIKey          *string `json:"apiKey,omitempty"`
	APISecret       *string `json:"apiSecret,omitempty"`
	AllowLocalhost  *bool   `json:"allowLocalhost,omitempty"`
}

type CustomDataPluginInput struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	Kind           string `json:"kind"`
	Region         string `json:"region"`
	Cluster        string `json:"cluster"`
	Endpoint       string `json:"endpoint"`
	Enabled        *bool  `json:"enabled,omitempty"`
	AllowLocalhost bool   `json:"allowLocalhost"`
	APIKey         string `json:"apiKey,omitempty"`
	APISecret      string `json:"apiSecret,omitempty"`
}

func defaultDataPluginsCatalog() []dataPluginRecord {
	out := []dataPluginRecord{
		{ID: "hyperliquid", Name: "Hyperliquid", Category: PluginCatExchanges, Endpoint: "https://api.hyperliquid.xyz", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "binance", Name: "Binance", Category: PluginCatExchanges, Endpoint: "https://data-api.binance.vision", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "bybit", Name: "Bybit", Category: PluginCatExchanges, Endpoint: "https://api.bybit.com", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "okx", Name: "OKX", Category: PluginCatExchanges, Endpoint: "https://www.okx.com", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "kraken", Name: "Kraken", Category: PluginCatExchanges, Endpoint: "https://api.kraken.com", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "coinbase", Name: "Coinbase", Category: PluginCatExchanges, Endpoint: "https://api.exchange.coinbase.com", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "deribit", Name: "Deribit", Category: PluginCatExchanges, Endpoint: "https://www.deribit.com", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "lighter", Name: "Lighter", Category: PluginCatExchanges, Endpoint: "https://mainnet.zklighter.elliott.ai", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "solana", Name: "Solana DEX (Jupiter / Gecko)", Category: PluginCatExchanges, Endpoint: "https://api.geckoterminal.com", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "alpaca", Name: "Alpaca Markets", Category: PluginCatExchanges, Endpoint: "https://data.alpaca.markets", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "ibkr", Name: "Interactive Brokers", Category: PluginCatExchanges, Endpoint: "https://127.0.0.1:5000", SupportsFeed: true, SupportsTrading: true, AllowLocalhost: true, BuiltIn: true},
		{ID: "fred", Name: "FRED Macro Series", Category: PluginCatExchanges, Endpoint: "https://fred.stlouisfed.org", SupportsFeed: true, BuiltIn: true},
		{ID: "sec_edgar", Name: "SEC EDGAR Filings", Category: PluginCatExchanges, Endpoint: "https://data.sec.gov", SupportsFeed: true, BuiltIn: true},
		{ID: "custom", Name: "Custom CSV / Webhook Feeds", Category: PluginCatExchanges, SupportsFeed: true, BuiltIn: true},
		// Chain RPC Providers (all disabled until explicitly configured/activated by user)
		{ID: "solana-mainnet", Name: "Solana Mainnet RPC", Category: PluginCatRPCs, Kind: "solana", Cluster: "mainnet-beta", Endpoint: "https://api.mainnet-beta.solana.com", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "solana-devnet", Name: "Solana Devnet RPC", Category: PluginCatRPCs, Kind: "solana", Cluster: "devnet", Endpoint: "https://api.devnet.solana.com", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "evm-arbitrum", Name: "Arbitrum One RPC", Category: PluginCatRPCs, Kind: "evm", Cluster: "arbitrum", Endpoint: "https://arb1.arbitrum.io/rpc", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
		{ID: "evm-mainnet", Name: "Ethereum Mainnet RPC", Category: PluginCatRPCs, Kind: "evm", Cluster: "ethereum", Endpoint: "https://eth.llamarpc.com", SupportsFeed: true, SupportsTrading: true, BuiltIn: true},
	}

	for _, src := range worldintel.DefaultSources() {
		cat := PluginCatNews
		supportsTrading := false
		if src.Kind == worldintel.KindPolymarket || src.Kind == worldintel.KindKalshi {
			cat = PluginCatPredictions
			supportsTrading = true
		}
		out = append(out, dataPluginRecord{
			ID:              src.ID,
			Name:            src.Name,
			Category:        cat,
			Kind:            src.Kind,
			Region:          src.Region,
			Endpoint:        src.Endpoint,
			Enabled:         false,
			DataFeedEnabled: false,
			TradingEnabled:  false,
			SupportsFeed:    true,
			SupportsTrading: supportsTrading,
			BuiltIn:         true,
			AllowLocalhost:  src.AllowLocalhost,
		})
	}
	return out
}

func maskPluginSecret(v string) string {
	trimmed := strings.TrimSpace(v)
	if len(trimmed) <= 4 {
		if trimmed == "" {
			return ""
		}
		return "****"
	}
	return "****" + trimmed[len(trimmed)-4:]
}

func pluginAAD(pluginID, field string) []byte {
	return []byte("fased-data-plugin-v1:" + pluginID + ":" + field)
}

type sealedPluginKeyEnvelope struct {
	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func (s *Store) pluginSecretKey(create bool) ([]byte, error) {
	sealedPath := filepath.Join(s.dir, "data-plugins.key.sealed")
	keyPath := filepath.Join(s.dir, "data-plugins.key")

	var kek []byte
	if s.taskSecrets != nil {
		if k, err := s.taskSecrets.Key(context.Background(), s.dir+":data-plugins", create); err == nil && len(k) == 32 {
			kek = k
			defer clear(kek)
		}
	}

	if rawSealed, err := readPrivateFile(sealedPath); err == nil {
		var env sealedPluginKeyEnvelope
		if jErr := json.Unmarshal(rawSealed, &env); jErr != nil || env.Version != 1 {
			return nil, errors.New("corrupt sealed data-plugins key")
		}
		if len(kek) != 32 {
			return nil, providersecrets.ErrUnavailable
		}
		plain, oErr := providersecrets.Open(kek, env.Nonce, env.Ciphertext, []byte("fased-data-plugins-kek:v1"))
		if oErr != nil || len(plain) != 32 {
			return nil, errors.New("failed to unwrap sealed data-plugins key")
		}
		return plain, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	raw, err := readPrivateFile(keyPath)
	if err == nil && len(raw) == 32 {
		if len(kek) == 32 {
			if n, ct, sErr := providersecrets.Seal(kek, raw, []byte("fased-data-plugins-kek:v1")); sErr == nil {
				if wErr := WritePrivateJSON(sealedPath, sealedPluginKeyEnvelope{Version: 1, Nonce: n, Ciphertext: ct}); wErr == nil {
					_ = os.Remove(keyPath)
				}
			}
		}
		return raw, nil
	}
	if !errors.Is(err, os.ErrNotExist) && err != nil {
		return nil, err
	}
	if !create {
		return nil, os.ErrNotExist
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if len(kek) == 32 {
		n, ct, sErr := providersecrets.Seal(kek, key, []byte("fased-data-plugins-kek:v1"))
		if sErr == nil {
			if wErr := WritePrivateJSON(sealedPath, sealedPluginKeyEnvelope{Version: 1, Nonce: n, Ciphertext: ct}); wErr == nil {
				return key, nil
			}
		}
	}
	if err := privateDir(s.dir); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(s.dir, ".dpk-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(f.Name(), keyPath); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *Store) sealPluginValue(pluginID, field, plain string) (*sealedPluginSecret, error) {
	trimmed := strings.TrimSpace(plain)
	if trimmed == "" {
		return nil, nil
	}
	key, err := s.pluginSecretKey(true)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	nonce, err := vault.RandomBytes(12)
	if err != nil {
		return nil, err
	}
	ct, err := vault.SealAESGCM(key, nonce, []byte(trimmed), pluginAAD(pluginID, field))
	if err != nil {
		return nil, err
	}
	return &sealedPluginSecret{
		Nonce:      nonce,
		Ciphertext: ct,
		Masked:     maskPluginSecret(trimmed),
	}, nil
}

func (s *Store) unsealPluginValue(pluginID, field string, sec *sealedPluginSecret) (string, error) {
	if sec == nil || len(sec.Ciphertext) == 0 {
		return "", nil
	}
	key, err := s.pluginSecretKey(false)
	if err != nil {
		return "", err
	}
	defer clear(key)
	plain, err := vault.OpenAESGCM(key, sec.Nonce, sec.Ciphertext, pluginAAD(pluginID, field))
	if err != nil {
		return "", err
	}
	defer clear(plain)
	return string(plain), nil
}

func mergePluginDefaults(doc *dataPluginsDoc) {
	if doc.Version <= 0 {
		doc.Version = 1
	}
	byID := make(map[string]dataPluginRecord, len(doc.Plugins))
	for _, p := range doc.Plugins {
		byID[p.ID] = p
	}
	defaults := defaultDataPluginsCatalog()
	merged := make([]dataPluginRecord, 0, len(defaults)+len(doc.Plugins))
	seen := make(map[string]bool, len(defaults))
	for _, def := range defaults {
		seen[def.ID] = true
		if cur, ok := byID[def.ID]; ok {
			cur.Name = def.Name
			cur.Category = def.Category
			cur.Kind = def.Kind
			if cur.Region == "" {
				cur.Region = def.Region
			}
			if cur.Cluster == "" {
				cur.Cluster = def.Cluster
			}
			cur.SupportsFeed = def.SupportsFeed
			cur.SupportsTrading = def.SupportsTrading
			cur.BuiltIn = true
			if cur.Endpoint == "" {
				cur.Endpoint = def.Endpoint
			}
			merged = append(merged, cur)
		} else {
			merged = append(merged, def)
		}
	}
	for _, p := range doc.Plugins {
		if !seen[p.ID] {
			merged = append(merged, p)
		}
	}
	doc.Plugins = merged
}

func (s *Store) dataPluginsPath() string {
	return filepath.Join(s.dir, "data-plugins.json")
}

func (s *Store) loadDataPluginsLocked() (*dataPluginsDoc, error) {
	if s.dataPlugins != nil {
		return s.dataPlugins, nil
	}
	doc := &dataPluginsDoc{Version: 1}
	raw, err := readPrivateFile(s.dataPluginsPath())
	if err == nil && len(raw) > 0 {
		if jErr := json.Unmarshal(raw, doc); jErr != nil {
			return nil, errors.New("corrupt data-plugins state")
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	mergePluginDefaults(doc)
	s.dataPlugins = doc
	return doc, nil
}

func (s *Store) saveDataPluginsLocked(doc *dataPluginsDoc) error {
	if err := WritePrivateJSON(s.dataPluginsPath(), doc); err != nil {
		return err
	}
	s.dataPlugins = doc
	return nil
}

func toDataPluginView(rec dataPluginRecord) DataPluginView {
	status := rec.LastStatus
	if status == "" {
		if rec.Enabled {
			status = "ready"
		} else {
			status = "disabled"
		}
	}
	if !rec.Enabled {
		status = "disabled"
	}
	masked := ""
	if rec.SealedAPIKey != nil {
		masked = rec.SealedAPIKey.Masked
	}
	return DataPluginView{
		ID:              rec.ID,
		Name:            rec.Name,
		Category:        rec.Category,
		Kind:            rec.Kind,
		Region:          rec.Region,
		Cluster:         rec.Cluster,
		Endpoint:        rec.Endpoint,
		Enabled:         rec.Enabled,
		DataFeedEnabled: rec.DataFeedEnabled,
		TradingEnabled:  rec.TradingEnabled,
		SupportsFeed:    rec.SupportsFeed,
		SupportsTrading: rec.SupportsTrading,
		DefaultChart:    rec.DefaultChart,
		BuiltIn:         rec.BuiltIn,
		Custom:          rec.Custom,
		AllowLocalhost:  rec.AllowLocalhost,
		HasAPIKey:       rec.SealedAPIKey != nil && len(rec.SealedAPIKey.Ciphertext) > 0,
		HasAPISecret:    rec.SealedAPISecret != nil && len(rec.SealedAPISecret.Ciphertext) > 0,
		MaskedAPIKey:    masked,
		LastCheckedAt:   rec.LastCheckedAt,
		LastStatus:      status,
		LastLatencyMs:   rec.LastLatencyMs,
		LastError:       rec.LastError,
	}
}

func buildPluginsResponse(doc *dataPluginsDoc) DataPluginsResponse {
	views := make([]DataPluginView, 0, len(doc.Plugins))
	var summary DataPluginsSummary
	for _, p := range doc.Plugins {
		views = append(views, toDataPluginView(p))
		if !p.Enabled {
			continue
		}
		summary.ActiveTotal++
		switch p.Category {
		case PluginCatExchanges:
			summary.ActiveExchanges++
		case PluginCatNews:
			summary.ActiveNews++
		case PluginCatPredictions:
			summary.ActivePredictions++
		case PluginCatRPCs:
			summary.ActiveRPCs++
		}
	}
	return DataPluginsResponse{Plugins: views, Summary: summary}
}

func validatePluginEndpointURL(raw string, allowLocalhost bool) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return "", errors.New("valid endpoint URL required")
	}
	if u.User != nil {
		return "", errors.New("embedded URL credentials are not allowed; use the API key field")
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	isLocal := host == "127.0.0.1" || host == "localhost" || host == "::1"
	if isLocal {
		if !allowLocalhost {
			return "", errors.New("localhost endpoints require AllowLocalhost opt-in")
		}
		if scheme != "http" && scheme != "https" {
			return "", errors.New("endpoint must use http or https")
		}
		return u.String(), nil
	}
	if scheme != "https" {
		return "", errors.New("remote endpoints must use https")
	}
	if ip, err := netip.ParseAddr(host); err == nil && !custody.IsPublicUnicastIP(ip) {
		return "", errors.New("private or non-public IP endpoints are blocked")
	}
	return u.String(), nil
}

func (s *Store) ListDataPlugins() (DataPluginsResponse, error) {
	if s == nil {
		return DataPluginsResponse{}, errors.New("workspace store unavailable")
	}
	s.pluginsMu.Lock()
	defer s.pluginsMu.Unlock()
	doc, err := s.loadDataPluginsLocked()
	if err != nil {
		return DataPluginsResponse{}, err
	}
	return buildPluginsResponse(doc), nil
}

func (s *Store) UpdateDataPlugin(ctx context.Context, id string, patch DataPluginPatch) (DataPluginView, error) {
	if s == nil {
		return DataPluginView{}, errors.New("workspace store unavailable")
	}
	cleanID := strings.TrimSpace(id)
	s.pluginsMu.Lock()
	doc, err := s.loadDataPluginsLocked()
	if err != nil {
		s.pluginsMu.Unlock()
		return DataPluginView{}, err
	}
	idx := -1
	for i := range doc.Plugins {
		if doc.Plugins[i].ID == cleanID {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.pluginsMu.Unlock()
		return DataPluginView{}, os.ErrNotExist
	}
	rec := doc.Plugins[idx]
	if patch.AllowLocalhost != nil {
		rec.AllowLocalhost = *patch.AllowLocalhost
	}
	if patch.Endpoint != nil {
		validated, err := validatePluginEndpointURL(*patch.Endpoint, rec.AllowLocalhost)
		if err != nil {
			s.pluginsMu.Unlock()
			return DataPluginView{}, err
		}
		rec.Endpoint = validated
	}
	if patch.Enabled != nil {
		rec.Enabled = *patch.Enabled
		if *patch.Enabled && rec.SupportsFeed && patch.DataFeedEnabled == nil {
			rec.DataFeedEnabled = true
		}
		if !*patch.Enabled {
			rec.DataFeedEnabled = false
			rec.TradingEnabled = false
		}
	}
	if patch.DataFeedEnabled != nil {
		if *patch.DataFeedEnabled && !rec.SupportsFeed {
			s.pluginsMu.Unlock()
			return DataPluginView{}, errors.New("plugin does not support market data feed")
		}
		rec.DataFeedEnabled = *patch.DataFeedEnabled
		if *patch.DataFeedEnabled {
			rec.Enabled = true
		}
	}
	if patch.TradingEnabled != nil {
		if *patch.TradingEnabled && !rec.SupportsTrading {
			s.pluginsMu.Unlock()
			return DataPluginView{}, errors.New("plugin does not support trading execution")
		}
		rec.TradingEnabled = *patch.TradingEnabled
		if *patch.TradingEnabled {
			rec.Enabled = true
		}
	}
	if patch.DefaultChart != nil {
		if *patch.DefaultChart {
			for i := range doc.Plugins {
				if doc.Plugins[i].Category == PluginCatExchanges {
					doc.Plugins[i].DefaultChart = false
				}
			}
		}
		rec.DefaultChart = *patch.DefaultChart
	}
	if patch.APIKey != nil {
		if strings.TrimSpace(*patch.APIKey) == "" {
			rec.SealedAPIKey = nil
		} else {
			sealed, sErr := s.sealPluginValue(rec.ID, "apiKey", *patch.APIKey)
			if sErr != nil {
				s.pluginsMu.Unlock()
				return DataPluginView{}, sErr
			}
			rec.SealedAPIKey = sealed
		}
	}
	if patch.APISecret != nil {
		if strings.TrimSpace(*patch.APISecret) == "" {
			rec.SealedAPISecret = nil
		} else {
			sealed, sErr := s.sealPluginValue(rec.ID, "apiSecret", *patch.APISecret)
			if sErr != nil {
				s.pluginsMu.Unlock()
				return DataPluginView{}, sErr
			}
			rec.SealedAPISecret = sealed
		}
	}
	doc.Plugins[idx] = rec
	if err := s.saveDataPluginsLocked(doc); err != nil {
		s.pluginsMu.Unlock()
		return DataPluginView{}, err
	}
	s.pluginsMu.Unlock()

	if rec.Category == PluginCatNews || rec.Category == PluginCatPredictions {
		s.syncPluginToWorldIntel(ctx, rec)
	}
	return toDataPluginView(rec), nil
}

func (s *Store) AddCustomDataPlugin(ctx context.Context, in CustomDataPluginInput) (DataPluginView, error) {
	if s == nil {
		return DataPluginView{}, errors.New("workspace store unavailable")
	}
	cat := strings.ToLower(strings.TrimSpace(in.Category))
	if cat != PluginCatExchanges && cat != PluginCatNews && cat != PluginCatPredictions && cat != PluginCatRPCs {
		return DataPluginView{}, errors.New("invalid category (exchanges, news, predictions, or rpcs)")
	}
	name := strings.TrimSpace(in.Name)
	if !validName(name) {
		return DataPluginView{}, errors.New("valid plugin name required")
	}
	validatedURL, err := validatePluginEndpointURL(in.Endpoint, in.AllowLocalhost)
	if err != nil {
		return DataPluginView{}, err
	}
	if validatedURL == "" {
		return DataPluginView{}, errors.New("endpoint URL is required")
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		sum := sha256.Sum256([]byte(cat + ":" + name + ":" + validatedURL + ":" + s.now().String()))
		id = "custom-" + hex.EncodeToString(sum[:6])
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		if cat == PluginCatNews {
			kind = worldintel.KindRSS
		} else if cat == PluginCatRPCs {
			kind = "solana"
		}
	}
	region := strings.TrimSpace(in.Region)
	if region == "" && cat == PluginCatNews {
		region = worldintel.RegionGlobal
	}
	rec := dataPluginRecord{
		ID:              id,
		Name:            name,
		Category:        cat,
		Kind:            kind,
		Region:          region,
		Cluster:         strings.TrimSpace(in.Cluster),
		Endpoint:        validatedURL,
		Enabled:         enabled,
		DataFeedEnabled: enabled,
		SupportsFeed:    true,
		SupportsTrading: cat == PluginCatRPCs || cat == PluginCatExchanges,
		BuiltIn:         false,
		Custom:          true,
		AllowLocalhost:  in.AllowLocalhost,
	}
	if strings.TrimSpace(in.APIKey) != "" {
		sealed, sErr := s.sealPluginValue(rec.ID, "apiKey", in.APIKey)
		if sErr != nil {
			return DataPluginView{}, sErr
		}
		rec.SealedAPIKey = sealed
	}
	if strings.TrimSpace(in.APISecret) != "" {
		sealed, sErr := s.sealPluginValue(rec.ID, "apiSecret", in.APISecret)
		if sErr != nil {
			return DataPluginView{}, sErr
		}
		rec.SealedAPISecret = sealed
	}

	s.pluginsMu.Lock()
	doc, err := s.loadDataPluginsLocked()
	if err != nil {
		s.pluginsMu.Unlock()
		return DataPluginView{}, err
	}
	for _, existing := range doc.Plugins {
		if existing.ID == rec.ID {
			s.pluginsMu.Unlock()
			return DataPluginView{}, errors.New("plugin id already exists")
		}
	}
	doc.Plugins = append(doc.Plugins, rec)
	if err := s.saveDataPluginsLocked(doc); err != nil {
		s.pluginsMu.Unlock()
		return DataPluginView{}, err
	}
	s.pluginsMu.Unlock()

	if rec.Category == PluginCatNews || rec.Category == PluginCatPredictions {
		s.syncPluginToWorldIntel(ctx, rec)
	}
	return toDataPluginView(rec), nil
}

func (s *Store) DeleteCustomDataPlugin(ctx context.Context, id string) (bool, error) {
	if s == nil {
		return false, errors.New("workspace store unavailable")
	}
	cleanID := strings.TrimSpace(id)
	s.pluginsMu.Lock()
	doc, err := s.loadDataPluginsLocked()
	if err != nil {
		s.pluginsMu.Unlock()
		return false, err
	}
	idx := -1
	for i := range doc.Plugins {
		if doc.Plugins[i].ID == cleanID {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.pluginsMu.Unlock()
		return false, nil
	}
	if doc.Plugins[idx].BuiltIn {
		s.pluginsMu.Unlock()
		return false, errors.New("built-in plugins cannot be deleted; disable them instead")
	}
	removed := doc.Plugins[idx]
	doc.Plugins = append(doc.Plugins[:idx], doc.Plugins[idx+1:]...)
	if err := s.saveDataPluginsLocked(doc); err != nil {
		s.pluginsMu.Unlock()
		return false, err
	}
	s.pluginsMu.Unlock()

	if removed.Category == PluginCatNews || removed.Category == PluginCatPredictions {
		if wi := s.loadedWorldIntelService(); wi != nil {
			_, _ = wi.DeleteSource(ctx, removed.ID)
		}
	}
	return true, nil
}

func (s *Store) syncPluginToWorldIntel(ctx context.Context, rec dataPluginRecord) {
	if s == nil || s.dir == "" {
		return
	}
	wi := s.loadedWorldIntelService()
	if wi == nil {
		if _, err := os.Stat(filepath.Join(s.dir, "worldintel", "intel.sqlite")); err != nil {
			if !rec.Enabled {
				return
			}
		}
		var err error
		wi, err = s.worldIntelService()
		if err != nil || wi == nil {
			return
		}
	}
	kind := rec.Kind
	if kind == "" {
		kind = worldintel.KindRSS
	}
	cat := worldintel.CategoryMacro
	if rec.Category == PluginCatPredictions {
		cat = worldintel.CategoryPrediction
	}
	reg := rec.Region
	if reg == "" {
		reg = worldintel.RegionGlobal
	}
	enabled := rec.Enabled
	_, _ = wi.SaveSource(ctx, worldintel.SourceInput{
		ID:             rec.ID,
		Name:           rec.Name,
		Kind:           kind,
		Endpoint:       rec.Endpoint,
		Category:       cat,
		Region:         reg,
		Enabled:        &enabled,
		AllowLocalhost: rec.AllowLocalhost,
	})
}

func (s *Store) syncWorldIntelSourceState(id string, src worldintel.SourceConfig) {
	if s == nil || s.dir == "" {
		return
	}
	s.pluginsMu.Lock()
	defer s.pluginsMu.Unlock()
	doc, err := s.loadDataPluginsLocked()
	if err != nil {
		return
	}
	for i := range doc.Plugins {
		if doc.Plugins[i].ID == id {
			doc.Plugins[i].Enabled = src.Enabled
			doc.Plugins[i].DataFeedEnabled = src.Enabled
			if src.Endpoint != "" {
				doc.Plugins[i].Endpoint = src.Endpoint
			}
			_ = s.saveDataPluginsLocked(doc)
			return
		}
	}
}

func pluginIDsForMarketVenue(venue string) []string {
	switch strings.ToLower(strings.TrimSpace(venue)) {
	case marketdata.VenueHyperliquid:
		return []string{"hyperliquid"}
	case marketdata.VenueBinance:
		return []string{"binance"}
	case marketdata.VenueBybit:
		return []string{"bybit"}
	case marketdata.VenueOKX:
		return []string{"okx"}
	case marketdata.VenueKraken:
		return []string{"kraken"}
	case marketdata.VenueCoinbase:
		return []string{"coinbase"}
	case marketdata.VenueDeribit:
		return []string{"deribit"}
	case marketdata.VenueLighter:
		return []string{"lighter"}
	case marketdata.VenueSolana, "jupiter":
		return []string{"solana"}
	case marketdata.VenueAlpaca, "equities", "ibkr":
		return []string{"alpaca", "ibkr"}
	case marketdata.VenueFRED:
		return []string{"fred"}
	case marketdata.VenueSEC:
		return []string{"sec_edgar"}
	case marketdata.VenueCustom:
		return []string{"custom"}
	default:
		return []string{strings.ToLower(strings.TrimSpace(venue))}
	}
}

func (s *Store) isMarketVenueEnabled(venue string) bool {
	if s == nil {
		return false
	}
	// Fixture tests stay compatible unless the test explicitly enables strict plugin enforcement.
	if s.providerFixture && !s.enforceDataPlugins {
		return true
	}
	if strings.TrimSpace(venue) == "" {
		return len(s.enabledMarketPluginIDs()) > 0
	}
	s.pluginsMu.Lock()
	defer s.pluginsMu.Unlock()
	doc, err := s.loadDataPluginsLocked()
	if err != nil {
		return false
	}
	candidates := pluginIDsForMarketVenue(venue)
	for _, p := range doc.Plugins {
		if p.Category != PluginCatExchanges {
			continue
		}
		for _, want := range candidates {
			if p.ID == want && p.Enabled && p.DataFeedEnabled {
				return true
			}
		}
	}
	return false
}

func (s *Store) enabledMarketPluginIDs() map[string]bool {
	out := map[string]bool{}
	if s == nil {
		return out
	}
	s.pluginsMu.Lock()
	defer s.pluginsMu.Unlock()
	doc, err := s.loadDataPluginsLocked()
	if err != nil {
		return out
	}
	for _, p := range doc.Plugins {
		if p.Category == PluginCatExchanges && p.Enabled && p.DataFeedEnabled {
			out[p.ID] = true
		}
	}
	return out
}

func (s *Store) enabledWorldIntelPluginCount() int {
	if s == nil {
		return 0
	}
	s.pluginsMu.Lock()
	defer s.pluginsMu.Unlock()
	doc, err := s.loadDataPluginsLocked()
	if err != nil {
		return 0
	}
	count := 0
	for _, p := range doc.Plugins {
		if (p.Category == PluginCatNews || p.Category == PluginCatPredictions) && p.Enabled {
			count++
		}
	}
	return count
}

// resolveSolanaRPC returns the enabled Solana RPC URL for cluster ("mainnet-beta" or "devnet")
// with any stored API key interpolated ONLY inside Go server-side outbound callers.
func (s *Store) resolveSolanaRPC(cluster string) (rawURL string, maskedHost string, ok bool) {
	if s == nil {
		return "", "", false
	}
	targetID := "solana-mainnet"
	if strings.EqualFold(strings.TrimSpace(cluster), "devnet") {
		targetID = "solana-devnet"
	}
	s.pluginsMu.Lock()
	defer s.pluginsMu.Unlock()
	doc, err := s.loadDataPluginsLocked()
	if err != nil {
		return "", "", false
	}
	for _, p := range doc.Plugins {
		if p.Category != PluginCatRPCs || !p.Enabled {
			continue
		}
		if p.ID != targetID && !strings.EqualFold(p.Cluster, cluster) {
			continue
		}
		ep := strings.TrimSpace(p.Endpoint)
		if ep == "" {
			continue
		}
		parsed, err := url.Parse(ep)
		hostOnly := ep
		if err == nil && parsed.Host != "" {
			hostOnly = parsed.Scheme + "://" + parsed.Host
		}
		if p.SealedAPIKey != nil {
			if apiKey, kErr := s.unsealPluginValue(p.ID, "apiKey", p.SealedAPIKey); kErr == nil && apiKey != "" {
				if isTrustedHeliusHost(parsed) && !parsed.Query().Has("api-key") {
					q := parsed.Query()
					q.Set("api-key", apiKey)
					parsed.RawQuery = q.Encode()
					ep = parsed.String()
				} else if isTrustedAlchemyHost(parsed) && !strings.Contains(parsed.Path, "/v2/") {
					parsed.Path = "/v2/" + apiKey
					ep = parsed.String()
				}
			}
		}
		return ep, hostOnly, true
	}
	return "", "", false
}

func isTrustedHeliusHost(u *url.URL) bool {
	if u == nil || !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	h := strings.ToLower(strings.TrimSpace(u.Hostname()))
	return h == "helius-rpc.com" || strings.HasSuffix(h, ".helius-rpc.com")
}

func isTrustedAlchemyHost(u *url.URL) bool {
	if u == nil || !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	h := strings.ToLower(strings.TrimSpace(u.Hostname()))
	return h == "g.alchemy.com" || strings.HasSuffix(h, ".g.alchemy.com")
}

func (s *Store) TestDataPlugin(ctx context.Context, id string) (DataPluginView, error) {
	if s == nil {
		return DataPluginView{}, errors.New("workspace store unavailable")
	}
	cleanID := strings.TrimSpace(id)
	s.pluginsMu.Lock()
	doc, err := s.loadDataPluginsLocked()
	if err != nil {
		s.pluginsMu.Unlock()
		return DataPluginView{}, err
	}
	idx := -1
	var rec dataPluginRecord
	for i := range doc.Plugins {
		if doc.Plugins[i].ID == cleanID {
			idx = i
			rec = doc.Plugins[i]
			break
		}
	}
	if idx < 0 {
		s.pluginsMu.Unlock()
		return DataPluginView{}, os.ErrNotExist
	}
	apiKey, _ := s.unsealPluginValue(rec.ID, "apiKey", rec.SealedAPIKey)
	s.pluginsMu.Unlock()

	start := time.Now()
	nowSec := s.now().UTC().Unix()
	status := "ready"
	probeErr := ""

	if s.providerFixture {
		rec.LastCheckedAt = nowSec
		rec.LastStatus = "ready"
		rec.LastLatencyMs = 4
		rec.LastError = ""
	} else if rec.Endpoint != "" {
		probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		transport := http.RoundTripper(custody.NewSafeTransport(rec.AllowLocalhost))
		if s.marketDataTransport != nil {
			transport = s.marketDataTransport
		}
		client := &http.Client{
			Transport: transport,
			Timeout:   8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("redirects blocked")
			},
		}
		var req *http.Request
		var reqErr error
		if rec.Category == PluginCatRPCs {
			ep := rec.Endpoint
			if parsed, pErr := url.Parse(ep); pErr == nil && apiKey != "" {
				if isTrustedHeliusHost(parsed) && !parsed.Query().Has("api-key") {
					q := parsed.Query()
					q.Set("api-key", apiKey)
					parsed.RawQuery = q.Encode()
					ep = parsed.String()
				} else if isTrustedAlchemyHost(parsed) && !strings.Contains(parsed.Path, "/v2/") {
					parsed.Path = "/v2/" + apiKey
					ep = parsed.String()
				}
			}
			body := `{"jsonrpc":"2.0","id":1,"method":"getHealth"}`
			if rec.Kind == "evm" {
				body = `{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`
			}
			req, reqErr = http.NewRequestWithContext(probeCtx, http.MethodPost, ep, strings.NewReader(body))
			if reqErr == nil {
				req.Header.Set("Content-Type", "application/json")
			}
		} else {
			req, reqErr = http.NewRequestWithContext(probeCtx, http.MethodGet, rec.Endpoint, nil)
		}
		if reqErr != nil {
			status = "error"
			probeErr = "invalid plugin endpoint"
		} else {
			resp, doErr := client.Do(req)
			if doErr != nil {
				status = "error"
				probeErr = "connection probe failed"
			} else {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
				if resp.StatusCode >= 500 || resp.StatusCode == 401 || resp.StatusCode == 403 {
					status = "error"
					probeErr = "HTTP " + http.StatusText(resp.StatusCode)
				}
			}
		}
		rec.LastCheckedAt = nowSec
		rec.LastStatus = status
		rec.LastLatencyMs = time.Since(start).Milliseconds()
		rec.LastError = probeErr
	}

	s.pluginsMu.Lock()
	doc, err = s.loadDataPluginsLocked()
	if err == nil {
		for i := range doc.Plugins {
			if doc.Plugins[i].ID == cleanID {
				doc.Plugins[i].LastCheckedAt = rec.LastCheckedAt
				doc.Plugins[i].LastStatus = rec.LastStatus
				doc.Plugins[i].LastLatencyMs = rec.LastLatencyMs
				doc.Plugins[i].LastError = rec.LastError
				_ = s.saveDataPluginsLocked(doc)
				rec = doc.Plugins[i]
				break
			}
		}
	}
	s.pluginsMu.Unlock()
	return toDataPluginView(rec), nil
}

func (h *HTTPHandler) dataPluginsAPI(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "data-plugins", 120) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/data-plugins"), "/")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	switch {
	case rel == "":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		resp, err := h.store.ListDataPlugins()
		if err != nil {
			writeError(w, 500, "data plugins unavailable")
			return
		}
		writeJSON(w, 200, resp)

	case rel == "custom":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in CustomDataPluginInput
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		created, err := h.store.AddCustomDataPlugin(ctx, in)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, created)

	case strings.HasPrefix(rel, "custom/") && !strings.Contains(strings.TrimPrefix(rel, "custom/"), "/"):
		if r.Method != http.MethodDelete {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimPrefix(rel, "custom/")
		deleted, err := h.store.DeleteCustomDataPlugin(ctx, id)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if !deleted {
			writeError(w, 404, "custom plugin not found")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": true, "id": id})

	case strings.HasSuffix(rel, "/test"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimSuffix(rel, "/test")
		view, err := h.store.TestDataPlugin(ctx, id)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, 404, "data plugin not found")
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, view)

	case !strings.Contains(rel, "/"):
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var patch DataPluginPatch
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &patch) {
			return
		}
		updated, err := h.store.UpdateDataPlugin(ctx, rel, patch)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, 404, "data plugin not found")
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, updated)

	default:
		writeError(w, 404, "unknown data-plugins operation")
	}
}
