package core

// API credentials are separate from wallet authority and metadata snapshots.
// Ordinary admission remains closed by the shared provider release hold.
import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type AIConnectionCommand struct {
	SignInID             string                `json:"signInId,omitempty"`
	Callback             string                `json:"callback,omitempty"`
	Remote               bool                  `json:"remote,omitempty"`
	Pilot                *OpenAIPilotAllowance `json:"pilot,omitempty"`
	Provider             string                `json:"provider,omitempty"`
	Name                 string                `json:"name,omitempty"`
	CredentialMode       string                `json:"credentialMode,omitempty"`
	Revision             int64                 `json:"revision,omitempty"`
	ProfileID            string                `json:"profileId,omitempty"`
	TaskIDs              []string              `json:"taskIds,omitempty"`
	AuthorizationSeconds int64                 `json:"authorizationSeconds,omitempty"`
	Allowance            *TaskAccessAllowance  `json:"allowance,omitempty"`
	ConsentDigest        string                `json:"consentDigest,omitempty"`
	Action               string                `json:"action"`
	Ref                  string                `json:"ref,omitempty"`
	Key                  string                `json:"key,omitempty"`
	Passphrase           string                `json:"passphrase,omitempty"`
}
type AIConnectionView struct {
	AuthMethod             string                `json:"authMethod"`
	PlanUse                bool                  `json:"planUse"`
	PlanPaused             bool                  `json:"planPaused"`
	PlanFailure            string                `json:"planFailure,omitempty"`
	SignInAvailable        bool                  `json:"signInAvailable"`
	SignIn                 *ProviderSignInView   `json:"signIn,omitempty"`
	PilotEnrollment        bool                  `json:"pilotEnrollment,omitempty"`
	PilotAvailable         bool                  `json:"pilotAvailable,omitempty"`
	Pilot                  *OpenAIPilotAllowance `json:"pilot,omitempty"`
	PilotReview            *OpenAIPilotAllowance `json:"pilotReview,omitempty"`
	PilotState             string                `json:"pilotState,omitempty"`
	Name                   string                `json:"name,omitempty"`
	Connections            []AIConnectionView    `json:"connections,omitempty"`
	Catalog                ProviderCatalog       `json:"catalog"`
	CredentialModes        []string              `json:"credentialModes"`
	CredentialMode         string                `json:"credentialMode"`
	TaskAccess             *TaskAccessAllowance  `json:"taskAccess,omitempty"`
	AccessReview           *TaskAccessAllowance  `json:"accessReview,omitempty"`
	AdmissionPolicy        OpenAIModelPolicy     `json:"admissionPolicy"`
	Ref                    string                `json:"ref"`
	Provider               string                `json:"provider"`
	Model                  string                `json:"model"`
	Locked                 bool                  `json:"locked"`
	UnlockExpires          int64                 `json:"unlockExpires"`
	TestOnly               bool                  `json:"testOnly"`
	LiveCallsEnabled       bool                  `json:"liveCallsEnabled"`
	InputMicrousdPerToken  int64                 `json:"inputMicrousdPerToken,omitempty"`
	OutputMicrousdPerToken int64                 `json:"outputMicrousdPerToken,omitempty"`
}
type aiVaultEnvelope struct {
	TestOnly   bool   `json:"testOnly,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Name       string `json:"name,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Version    int    `json:"version"`
	Ref        string `json:"ref"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}
type providerState struct {
	signIn          *providerSignInAttempt
	signInTransport http.RoundTripper // controlled tests only; ordinary constructors never inject it
	signInRenewals  map[string]bool
	// Closed in every runtime constructor. Only controlled tests set this field.
	apiCredentials bool
	nextEpoch      uint64
	accounts       map[string]*providerCredential
	mu             sync.Mutex
}
type providerCredential struct {
	planPaused     bool
	planFailure    string
	superseded     map[string]bool
	discovery      modelDiscovery
	modelRefresh   uint64
	epoch          uint64
	taskAccessLast int64
	taskAccess     *TaskAccessAllowance
	ref            string
	key            []byte
	expires        int64
	digest         [32]byte
}

func testAICredential(key string) bool {
	return strings.HasPrefix(key, "fased-test-") && len(key) >= 24 && len(key) <= 256 && !strings.ContainsAny(key, "\r\n\t ")
}
func (s *Store) aiVaultPath() string { return filepath.Join(s.dir, "ai-credentials.json") }

type aiVaultCollection struct {
	Version  int               `json:"version"`
	Default  string            `json:"default"`
	Accounts []aiVaultEnvelope `json:"accounts"`
}

// Eight maximum sign-in envelopes include base64 expansion and JSON metadata.
// Reader and atomic writer enforce the same serialized byte limit.
const aiVaultMaxSerializedBytes = 256 * 1024

func validConnectionRef(ref string) bool {
	if len(ref) != 43 {
		return false
	}
	for _, r := range ref {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func (s *Store) readAIVaultCollection() (aiVaultCollection, error) {
	var col aiVaultCollection
	info, e := os.Lstat(s.aiVaultPath())
	if e != nil {
		return col, e
	}
	if !info.Mode().IsRegular() || info.Size() > aiVaultMaxSerializedBytes {
		return col, errors.New("invalid credential vault")
	}
	b, e := ReadPrivateFile(s.aiVaultPath())
	if e != nil {
		return col, e
	}
	var legacy aiVaultEnvelope
	if json.Unmarshal(b, &legacy) != nil {
		return col, errors.New("invalid credential vault")
	}
	if legacy.Version == 3 {
		if json.Unmarshal(b, &col) != nil || len(col.Accounts) > 8 {
			return col, errors.New("invalid credential collection")
		}
	} else {
		col = aiVaultCollection{Version: 3, Default: legacy.Ref, Accounts: []aiVaultEnvelope{legacy}}
	}
	if err := validateAIVaultCollection(col); err != nil {
		return col, err
	}
	return col, nil
}
func validateAIVaultCollection(col aiVaultCollection) error {
	if col.Version != 3 || len(col.Accounts) > 8 {
		return errors.New("invalid credential collection")
	}
	seen := map[string]bool{}
	for _, v := range col.Accounts {
		if seen[v.Ref] || !validAIVaultMode(v) || !validConnectionRef(v.Ref) || !validAgentText(v.Name, 80, false) || len(v.Salt) != 16 || len(v.Nonce) != 12 || len(v.Ciphertext) < 16 || len(v.Ciphertext) > aiVaultCiphertextBound(v) {
			return errors.New("invalid credential vault")
		}
		seen[v.Ref] = true
	}
	if col.Default != "" && !seen[col.Default] {
		return errors.New("invalid default connection")
	}
	return nil
}
func (s *Store) writeAIVaultCollection(col aiVaultCollection) error {
	if err := validateAIVaultCollection(col); err != nil {
		return err
	}
	if len(col.Accounts) == 0 {
		return os.Remove(s.aiVaultPath())
	}
	var value any = col
	if len(col.Accounts) == 1 && col.Default == col.Accounts[0].Ref {
		value = col.Accounts[0]
	}
	serialized, err := json.Marshal(value)
	if err != nil || len(serialized)+1 > aiVaultMaxSerializedBytes {
		return errors.New("credential vault serialized size exceeded")
	}
	return WritePrivateJSON(s.aiVaultPath(), value)
}
func (s *Store) readAIVault(refs ...string) (aiVaultEnvelope, error) {
	col, e := s.readAIVaultCollection()
	if e != nil {
		return aiVaultEnvelope{}, e
	}
	ref := col.Default
	if len(refs) > 0 && refs[0] != "" {
		ref = refs[0]
	}
	for _, v := range col.Accounts {
		if v.Ref == ref {
			return v, nil
		}
	}
	return aiVaultEnvelope{}, os.ErrNotExist
}
func (s *Store) credentialLocked(ref string) *providerCredential {
	if ref == "" {
		return &providerCredential{}
	}
	if s.provider.accounts == nil {
		s.provider.accounts = map[string]*providerCredential{}
	}
	if c := s.provider.accounts[ref]; c != nil {
		return c
	}
	s.provider.nextEpoch++
	c := &providerCredential{epoch: s.provider.nextEpoch}
	s.provider.accounts[ref] = c
	return c
}
func (s *Store) clearAIConnectionLocked(refs ...string) {
	ref := ""
	if len(refs) > 0 {
		ref = refs[0]
	}
	if ref == "" {
		return
	}
	s.revokePilotLocked(ref)
	state := s.credentialLocked(ref)
	s.provider.nextEpoch++
	state.epoch = s.provider.nextEpoch
	clear(state.key)
	state.taskAccess = nil
	state.taskAccessLast = 0
	state.key = nil
	state.discovery = modelDiscovery{}
	state.ref = ""
	state.expires = 0
	state.digest = [32]byte{}
}
func (s *Store) lockAIConnection() {
	s.chatEpoch.Add(1)
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	s.cancelProviderSignInLocked()
	s.clearAIConnectionLocked()
	for ref := range s.provider.accounts {
		s.clearAIConnectionLocked(ref)
	}
}
func (s *Store) aiConnectionLocked(refs ...string) (v AIConnectionView, err error) {
	v = AIConnectionView{CredentialMode: "test", AdmissionPolicy: openAIModelPolicy(), Provider: "openai", Model: fixtureAIModel, Locked: true, TestOnly: true, InputMicrousdPerToken: 1, OutputMicrousdPerToken: 4}
	defer func() {
		v.SignInAvailable = s.providerSignInAvailableLocked()
		v.SignIn = s.providerSignInViewLocked()
		v.AuthMethod = "api-key"
		if v.CredentialMode == "openai-signin" {
			v.AuthMethod = "sign-in"
			if policy := chatModelPolicyForMethod(v.Model, "sign-in"); policy != nil {
				v.AdmissionPolicy = *policy
			}
			if state := s.provider.accounts[v.Ref]; state != nil {
				if credential, ok := parseProviderSignInCredential(state.key); ok {
					v.PlanUse = credential.planUse()
				}
			}
		}
		s.pilotViewLocked(&v)
		if s.pilotBindingActiveLocked() {
			v.PilotEnrollment = true
			v.CredentialModes = []string{"openai"}
			v.LiveCallsEnabled = v.Ref == s.pilot.binding.ConnectionRef && s.pilotCurrentLocked()
		}
		d := modelDiscovery{}
		if state := s.provider.accounts[v.Ref]; state != nil {
			d = state.discovery
		}
		v.Catalog = discoveredProviderCatalog(v, d, s.now().Unix())
		if s.pilotBindingActiveLocked() {
			providers := v.Catalog.Providers[:0]
			for _, p := range v.Catalog.Providers {
				if p.ID == "openai" {
					providers = append(providers, p)
				}
			}
			v.Catalog.Providers = providers
			models := v.Catalog.Models[:0]
			for _, m := range v.Catalog.Models {
				if m.ID == s.pilot.binding.Model {
					models = append(models, m)
				}
			}
			v.Catalog.Models = models
		}
		if state := s.provider.accounts[v.Ref]; state != nil {
			for i, m := range v.Catalog.Models {
				if state.superseded[m.ID] {
					v.Catalog.Models[i].Availability = "superseded"
					v.Catalog.Models[i].Selectable = false
				}
			}
		}
	}()
	v.CredentialModes = []string{"test"}
	if s.provider.apiCredentials {
		v.CredentialModes = []string{"openai", "anthropic", "xai"}
	}
	v.LiveCallsEnabled = providerActivationEnabled && s.provider.apiCredentials
	env, e := s.readAIVault(refs...)
	if errors.Is(e, os.ErrNotExist) {
		if len(refs) > 0 {
			s.clearAIConnectionLocked(refs[0])
			delete(s.provider.accounts, refs[0])
		}
		return v, nil
	}
	if e != nil {
		for ref := range s.provider.accounts {
			s.clearAIConnectionLocked(ref)
		}
		return v, errors.New("credential vault unavailable; preserve it before recovery")
	}
	state := s.credentialLocked(env.Ref)
	v.Name = env.Name
	v.Provider = aiVaultProvider(env)
	v.Ref = env.Ref
	v.PlanPaused, v.PlanFailure = state.planPaused, state.planFailure
	v.CredentialMode = aiVaultMode(env)
	v.TestOnly = v.CredentialMode == "test" || env.Version == 5 && env.TestOnly
	if env.Version == 5 {
		v.Model, v.AdmissionPolicy = admittedOpenAIModel, openAIModelPolicy(admittedOpenAIModel)
	}
	if v.Provider != "openai" {
		v.Model = registryForProvider(v.Provider).Models[0].ID
		v.AdmissionPolicy = openAIModelPolicy(v.Model)
	}
	if !v.TestOnly {
		if v.Provider == "openai" {
			v.Model = admittedOpenAIModel
		}
		// Legacy per-token integers describe the fixture tariff only. The versioned
		// admissionPolicy is authoritative for the actual model's fractional tariff.
		v.InputMicrousdPerToken, v.OutputMicrousdPerToken = 0, 0
	}
	if !s.aiVaultModeAllowed(env) && !(s.pilotTargetLocked(v.Ref) && v.CredentialMode == "openai") {
		s.clearAIConnectionLocked(env.Ref)

		return v, nil
	}
	encoded, _ := json.Marshal(env)
	if state.digest == sha256.Sum256(encoded) && state.ref == env.Ref && len(state.key) > 0 {
		if state.expires > s.now().Unix() {
			v.Locked = false
			v.UnlockExpires = state.expires
		}
		if s.taskAccessCurrent(state.taskAccess, state) {
			copy := *state.taskAccess
			copy.Tasks = append([]TaskAccessBinding(nil), copy.Tasks...)
			v.TaskAccess = &copy
		} else {
			state.taskAccess = nil
		}
		if v.Locked && v.TaskAccess == nil {
			s.clearAIConnectionLocked(env.Ref)

		}
	} else {
		s.clearAIConnectionLocked(env.Ref)
	}
	return v, nil
}
func (s *Store) AIConnection(refs ...string) (AIConnectionView, error) {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	v, e := s.aiConnectionLocked(refs...)
	if e != nil {
		return v, e
	}
	col, e := s.readAIVaultCollection()
	if errors.Is(e, os.ErrNotExist) {
		return v, nil
	}
	if e != nil {
		return v, e
	}
	for _, env := range col.Accounts {
		a, err := s.aiConnectionLocked(env.Ref)
		if err != nil {
			return v, err
		}
		v.Connections = append(v.Connections, a)
	}
	return v, nil
}
func (s *Store) aiConnectionValid(ref string) bool {
	if ref == "" {
		return true
	}
	v, e := s.AIConnection(ref)
	return e == nil && v.Ref == ref && !v.Locked && !v.PlanPaused
}

// A plan failure revokes permissions for this account without deleting its
// credentials. Only an explicit successful owner unlock clears this pause.
func (s *Store) pauseSignInAccount(ref, failure string) {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	env, err := s.readAIVault(ref)
	if err != nil || env.Ref != ref || env.Version != 5 || aiVaultMode(env) != "openai-signin" {
		return
	}
	state := s.credentialLocked(ref)
	s.provider.nextEpoch++
	state.epoch = s.provider.nextEpoch
	state.taskAccess, state.taskAccessLast = nil, 0
	state.planPaused = true
	if len(failure) > 2048 {
		failure = "Plan access paused; inspect the request."
	}
	state.planFailure = failure
}
func (s *Store) ChangeAIConnection(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	if strings.HasPrefix(c.Action, "sign-in-") {
		return s.changeProviderSignIn(ctx, c)
	}
	if c.SignInID != "" || c.Callback != "" || c.Remote {
		return AIConnectionView{}, errors.New("sign-in fields require a sign-in operation")
	}
	if pilotAction(c.Action) {
		v, e := s.changeOpenAIPilot(ctx, c)
		if e == nil && c.Action == "pilot-allow" && s.pilotTransport(c.Ref) != nil {
			return s.refreshProviderModels(ctx, c.Ref, s.providerModelTransport(c.Ref))
		}
		return v, e
	}
	if c.Pilot != nil {
		return AIConnectionView{}, errors.New("pilot requires a dedicated review")
	}
	if c.Action == "models-refresh" {
		if c.Provider != "" || c.Ref == "" || c.Key != "" || c.Passphrase != "" || c.Name != "" || c.CredentialMode != "" || c.Allowance != nil || c.ProfileID != "" || c.Revision != 0 || len(c.TaskIDs) > 0 || c.AuthorizationSeconds != 0 || c.ConsentDigest != "" {
			return AIConnectionView{}, errors.New("model refresh accepts an account only")
		}
		return s.refreshProviderModels(ctx, c.Ref, s.providerModelTransport(c.Ref))
	}
	v, err := s.changeAIConnection(ctx, c)
	if err != nil {
		return v, err
	}
	if (c.Action == "save" || c.Action == "add" || c.Action == "unlock") && v.Ref != "" && (v.AuthMethod != "sign-in" || v.PlanUse) && !s.deferPilotDiscovery() {
		return s.refreshProviderModels(ctx, v.Ref, s.providerModelTransport(v.Ref))
	}
	return v, nil
}
func (s *Store) providerModelTransport(ref string) http.RoundTripper {
	if t := s.pilotTransport(ref); t != nil {
		return t
	}
	if s.providerFixture {
		v, err := s.AIConnection(ref)
		if err == nil && v.AuthMethod == "sign-in" {
			return providerSignInModelFixture{}
		}
		return fixtureAITransport{}
	}
	v, err := s.AIConnection(ref)
	if err != nil {
		return nil
	}
	return newNativeProviderTransport(v.Provider)
}

func (s *Store) changeAIConnection(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	// Empty mode preserves existing test-vault clients. Mode selection never
	// enables a provider or changes the saved vault format/credential authority.
	if c.CredentialMode != "" && ((c.Action != "save" && c.Action != "add") || (c.CredentialMode != "test" && c.CredentialMode != "openai" && c.CredentialMode != "anthropic" && c.CredentialMode != "xai")) {
		return AIConnectionView{}, errors.New("credential mode unavailable; real credentials disabled")
	}
	if c.Provider != "" {
		if _, ok := providerDefinitionFor(c.Provider); !ok || (c.Action != "save" && c.Action != "add") {
			return AIConnectionView{}, errors.New("provider selection unavailable")
		}
	}
	// All paths needing both locks acquire SQLite before the in-memory mutex.
	if !taskAccessAction(c.Action) && (c.Allowance != nil || len(c.TaskIDs) > 0 || c.ProfileID != "" || c.AuthorizationSeconds != 0 || c.ConsentDigest != "" || c.Revision != 0) {
		return AIConnectionView{}, errors.New("task credential scope requires a dedicated review")
	}
	if c.Action == "lock" {
		if c.Key != "" || c.Passphrase != "" {
			return AIConnectionView{}, errors.New("lock accepts no secrets")
		}
		s.provider.mu.Lock()
		defer s.provider.mu.Unlock()
		v, e := s.aiConnectionLocked(c.Ref)
		if e != nil {
			return v, e
		}
		if c.Ref != v.Ref {
			return v, ErrConflict
		}
		s.clearAIConnectionLocked(c.Ref)
		return s.aiConnectionLocked(c.Ref)
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return AIConnectionView{}, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "UPDATE instance SET revision=revision WHERE id=1"); e != nil {
		return AIConnectionView{}, metadataWriteError(e)
	}
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	v, e := s.aiConnectionLocked(c.Ref)
	if e != nil {
		return v, e
	}
	if c.Action != "add" && c.Ref != v.Ref {
		return v, ErrConflict
	}
	if c.Action == "save" && v.Ref != "" && c.Provider != "" && c.Provider != v.Provider {
		return v, errors.New("add a separate provider connection")
	}
	if (c.Action == "save" || c.Action == "remove") && v.AuthMethod == "sign-in" {
		return v, errors.New("reconnect this sign-in account or add a separate API-key account")
	}
	if c.Action == "add" {
		if c.Ref != "" {
			return v, ErrConflict
		}
		v = AIConnectionView{}
	}
	if taskAccessAction(c.Action) {
		if c.Action != "task-access-revoke" && !s.aiCredentialModeAllowed(v.CredentialMode) && !(s.pilotTargetLocked(v.Ref) && v.CredentialMode == "openai") {
			return v, errors.New("credential mode unavailable; real credentials disabled")
		}
		return s.changeTaskAccess(ctx, tx, c, v)
	}
	if c.Action != "save" && c.Action != "add" && c.Action != "unlock" && c.Action != "remove" {
		return v, errors.New("unknown credential operation")
	}
	provider := c.Provider
	if provider == "" {
		provider = v.Provider
	}
	if provider == "" {
		provider = "openai"
	}
	mode := c.CredentialMode
	if mode == "" {
		mode = "test"
	}
	if mode != "test" && mode != provider {
		return v, errors.New("credential mode/provider mismatch")
	}
	bootstrap := s.pilotBootstrapAllowedLocked(c, provider, mode)
	if s.pilotBindingActiveLocked() && (c.Action == "save" || c.Action == "add") && !bootstrap {
		return v, errors.New("pilot permits one initial OpenAI account; preserve the existing vault")
	}
	if ((c.Action == "save" || c.Action == "add") && !s.aiCredentialModeAllowed(mode) && !bootstrap) || (c.Action == "unlock" && !s.aiCredentialModeAllowed(v.CredentialMode) && !(s.pilotTargetLocked(v.Ref) && v.CredentialMode == "openai")) {
		return v, errors.New("credential mode unavailable; real credentials disabled")
	}
	if !validPassword(c.Passphrase) || ((c.Action == "save" || c.Action == "add") && !validAICredential(mode, c.Key)) || (c.Action != "save" && c.Action != "add" && c.Key != "") {
		return v, errors.New("invalid credential or vault passphrase")
	}
	if (c.Action != "save" && c.Action != "add") || v.Ref != "" {
		env, err := s.readAIVault(c.Ref)
		if err != nil {
			return v, errors.New("credential vault unavailable")
		}
		plain, err := s.openAIVault(env, c.Passphrase)
		defer clear(plain)
		if err != nil {
			return v, errors.New("vault unlock failed")
		}
		if c.Action == "unlock" {
			s.clearAIConnectionLocked(env.Ref)
			state := s.credentialLocked(env.Ref)
			encoded, _ := json.Marshal(env)
			state.digest = sha256.Sum256(encoded)
			state.ref, state.key, state.expires = env.Ref, append([]byte(nil), plain...), s.now().Add(15*time.Minute).Unix()
			state.planPaused, state.planFailure = false, ""
			return s.aiConnectionLocked(env.Ref)
		}
	}
	col, err := s.readAIVaultCollection()
	if errors.Is(err, os.ErrNotExist) {
		col = aiVaultCollection{Version: 3}
	} else if err != nil {
		return v, err
	}
	if c.Action == "remove" {
		kept := []aiVaultEnvelope{}
		for _, a := range col.Accounts {
			if a.Ref != c.Ref {
				kept = append(kept, a)
			}
		}
		col.Accounts = kept
		if col.Default == c.Ref {
			col.Default = ""
		}
		if e = s.writeAIVaultCollection(col); e != nil {
			return v, e
		}
		s.clearAIConnectionLocked(c.Ref)
		delete(s.provider.accounts, c.Ref)
		return s.aiConnectionLocked()
	}
	if (c.Action == "add" || c.Ref == "") && len(col.Accounts) >= 8 {
		return v, errors.New("connection limit reached")
	}
	if !validAgentText(c.Name, 80, false) {
		return v, errors.New("invalid connection name")
	}
	env := aiVaultEnvelope{Name: strings.TrimSpace(c.Name), Version: 1, Salt: make([]byte, 16), Nonce: make([]byte, 12)}
	if mode == "openai" {
		env.Version, env.Mode = 2, mode
	}
	if provider != "openai" {
		env.Version, env.Provider, env.Mode = 4, provider, mode
	}
	if env.Ref, e = randomToken(); e != nil {
		return v, e
	}
	if bootstrap {
		env.Ref = s.pilot.binding.ConnectionRef
	}
	if _, e = rand.Read(env.Salt); e != nil {
		return v, e
	}
	if _, e = rand.Read(env.Nonce); e != nil {
		return v, e
	}
	key := passwordHash(c.Passphrase, env.Salt)
	block, _ := aes.NewCipher(key)
	clear(key)
	gcm, _ := cipher.NewGCM(block)
	env.Ciphertext = gcm.Seal(nil, env.Nonce, []byte(c.Key), s.aiVaultAAD(env))
	replaced := false
	for i, a := range col.Accounts {
		if a.Ref == c.Ref {
			col.Accounts[i] = env
			replaced = true
		}
	}
	if !replaced {
		col.Accounts = append(col.Accounts, env)
	}
	if col.Default == c.Ref && c.Action != "add" {
		col.Default = env.Ref
	}
	if bootstrap {
		col.Default = env.Ref
	}
	if e = s.writeAIVaultCollection(col); e != nil {
		return v, errors.New("credential storage failed; inspect vault before retry")
	}
	if bootstrap {
		s.pilot.bootstrap = false
	}
	s.clearAIConnectionLocked(c.Ref)
	delete(s.provider.accounts, c.Ref)
	state := s.credentialLocked(env.Ref)
	encoded, _ := json.Marshal(env)
	state.digest = sha256.Sum256(encoded)
	state.ref, state.key, state.expires = env.Ref, []byte(c.Key), s.now().Add(15*time.Minute).Unix()
	return s.aiConnectionLocked(env.Ref)
}

type providerMethodContextKey struct{}

type authenticatedAITransport struct {
	modelsOnly bool
	store      *Store
	ref        string
	next       http.RoundTripper
	request    *AgentAIRequest
}

func (a authenticatedAITransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if a.next == nil || a.modelsOnly && (r.Method != "GET" || r.URL.Path != "/v1/models") {
		return nil, errors.New("provider transport unavailable")
	}
	a.store.provider.mu.Lock()
	v, e := a.store.aiConnectionLocked(a.ref)
	state := a.store.credentialLocked(a.ref)
	allowed := e == nil && !v.Locked && v.Ref == a.ref
	if a.request != nil && a.request.TaskID != "" {
		allowed = e == nil && v.Ref == a.ref && a.store.taskAccessMatches(*a.request, state.taskAccess, true, state)
	}
	if v.PlanPaused && !a.modelsOnly {
		allowed = false
	}
	if !a.modelsOnly && a.request != nil && (policyProvider(a.request.Policy) != v.Provider || !providerRequestAllowed(v.Provider, r)) {
		allowed = false
	}
	if v.AuthMethod == "sign-in" {
		if !((r.Method == "GET" && r.URL.Path == "/v1/models") || (r.Method == "POST" && r.URL.Path == "/v1/responses")) || !a.modelsOnly && (a.request == nil || !signInRequest(*a.request)) {
			allowed = false
		}
	} else if a.request != nil && signInRequest(*a.request) {
		allowed = false
	}
	if v.CredentialMode != "test" && !a.modelsOnly && (a.request == nil || !currentModelRequest(*a.request)) {
		allowed = false
	}
	if !allowed {
		a.store.provider.mu.Unlock()
		return nil, errors.New("connection locked or changed")
	}
	if !a.store.pilotExchangeLocked(a.request, a.ref, r, a.modelsOnly) {
		a.store.provider.mu.Unlock()
		return nil, errors.New("pilot scope or allowance unavailable")
	}
	key := append([]byte(nil), state.key...)
	if v.AuthMethod == "sign-in" {
		credential, ok := parseProviderSignInCredential(key)
		if !ok || !credential.planUse() || credential.ExpiresAt <= a.store.now().Unix() {
			clear(key)
			a.store.provider.mu.Unlock()
			return nil, errors.New("sign-in expired or ChatGPT plan access unavailable; refresh or reconnect")
		}
		clear(key)
		key = []byte(credential.AccessToken)
	}
	a.store.provider.mu.Unlock()
	defer clear(key)
	if !providerRequestAllowed(v.Provider, r) {
		return nil, errors.New("provider origin refused")
	}
	copy := r.Clone(context.WithValue(r.Context(), providerMethodContextKey{}, v.AuthMethod))
	copy.Header = r.Header.Clone()
	copy.Header.Del("Authorization")
	copy.Header.Del("x-api-key")
	copy.Header.Del("anthropic-version")
	if v.Provider == "anthropic" {
		copy.Header.Set("x-api-key", string(key))
		copy.Header.Set("anthropic-version", "2026-10-21")
	} else {
		copy.Header.Set("Authorization", "Bearer "+string(key))
	}
	defer copy.Header.Del("Authorization")
	defer copy.Header.Del("x-api-key")
	res, e := a.next.RoundTrip(copy)
	if e != nil {
		return nil, errors.New("provider transport unavailable")
	}
	// Diagnostic identifiers are untrusted provider input too. Preserve ordinary
	// request IDs, but never persist an echoed credential through an error header.
	res.Header = res.Header.Clone()
	for _, value := range res.Header.Values("X-Request-Id") {
		if bytes.Contains([]byte(value), key) {
			res.Header.Del("X-Request-Id")
			break
		}
	}
	if strings.Split(res.Header.Get("Content-Type"), ";")[0] == "text/event-stream" {
		res.Body = &guardedAIBody{body: res.Body, key: append([]byte(nil), key...)}
		return res, nil
	}
	limit := int64(16384)
	if a.modelsOnly {
		limit = 262144
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, limit+1))
	res.Body.Close()
	if e != nil || int64(len(body)) > limit || bytes.Contains(body, key) {
		return nil, errors.New("provider response refused")
	}
	res.Body = io.NopCloser(bytes.NewReader(body))
	return res, nil
}

func validAIVaultMode(v aiVaultEnvelope) bool {
	if v.Version == 5 {
		return v.Mode == "openai-signin" && v.Provider == "openai"
	}
	if v.TestOnly {
		return false
	}
	if v.Version == 1 {
		return v.Mode == "" && v.Provider == ""
	}
	if v.Version == 2 {
		return v.Mode == "openai" && v.Provider == ""
	}
	return v.Version == 4 && (v.Provider == "anthropic" || v.Provider == "xai") && (v.Mode == "test" || v.Mode == v.Provider)
}
func aiVaultProvider(v aiVaultEnvelope) string {
	if v.Version < 4 {
		return "openai"
	}
	return v.Provider
}
func aiVaultMode(v aiVaultEnvelope) string {
	if v.Version == 1 {
		return "test"
	}
	return v.Mode
}
func (s *Store) aiCredentialModeAllowed(mode string) bool {
	if mode == "openai-signin" {
		return s.provider.apiCredentials || s.providerFixture || s.provider.signInTransport != nil
	}
	_, ok := providerDefinitionFor(mode)
	return mode == "test" || (ok && mode != "" && s.provider.apiCredentials)
}
func validAICredential(mode, key string) bool {
	if mode == "openai-signin" {
		_, ok := parseProviderSignInCredential([]byte(key))
		return ok
	}
	if mode == "test" {
		return testAICredential(key)
	}
	if _, ok := providerDefinitionFor(mode); !ok || mode == "" || len(key) < 24 || len(key) > 256 || strings.HasPrefix(key, "fased-test-") {
		return false
	}
	for _, b := range []byte(key) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}
func (s *Store) aiVaultAAD(v aiVaultEnvelope) []byte {
	if v.Version == 5 {
		kind := "live"
		if v.TestOnly {
			kind = "fixture"
		}
		return []byte("fased-provider-signin-v5:" + s.instanceID + ":" + v.Provider + ":" + v.Mode + ":" + v.Ref + ":" + kind)
	}
	if v.Version == 1 {
		return []byte("fased-api-vault-v1:" + s.instanceID + ":" + v.Ref)
	}
	if v.Version == 2 {
		return []byte("fased-api-vault-v2:" + s.instanceID + ":openai:" + v.Mode + ":" + v.Ref)
	}
	return []byte("fased-api-vault-v4:" + s.instanceID + ":" + v.Provider + ":" + v.Mode + ":" + v.Ref)
}
func (s *Store) openAIVault(v aiVaultEnvelope, passphrase string) ([]byte, error) {
	if !validAIVaultMode(v) {
		return nil, errors.New("vault mode unavailable")
	}
	key := passwordHash(passphrase, v.Salt)
	block, _ := aes.NewCipher(key)
	clear(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, v.Nonce, v.Ciphertext, s.aiVaultAAD(v))
	if err != nil || !validAICredential(aiVaultMode(v), string(plain)) {
		clear(plain)
		return nil, errors.New("vault unlock failed")
	}
	return plain, nil
}

func migrateProviderAccounts(ctx context.Context, db *sql.DB, dir string) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var phase string
	if e = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); e != nil {
		return e
	}
	if phase == "paused" {
		return errors.New("finish schema 22 recovery before provider account migration")
	}
	// Bind existing OpenAI defaults to the legacy identity without rewriting
	// encrypted bytes or changing draft/task/request references.
	col, vaultErr := (&Store{dir: dir}).readAIVaultCollection()
	if vaultErr != nil && !errors.Is(vaultErr, os.ErrNotExist) {
		return vaultErr
	}
	if vaultErr == nil && col.Default != "" {
		d, _, err := readAgents(ctx, tx)
		if err != nil {
			return err
		}
		for i := range d.Profiles {
			if d.Profiles[i].Provider == "openai" && d.Profiles[i].ConnectionRef == "" {
				d.Profiles[i].ConnectionRef = col.Default
			}
		}
		raw, err := json.Marshal(d)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE agent_workspace SET document=? WHERE id=1", string(raw)); err != nil {
			return err
		}
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA user_version=23"); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	return nil
}

// Account-local revocation cannot invalidate another account's Chat consent.
func (s *Store) connectionAccessEpoch(ref string) uint64 {
	if ref == "" {
		return 0
	}
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if state := s.provider.accounts[ref]; state != nil {
		return state.epoch
	}
	return 0
}
