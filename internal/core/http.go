package core

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/auth"
	"github.com/fased-ai/fased-app/internal/httpapi"
	"github.com/fased-ai/fased-app/internal/tailnet"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const sessionCookie = "fased_session"

type HTTPHandler struct {
	wenResearchOrigin string
	wenResearchKey    [32]byte
	agentFixture      bool
	store             *Store
	origins           map[string]bool
	hosts             map[string]bool
	publicHost        string
	remoteIngress     bool
	remoteProxyTrust  bool
	remoteTransport   string
	instanceLocation  string
	instanceOwnerUID  int
	instanceOwnerKind string
	requirePassword   bool
	assets            fs.FS
	authSlots         chan struct{}
	aiSlots           chan struct{}
	privateProof      privateAccessProofCache
	desktopRelease    desktopReleaseCache
	limiter           auth.RateLimiter
	loopbackPeerUID   func(client, server *net.TCPAddr) (int, bool, error)
	localNonce        string
	streamTicketMu    sync.Mutex
	streamTickets     map[string]streamTicketEntry
	activeWSConns     atomic.Int32
}

func NewHTTPHandler(store *Store, baseURL, publicOrigin string, assets fs.FS) (*HTTPHandler, error) {
	h := &HTTPHandler{
		store:            store,
		origins:          map[string]bool{},
		hosts:            map[string]bool{},
		assets:           assets,
		authSlots:        make(chan struct{}, 2),
		aiSlots:          make(chan struct{}, 2),
		instanceLocation: "local",
	}
	if _, err := rand.Read(h.wenResearchKey[:]); err != nil {
		return nil, err
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme != "http" || !isLoopbackHost(base.Hostname()) {
		return nil, errors.New("core listener must use an explicit loopback address")
	}
	h.origins[baseURL] = true
	h.hosts[base.Host] = true
	if base.Hostname() == "127.0.0.1" || base.Hostname() == "::1" {
		local := "http://" + net.JoinHostPort("localhost", base.Port())
		h.origins[local] = true
		h.hosts[net.JoinHostPort("localhost", base.Port())] = true
	}
	if publicOrigin != "" {
		canonOrigin, host, err := httpapi.ValidatePublicOrigin(publicOrigin)
		if err != nil {
			return nil, err
		}
		h.origins[canonOrigin] = true
		h.hosts[host] = true
		h.publicHost = host
	}
	return h, nil
}
func (h *HTTPHandler) SetRequirePassword(v bool) {
	h.requirePassword = v
}
func isLoopbackHost(host string) bool { return httpapi.IsLoopbackHost(host) }
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	httpapi.SetSecurityHeaders(w)
	if h.remoteIngress && !h.legacyRemoteAccess(r.Context()) && r.Context().Value(remoteIdentityKey{}) == nil {
		if r.Host == h.publicHost || (r.Context().Value(privateIPCKey{}) != true && (!h.hosts[r.Host] || r.Method != http.MethodGet || r.URL.Path != "/api/v1/health" || !h.checkLoopbackPeerUID(r, true))) {
			writeError(w, 403, "Private Tailscale access required")
			return
		}
	}
	if h.remoteProxyTrust && r.Host == h.publicHost && r.Context().Value(privateIPCKey{}) != true {
		if _, hasLocal := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr); hasLocal {
			if !isLoopbackRemoteAddr(r.RemoteAddr) {
				writeError(w, 403, "loopback reverse proxy peer required")
				return
			}
		}
		protos := r.Header.Values("X-Forwarded-Proto")
		if r.TLS == nil && (len(protos) != 1 || !strings.EqualFold(strings.TrimSpace(protos[0]), "https")) {
			writeError(w, 403, "HTTPS proxy required")
			return
		}
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
	if !h.hosts[r.Host] {
		writeError(w, http.StatusForbidden, "unexpected request host")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Origin") != "" && !h.origins[r.Header.Get("Origin")] {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		h.api(w, r)
		return
	}
	if r.URL.Path == "/.fased-private-access" {
		h.privateAccessProofPage(w, r)
		return
	}
	h.static(w, r)
}
func (h *HTTPHandler) api(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(custodyAccountsHeader) != "" && !h.custodyAccountsRequest(r) {
		writeError(w, 403, "protected custody account access required")
		return
	}
	if r.Header.Get(custodyOnchainHeader) != "" && !h.custodyOnchainRequest(r) {
		writeError(w, 403, "protected custody Onchain access required")
		return
	}
	if expected := r.Header.Get("X-Fased-Instance"); expected != "" && expected != h.store.instanceID {
		writeError(w, 409, "Remote instance changed")
		return
	}
	switch r.URL.Path {
	case "/api/v1/private-access/control":
		h.privateAccessProofControl(w, r)
		return
	case "/api/v1/private-access/confirm":
		h.privateAccessProofConfirm(w, r)
		return
	case "/api/v1/auth/devices":
		h.remoteDevicesAPI(w, r)
		return
	case "/api/v1/auth/remote":
		h.remoteOwnerAPI(w, r)
		return
	case "/api/v1/auth/pair/issue":
		h.pairIssueAPI(w, r)
		return
	case "/api/v1/auth/pair/manage":
		h.pairManageAPI(w, r)
		return
	case "/api/v1/auth/pair":
		h.pairExchangeAPI(w, r)
		return
	case "/api/v1/auth/remote-setup":
		h.remotePackageSetupAPI(w, r)
		return
	case "/api/v1/auth/local":
		h.localOwnerAPI(w, r)
		return
	case "/api/v1/updates/application/owner":
		h.applicationOwnerAPI(w, r)
		return
	case "/api/v1/health":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		health := h.store.Health()
		location, transport := h.instanceLocation, "loopback"
		if h.publicHost != "" && r.Host == h.publicHost {
			location, transport = "remote", "private-https"
		}
		owner, _ := h.store.remoteOwner(r.Context())
		health.Connection = &ConnectionInfo{RemoteReady: owner.UserID != 0, Location: location, Transport: transport, RemoteTransport: h.effectiveRemoteTransport(), OwnerUID: h.instanceOwnerUID, OwnerKind: h.instanceOwnerKind}
		writeJSON(w, 200, health)
		return
	case "/api/v1/auth/state":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		claimed, err := h.store.Claimed(r.Context())
		if err != nil {
			writeError(w, 500, "state unavailable")
			return
		}
		token := h.session(r)
		valid := h.ownerSession(r, token)
		result := map[string]any{"claimed": claimed, "authenticated": valid}
		if id, ok := r.Context().Value(remoteIdentityKey{}).(tailnet.Identity); ok {
			result["remoteAccess"] = true
			owner, e := h.store.remoteOwner(r.Context())
			if e != nil {
				writeError(w, 500, "Remote owner recovery required")
				return
			}
			result["remoteEnrolled"] = owner.UserID != 0
			result["legacyAuthenticated"] = h.store.Session(r.Context(), token)
			if result["legacyAuthenticated"] == true {
				result["csrfToken"] = csrfToken(token)
			}
			if !valid && id.NodeID != "" && h.store.isRemoteSignedOut(id.NodeID) {
				result["remoteSignedOut"] = true
			}
		}
		if h.localOwnerAllowed() || h.protectedLocalOwnerAllowed() {
			result["localAccess"] = true
			var salt []byte
			if err := h.store.db.QueryRowContext(r.Context(), "SELECT password_salt FROM instance WHERE id=1").Scan(&salt); err != nil {
				writeError(w, 500, "state unavailable")
				return
			}
			passwordAvailable := claimed && !passwordDisabled(salt)
			result["passwordAvailable"] = passwordAvailable
			result["localAutoEnter"] = !h.requirePassword && !passwordAvailable && (h.localPeerVerifiable() || r.Context().Value(privateIPCKey{}) == true)
		}
		if h.instanceLocation == "remote" || h.publicHost != "" || h.remoteIngress {
			hasPW, pwErr := h.store.hasPassword(r.Context())
			if pwErr != nil {
				writeError(w, 500, "state unavailable")
				return
			}
			result["pairingAvailable"] = true
			result["passwordAvailable"] = hasPW
			if transport := h.effectiveRemoteTransport(); transport != "" {
				result["remoteTransport"] = transport
			}
		}
		if valid {
			result["csrfToken"] = csrfToken(token)
		}
		writeJSON(w, 200, result)
		return
	case "/api/v1/auth/native-local":
		h.nativeLocalOwnerAPI(w, r)
		return
	case "/api/v1/auth/claim", "/api/v1/auth/login":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if h.instanceLocation == "local" && r.Context().Value(remoteIdentityKey{}) == nil && !h.checkLoopbackPeerUID(r, false) {
			writeError(w, 403, "Local OS owner access required")
			return
		}
		if !h.mutation(w, r) {
			return
		}
		if !h.allowRate(r, "auth-login", 8) {
			writeError(w, 429, ErrRateLimited.Error())
			return
		}
		select {
		case h.authSlots <- struct{}{}:
			defer func() { <-h.authSlots }()
		default:
			writeError(w, 429, "authentication is busy; retry shortly")
			return
		}
		var request struct {
			Code     string `json:"code"`
			Password string `json:"password"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		var token string
		var err error
		if r.URL.Path == "/api/v1/auth/claim" {
			token, err = h.store.Claim(r.Context(), request.Code, request.Password)
		} else {
			token, err = h.store.Login(r.Context(), request.Password)
		}
		if err != nil {
			writeError(w, 401, "authentication failed; check credentials and claim-code expiry")
			return
		}
		if id, ok := r.Context().Value(remoteIdentityKey{}).(tailnet.Identity); ok && id.NodeID != "" {
			h.store.clearRemoteSignedOut(id.NodeID)
		}
		h.writeSessionResponse(w, r, token)
		return
	}
	// Capture before session authentication/body decoding so an admitted slow
	// request cannot cross sign-out and establish a new credential generation.
	var credentialGeneration uint64
	if r.URL.Path == "/api/v1/accounts/venue-credential" {
		h.store.venueCredentials.mu.Lock()
		credentialGeneration = h.store.venueCredentials.generation
		h.store.venueCredentials.mu.Unlock()
	}
	if r.URL.Path == "/api/v1/stream" && r.Header.Get("Origin") == "" && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		writeError(w, 403, "origin or bearer auth required")
		return
	}
	token := h.resolveStreamSession(r)
	if !h.ownerSession(r, token) {
		writeError(w, 401, "owner authentication required")
		return
	}
	switch r.URL.Path {
	case "/api/v1/wen/sessions":
		h.wenSessionsAPI(w, r, token, "view")
		return
	case "/api/v1/wen/session-runs":
		h.wenFixtureRunsAPI(w, r, token, "view")
	case "/api/v1/wen/session-runs/review", "/api/v1/wen/session-runs/start", "/api/v1/wen/session-runs/control", "/api/v1/wen/session-runs/reconcile":
		h.wenFixtureRunsAPI(w, r, token, strings.TrimPrefix(r.URL.Path, "/api/v1/wen/session-runs/"))
	case "/api/v1/wen/sessions/review", "/api/v1/wen/sessions/create", "/api/v1/wen/sessions/step", "/api/v1/wen/sessions/action-review", "/api/v1/wen/sessions/control":
		h.wenSessionsAPI(w, r, token, strings.TrimPrefix(r.URL.Path, "/api/v1/wen/sessions/"))
		return
	case "/api/v1/wen/research/ai-prepare-next":
		h.wenResearchAIAPI(w, r, token, "next")
		return
	case "/api/v1/wen/research/ai-start":
		h.wenResearchAIAPI(w, r, token, "start")
		return
	case "/api/v1/wen/research/ai-prepare":
		h.wenResearchAIAPI(w, r, token, "prepare")
		return
	case "/api/v1/wen/research/ai-inspect":
		h.wenResearchAIAPI(w, r, token, "inspect")
		return
	case "/api/v1/wen/research/ai-cancel":
		h.wenResearchAIAPI(w, r, token, "cancel")
		return
	case "/api/v1/wen/research/read":
		h.wenResearchReadAPI(w, r, token)
		return
	case "/api/v1/wen/research/proposal-review":
		h.wenRevisionAPI(w, r, token, false, true)
		return
	case "/api/v1/wen/research/proposal-save":
		h.wenRevisionAPI(w, r, token, true, true)
		return
	case "/api/v1/wen/research/revision-review":
		h.wenStrategyRevisionAPI(w, r, token, false)
		return
	case "/api/v1/wen/research/revision-save":
		h.wenStrategyRevisionAPI(w, r, token, true)
		return
	case "/api/v1/wen/research/chat-review":
		h.wenSavedChatReviewAPI(w, r, token)
		return
	case "/api/v1/ai/connection":
		h.aiConnectionAPI(w, r, token)
		return
	case "/api/v1/wen/market-data":
		h.wenMarketDataAPI(w, r)
		return
	case "/api/v1/wen/research":
		h.wenResearchAPI(w, r, token)
		return
	case "/api/v1/agents":
		h.agentsAPI(w, r, token)
		return
	case "/api/v1/updates/application":
		h.applicationUpdatesAPI(w, r, token)
		return
	case "/api/v1/updates/preferences":
		h.updatePreferencesAPI(w, r, token)
		return
	case "/api/v1/updates/status":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		writeJSON(w, 200, h.store.ReadUpdateStatus(r.Context()))
		return
	case "/api/v1/updates/desktop-release":
		h.desktopReleaseAPI(w, r)
		return
	case "/api/v1/updates/backup", "/api/v1/updates/recovery":
		h.backupAPI(w, r, token)
		return
	case "/api/v1/updates/pause":
		h.pauseAPI(w, r, token)
		return
	case "/api/v1/updates/inventory":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		if len(r.URL.Query()) != 1 || len(r.URL.Query()["probe"]) != 1 {
			writeError(w, 400, "one inventory challenge required")
			return
		}
		v, e := h.store.UpdateSnapshot(r.Context(), r.URL.Query().Get("probe"))
		if e != nil {
			writeError(w, 400, "core inventory unavailable")
			return
		}
		writeJSON(w, 200, v)
		return
	case "/api/v1/accounts/venue-credential":
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var input VenueCredentialCommand
		if !decodeJSON(w, r, &input) {
			return
		}
		result, err := h.store.venueCredential(r.Context(), input, &credentialGeneration)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, result)
		return
	case "/api/v1/accounts/rpc-credential":
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var input RPCCredentialCommand
		if !decodeJSON(w, r, &input) {
			return
		}
		result, err := h.store.RPCCredential(r.Context(), input)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, result)
		return
	case "/api/v1/accounts/setup":
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var input AccountSetupCommand
		if !decodeJSON(w, r, &input) {
			return
		}
		v, err := h.store.SetupAccount(r.Context(), input)
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrCorePaused) {
			writeError(w, 409, err.Error())
			return
		}
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, v)
		return
	case "/api/v1/accounts/read":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var input WalletReadCommand
		if !decodeJSON(w, r, &input) {
			return
		}
		result, err := h.store.readWallet(r.Context(), input, func() bool { return h.ownerSession(r, token) })
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, result)
		return
	case "/api/v1/accounts/rpc":
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var input RPCConfigInput
		if !decodeJSON(w, r, &input) {
			return
		}
		result, err := h.store.ConfigureAccountRPC(r.Context(), input)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, result)
		return
	case "/api/v1/accounts/rpc-check":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var input RPCCheckRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		result, err := h.store.CheckAccountRPC(r.Context(), input)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, result)
		return
	case "/api/v1/accounts/portable":
		if r.Method == http.MethodGet {
			v, e := h.store.ExportPortableWallets(r.Context())
			if e != nil {
				writeError(w, 500, "accounts unavailable")
				return
			}
			writeJSON(w, 200, v)
			return
		}
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var input PortableWalletsImportCommand
		if !decodeJSON(w, r, &input) {
			return
		}
		v, e := h.store.ImportPortableWallets(r.Context(), input)
		if errors.Is(e, ErrConflict) || errors.Is(e, ErrCorePaused) {
			writeError(w, 409, e.Error())
			return
		}
		if e != nil {
			writeError(w, 400, e.Error())
			return
		}
		writeJSON(w, 200, v)
		return
	case "/api/v1/accounts":
		if r.Method == http.MethodGet {
			v, e := h.store.Accounts(r.Context())
			if e != nil {
				writeError(w, 500, "accounts unavailable")
				return
			}
			writeJSON(w, 200, v)
			return
		}
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var input AccountsRecord
		if !decodeJSON(w, r, &input) {
			return
		}
		v, e := h.store.UpdateAccounts(r.Context(), input)
		if errors.Is(e, ErrConflict) || errors.Is(e, ErrCorePaused) {
			writeError(w, 409, e.Error())
			return
		}
		if e != nil {
			writeError(w, 400, e.Error())
			return
		}
		writeJSON(w, 200, v)
		return
	case "/api/v1/setup":
		if r.Method == http.MethodGet {
			record, err := h.store.Setup(r.Context())
			if err != nil {
				writeError(w, 500, "setup unavailable")
				return
			}
			writeJSON(w, 200, record)
			return
		}
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		var request SetupRecord
		if !decodeJSON(w, r, &request) {
			return
		}
		if request.Revision < 0 {
			writeError(w, 400, "invalid revision")
			return
		}
		if err := request.Settings.Validate(); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		record, err := h.store.UpdateSetup(r.Context(), request.Revision, request.Settings)
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrCorePaused) {
			writeError(w, 409, err.Error())
			return
		}
		if err != nil {
			writeError(w, 500, "setup update failed")
			return
		}
		writeJSON(w, 200, record)
		return
	case "/api/v1/auth/pair/devices":
		h.pairDevicesAPI(w, r, token)
		return
	case "/api/v1/auth/logout":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.mutation(w, r) || !h.csrf(w, r, token) {
			return
		}
		if err := h.store.Logout(r.Context(), token); err != nil {
			writeError(w, 500, "logout failed")
			return
		}
		if id, ok := r.Context().Value(remoteIdentityKey{}).(tailnet.Identity); ok && id.NodeID != "" {
			h.store.markRemoteSignedOut(id.NodeID)
		}
		h.setCookie(w, r, "", -1)
		writeJSON(w, 200, map[string]bool{"authenticated": false})
		return
	case "/api/v1/signer/status":
		h.signerStatusAPI(w, r)
		return
	default:
		if r.URL.Path == "/api/v1/stream/ticket" {
			h.streamTicketAPI(w, r, token)
			return
		}
		if r.URL.Path == "/api/v1/stream" {
			h.streamWSAPI(w, r)
			return
		}
		if r.URL.Path == "/api/v1/mcp" {
			h.mcpAPI(w, r, token)
			return
		}
		if r.URL.Path == "/api/v1/agents/v2" || strings.HasPrefix(r.URL.Path, "/api/v1/agents/v2/") {
			h.agentsV2API(w, r, token)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/market/") {
			h.marketDataAPI(w, r, token)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/decision/") {
			h.decisionAPI(w, r, token)
			return
		}
		if r.URL.Path == "/api/v1/mcp/clients" || strings.HasPrefix(r.URL.Path, "/api/v1/mcp/clients/") {
			h.mcpClientsAPI(w, r, token)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/intel/") {
			h.worldIntelAPI(w, r, token)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/automation/") {
			h.automationAPI(w, r, token)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/lab/") {
			h.quantLabAPI(w, r, token)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/desk/") {
			h.deskAPI(w, r, token)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/trade/") {
			h.tradeAPI(w, r, token)
			return
		}
		if stage5RouteHook != nil && stage5RouteHook(h, w, r, token) {
			return
		}
		if stage6RouteHook != nil && stage6RouteHook(h, w, r, token) {
			return
		}
		switch r.URL.Path {
		case "/api/v1/execution/status":
			h.executionStatusAPI(w, r)
			return
		case "/api/v1/execution/solana-devnet/rehearse":
			h.solanaDevnetRehearseAPI(w, r, token)
			return
		case "/api/v1/execution/solana-mainnet/execute":
			h.solanaMainnetExecuteAPI(w, r, token)
			return
		case "/api/v1/execution/polymarket/status":
			h.polymarketStatusAPI(w, r)
			return
		case "/api/v1/execution/polymarket/rehearse":
			h.polymarketRehearseAPI(w, r, token)
			return
		case "/api/v1/execution/kalshi/status":
			h.kalshiStatusAPI(w, r)
			return
		case "/api/v1/execution/kalshi/rehearse":
			h.kalshiRehearseAPI(w, r, token)
			return
		case "/api/v1/execution/lighter/status":
			h.lighterStatusAPI(w, r)
			return
		case "/api/v1/execution/lighter/rehearse":
			h.lighterRehearseAPI(w, r, token)
			return
		case "/api/v1/vaults":
			h.agentVaultsAPI(w, r)
			return
		}
		writeError(w, 404, "unknown API operation")
	}
}

var (
	stage5RouteHook func(*HTTPHandler, http.ResponseWriter, *http.Request, string) bool
	stage6RouteHook func(*HTTPHandler, http.ResponseWriter, *http.Request, string) bool
)

func (h *HTTPHandler) ratePeerKey(r *http.Request) string {
	if id, ok := r.Context().Value(remoteIdentityKey{}).(tailnet.Identity); ok && id.NodeID != "" {
		return "node:" + id.NodeID
	}
	if r.Context().Value(privateIPCKey{}) == true {
		return "ipc"
	}
	if h.remoteProxyTrust && r.Host == h.publicHost {
		if _, hasLocal := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr); !hasLocal || isLoopbackRemoteAddr(r.RemoteAddr) {
			if h.remoteTransport == "cloudflare" {
				if cfIP := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cfIP != "" {
					if parsed := net.ParseIP(cfIP); parsed != nil {
						return "proxy:" + parsed.String()
					}
				}
			}
			if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
				for i := len(xff) - 1; i >= 0; i-- {
					parts := strings.Split(xff[i], ",")
					for j := len(parts) - 1; j >= 0; j-- {
						if parsed := net.ParseIP(strings.TrimSpace(parts[j])); parsed != nil {
							return "proxy:" + parsed.String()
						}
					}
				}
			}
			return "proxy:loopback"
		}
	}
	host := r.RemoteAddr
	if parsed, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && parsed != "" {
		host = parsed
	}
	return "ip:" + host
}

func (h *HTTPHandler) allowRate(r *http.Request, bucket string, limit int) bool {
	return h.limiter.Allow(h.store.now(), bucket+"|"+h.ratePeerKey(r), limit)
}

func (h *HTTPHandler) writeSessionResponse(w http.ResponseWriter, r *http.Request, token string) {
	mode := h.sessionMode(r)
	h.setCookie(w, r, token, int(SessionLifetime.Seconds()))
	resp := map[string]any{
		"authenticated": true,
		"csrfToken":     csrfToken(token),
		"sessionMode":   mode,
	}
	if mode != "cookie" {
		resp["sessionToken"] = token
	}
	writeJSON(w, 200, resp)
}

func (h *HTTPHandler) checkLoopbackPeerUID(r *http.Request, allowRoot bool) bool {
	if r.Context().Value(privateIPCKey{}) == true || r.Context().Value(remoteIdentityKey{}) != nil {
		return true
	}
	lookup := h.loopbackPeerUID
	localAddr, hasLocal := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if !hasLocal && lookup == nil {
		return true
	}
	var remoteAddr *net.TCPAddr
	if host, portStr, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			if port, convErr := strconv.Atoi(portStr); convErr == nil {
				remoteAddr = &net.TCPAddr{IP: ip, Port: port}
			}
		}
	}
	if lookup == nil {
		if remoteAddr == nil || !remoteAddr.IP.IsLoopback() {
			return false
		}
		if runtime.GOOS != "linux" {
			return true
		}
		lookup = linuxLoopbackPeerUID
	}
	uid, found, err := lookup(remoteAddr, localAddr)
	if err != nil || !found {
		return false
	}
	if uid == os.Getuid() || (h.instanceOwnerUID > 0 && uid == h.instanceOwnerUID) || (h.store != nil && h.store.custodyOwnerUID > 0 && uid == h.store.custodyOwnerUID) || (allowRoot && uid == 0) {
		return true
	}
	return false
}

func linuxLoopbackPeerUID(clientAddr, serverAddr *net.TCPAddr) (int, bool, error) {
	if runtime.GOOS != "linux" {
		return 0, true, nil
	}
	if clientAddr == nil || serverAddr == nil || clientAddr.Port <= 0 || clientAddr.Port > 65535 || serverAddr.Port <= 0 || serverAddr.Port > 65535 {
		return 0, false, errors.New("invalid loopback TCP endpoint")
	}
	scanProc := func(procPath, wantClient, wantServer string) (int, bool, error) {
		f, err := os.Open(procPath)
		if err != nil {
			return 0, false, err
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		first := true
		for scanner.Scan() {
			if first {
				first = false
				continue
			}
			fields := strings.Fields(scanner.Text())
			if len(fields) < 8 || fields[1] != wantClient || fields[2] != wantServer {
				continue
			}
			if fields[3] != "01" && fields[3] != "04" && fields[3] != "05" && fields[3] != "08" {
				continue
			}
			uid, convErr := strconv.Atoi(fields[7])
			if convErr != nil || uid < 0 {
				return 0, false, errors.New("invalid loopback peer UID")
			}
			return uid, true, nil
		}
		return 0, false, scanner.Err()
	}
	format4 := func(ip4 net.IP, port int) string {
		return fmt.Sprintf("%08X:%04X", binary.NativeEndian.Uint32(ip4.To4()), port)
	}
	format6 := func(ip16 net.IP, port int) string {
		ip := ip16.To16()
		return fmt.Sprintf("%08X%08X%08X%08X:%04X",
			binary.NativeEndian.Uint32(ip[0:4]),
			binary.NativeEndian.Uint32(ip[4:8]),
			binary.NativeEndian.Uint32(ip[8:12]),
			binary.NativeEndian.Uint32(ip[12:16]),
			port)
	}
	if c4, s4 := clientAddr.IP.To4(), serverAddr.IP.To4(); c4 != nil && s4 != nil {
		uid, found, err := scanProc("/proc/net/tcp", format4(c4, clientAddr.Port), format4(s4, serverAddr.Port))
		if found || err != nil && !errors.Is(err, os.ErrNotExist) {
			return uid, found, err
		}
		return scanProc("/proc/net/tcp6", format6(c4.To16(), clientAddr.Port), format6(s4.To16(), serverAddr.Port))
	}
	return scanProc("/proc/net/tcp6", format6(clientAddr.IP.To16(), clientAddr.Port), format6(serverAddr.IP.To16(), serverAddr.Port))
}
func (h *HTTPHandler) mutation(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeError(w, 405, "method not allowed")
		return false
	}
	if !h.origins[r.Header.Get("Origin")] {
		writeError(w, 403, "same-origin request required")
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, 415, "JSON content type required")
		return false
	}
	// Freeze ordinary writes from durable dispatch through recovery. Sessions and
	// protected maintenance remain reachable. The existing metadata fence remains
	// the authority across processes; this closes dispatch-to-fence HTTP races.
	if !strings.HasPrefix(r.URL.Path, "/api/v1/updates/") && !oneOf(r.URL.Path, "/api/v1/login", "/api/v1/logout", "/api/v1/auth/login", "/api/v1/auth/logout", "/api/v1/auth/local", "/api/v1/auth/remote", "/api/v1/auth/pair", "/api/v1/auth/native-local") {
		o, _, e := readApplicationUpdate(r.Context(), h.store.db)
		if e != nil || oneOf(o.Phase, "applying", "recovery") {
			writeError(w, 409, "Update in progress. Inspect recovery before changing this instance.")
			return false
		}
	}
	return true
}
func (h *HTTPHandler) csrf(w http.ResponseWriter, r *http.Request, token string) bool {
	if h.custodyAccountsRequest(r) || h.custodyOnchainRequest(r) {
		return true
	}
	if !auth.VerifyCSRF(r.Header.Get("X-CSRF-Token"), token) {
		writeError(w, 403, "CSRF token required")
		return false
	}
	return true
}
func (h *HTTPHandler) session(r *http.Request) string {
	if h.sessionMode(r) == "cookie" {
		if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
			return cookie.Value
		}
		if r.URL.Path == "/api/v1/mcp" && r.Header.Get("Origin") == "" && r.Header.Get("Sec-Fetch-Site") == "" {
			if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				return strings.TrimPrefix(auth, "Bearer ")
			}
		}
		return ""
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}
func (h *HTTPHandler) setCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	if r.TLS == nil && !(h.publicHost != "" && r.Host == h.publicHost) {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/api/", HttpOnly: true, Secure: r.TLS != nil || (h.publicHost != "" && r.Host == h.publicHost), SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}
func (h *HTTPHandler) sessionMode(r *http.Request) string {
	if r.TLS != nil || (h.publicHost != "" && r.Host == h.publicHost) {
		return "cookie"
	}
	return "bearer"
}
func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	return httpapi.DecodeStrictJSON(w, r, 16*1024, value)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	httpapi.WriteJSON(w, status, value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	httpapi.WriteError(w, status, message)
}
func (h *HTTPHandler) static(w http.ResponseWriter, r *http.Request) {
	httpapi.ServeStaticAsset(w, r, h.assets)
}
