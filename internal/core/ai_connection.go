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
	CredentialMode       string               `json:"credentialMode,omitempty"`
	Revision             int64                `json:"revision,omitempty"`
	ProfileID            string               `json:"profileId,omitempty"`
	TaskIDs              []string             `json:"taskIds,omitempty"`
	AuthorizationSeconds int64                `json:"authorizationSeconds,omitempty"`
	Allowance            *TaskAccessAllowance `json:"allowance,omitempty"`
	ConsentDigest        string               `json:"consentDigest,omitempty"`
	Action               string               `json:"action"`
	Ref                  string               `json:"ref,omitempty"`
	Key                  string               `json:"key,omitempty"`
	Passphrase           string               `json:"passphrase,omitempty"`
}
type AIConnectionView struct {
	CredentialModes        []string             `json:"credentialModes"`
	CredentialMode         string               `json:"credentialMode"`
	TaskAccess             *TaskAccessAllowance `json:"taskAccess,omitempty"`
	AccessReview           *TaskAccessAllowance `json:"accessReview,omitempty"`
	AdmissionPolicy        OpenAIModelPolicy    `json:"admissionPolicy"`
	Ref                    string               `json:"ref"`
	Provider               string               `json:"provider"`
	Model                  string               `json:"model"`
	Locked                 bool                 `json:"locked"`
	UnlockExpires          int64                `json:"unlockExpires"`
	TestOnly               bool                 `json:"testOnly"`
	LiveCallsEnabled       bool                 `json:"liveCallsEnabled"`
	InputMicrousdPerToken  int64                `json:"inputMicrousdPerToken,omitempty"`
	OutputMicrousdPerToken int64                `json:"outputMicrousdPerToken,omitempty"`
}
type aiVaultEnvelope struct {
	Mode       string `json:"mode,omitempty"`
	Version    int    `json:"version"`
	Ref        string `json:"ref"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}
type providerState struct {
	// Closed in every runtime constructor. Only controlled tests set this field.
	apiCredentials bool
	taskAccessLast int64
	taskAccess     *TaskAccessAllowance
	mu             sync.Mutex
	ref            string
	key            []byte
	expires        int64
	digest         [32]byte
}

func testAICredential(key string) bool {
	return strings.HasPrefix(key, "fased-test-") && len(key) >= 24 && len(key) <= 256 && !strings.ContainsAny(key, "\r\n\t ")
}
func (s *Store) aiVaultPath() string { return filepath.Join(s.dir, "ai-credentials.json") }
func (s *Store) readAIVault() (aiVaultEnvelope, error) {
	var v aiVaultEnvelope
	info, e := os.Lstat(s.aiVaultPath())
	if e != nil {
		return v, e
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return v, errors.New("invalid credential vault")
	}
	b, e := ReadPrivateFile(s.aiVaultPath())
	if e != nil {
		return v, e
	}
	if json.Unmarshal(b, &v) != nil || !validAIVaultMode(v) || len(v.Ref) != 43 || len(v.Salt) != 16 || len(v.Nonce) != 12 || len(v.Ciphertext) < 16 || len(v.Ciphertext) > 512 {
		return v, errors.New("invalid credential vault")
	}
	return v, nil
}
func (s *Store) clearAIConnectionLocked() {
	clear(s.provider.key)
	s.provider.taskAccess = nil
	s.provider.taskAccessLast = 0
	s.provider.key = nil
	s.provider.ref = ""
	s.provider.expires = 0
	s.provider.digest = [32]byte{}
}
func (s *Store) lockAIConnection() {
	s.chatEpoch.Add(1)
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	s.clearAIConnectionLocked()
}
func (s *Store) aiConnectionLocked() (AIConnectionView, error) {
	v := AIConnectionView{CredentialMode: "test", AdmissionPolicy: openAIModelPolicy(), Provider: "openai", Model: fixtureAIModel, Locked: true, TestOnly: true, InputMicrousdPerToken: 1, OutputMicrousdPerToken: 4}
	v.CredentialModes = []string{"test"}
	if s.provider.apiCredentials {
		v.CredentialModes = []string{"openai"}
	}
	v.LiveCallsEnabled = providerActivationEnabled && s.provider.apiCredentials
	env, e := s.readAIVault()
	if errors.Is(e, os.ErrNotExist) {
		s.clearAIConnectionLocked()
		return v, nil
	}
	if e != nil {
		s.clearAIConnectionLocked()
		return v, errors.New("credential vault unavailable; preserve it before recovery")
	}
	v.Ref = env.Ref
	v.CredentialMode = aiVaultMode(env)
	v.TestOnly = v.CredentialMode == "test"
	if !v.TestOnly {
		v.Model = admittedOpenAIModel
		// Legacy per-token integers describe the fixture tariff only. The versioned
		// admissionPolicy is authoritative for the actual model's fractional tariff.
		v.InputMicrousdPerToken, v.OutputMicrousdPerToken = 0, 0
	}
	if !s.aiCredentialModeAllowed(v.CredentialMode) {
		s.clearAIConnectionLocked()
		return v, nil
	}
	encoded, _ := json.Marshal(env)
	if s.provider.digest == sha256.Sum256(encoded) && s.provider.ref == env.Ref && len(s.provider.key) > 0 {
		if s.provider.expires > s.now().Unix() {
			v.Locked = false
			v.UnlockExpires = s.provider.expires
		}
		if s.taskAccessCurrent(s.provider.taskAccess) {
			copy := *s.provider.taskAccess
			copy.Tasks = append([]TaskAccessBinding(nil), copy.Tasks...)
			v.TaskAccess = &copy
		} else {
			s.provider.taskAccess = nil
		}
		if v.Locked && v.TaskAccess == nil {
			s.clearAIConnectionLocked()
		}
	} else {
		s.clearAIConnectionLocked()
	}
	return v, nil
}
func (s *Store) AIConnection() (AIConnectionView, error) {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	return s.aiConnectionLocked()
}
func (s *Store) aiConnectionValid(ref string) bool {
	if ref == "" {
		return true
	}
	v, e := s.AIConnection()
	return e == nil && v.Ref == ref && !v.Locked
}
func (s *Store) ChangeAIConnection(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	// Empty mode preserves existing test-vault clients. Mode selection never
	// enables a provider or changes the saved vault format/credential authority.
	if c.CredentialMode != "" && (c.Action != "save" || (c.CredentialMode != "test" && c.CredentialMode != "openai")) {
		return AIConnectionView{}, errors.New("credential mode unavailable; real credentials disabled")
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
		v, e := s.aiConnectionLocked()
		if e != nil {
			return v, e
		}
		if c.Ref != v.Ref {
			return v, ErrConflict
		}
		s.chatEpoch.Add(1)
		s.clearAIConnectionLocked()
		return s.aiConnectionLocked()
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
	v, e := s.aiConnectionLocked()
	if e != nil {
		return v, e
	}
	if c.Ref != v.Ref {
		return v, ErrConflict
	}
	if taskAccessAction(c.Action) {
		if c.Action != "task-access-revoke" && !s.aiCredentialModeAllowed(v.CredentialMode) {
			return v, errors.New("credential mode unavailable; real credentials disabled")
		}
		return s.changeTaskAccess(ctx, tx, c, v)
	}
	if c.Action != "save" && c.Action != "unlock" && c.Action != "remove" {
		return v, errors.New("unknown credential operation")
	}
	mode := c.CredentialMode
	if mode == "" {
		mode = "test"
	}
	if (c.Action == "save" && !s.aiCredentialModeAllowed(mode)) || (c.Action == "unlock" && !s.aiCredentialModeAllowed(v.CredentialMode)) {
		return v, errors.New("credential mode unavailable; real credentials disabled")
	}
	if !validPassword(c.Passphrase) || (c.Action == "save" && !validAICredential(mode, c.Key)) || (c.Action != "save" && c.Key != "") {
		return v, errors.New("invalid credential or vault passphrase")
	}
	if c.Action != "save" || v.Ref != "" {
		env, err := s.readAIVault()
		if err != nil {
			return v, errors.New("credential vault unavailable")
		}
		plain, err := s.openAIVault(env, c.Passphrase)
		defer clear(plain)
		if err != nil {
			return v, errors.New("vault unlock failed")
		}
		if c.Action == "unlock" {
			s.clearAIConnectionLocked()
			encoded, _ := json.Marshal(env)
			s.provider.digest = sha256.Sum256(encoded)
			s.provider.ref, s.provider.key, s.provider.expires = env.Ref, append([]byte(nil), plain...), s.now().Add(15*time.Minute).Unix()
			return s.aiConnectionLocked()
		}
	}
	if c.Action == "remove" {
		s.chatEpoch.Add(1)
		if e = os.Remove(s.aiVaultPath()); e != nil {
			return v, errors.New("credential removal failed")
		}
		s.clearAIConnectionLocked()
		return s.aiConnectionLocked()
	}
	s.chatEpoch.Add(1)
	env := aiVaultEnvelope{Version: 1, Salt: make([]byte, 16), Nonce: make([]byte, 12)}
	if mode == "openai" {
		env.Version, env.Mode = 2, mode
	}
	if env.Ref, e = randomToken(); e != nil {
		return v, e
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
	if e = WritePrivateJSON(s.aiVaultPath(), env); e != nil {
		return v, errors.New("credential storage failed; inspect vault before retry")
	}
	s.clearAIConnectionLocked()
	encoded, _ := json.Marshal(env)
	s.provider.digest = sha256.Sum256(encoded)
	s.provider.ref, s.provider.key, s.provider.expires = env.Ref, []byte(c.Key), s.now().Add(15*time.Minute).Unix()
	return s.aiConnectionLocked()
}

type authenticatedAITransport struct {
	store   *Store
	ref     string
	next    http.RoundTripper
	request *AgentAIRequest
}

func (a authenticatedAITransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if a.next == nil {
		return nil, errors.New("provider transport unavailable")
	}
	a.store.provider.mu.Lock()
	v, e := a.store.aiConnectionLocked()
	allowed := e == nil && !v.Locked && v.Ref == a.ref
	if a.request != nil && a.request.TaskID != "" {
		allowed = e == nil && v.Ref == a.ref && a.store.taskAccessMatches(*a.request, a.store.provider.taskAccess, true)
	}
	if v.CredentialMode == "openai" && (a.request == nil || a.request.Model != admittedOpenAIModel || a.request.Policy == nil) {
		allowed = false
	}
	if !allowed {
		a.store.provider.mu.Unlock()
		return nil, errors.New("connection locked or changed")
	}
	key := append([]byte(nil), a.store.provider.key...)
	a.store.provider.mu.Unlock()
	defer clear(key)
	if !openAIRequestAllowed(r) {
		return nil, errors.New("provider origin refused")
	}
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+string(key))
	defer copy.Header.Del("Authorization")
	res, e := a.next.RoundTrip(copy)
	if e != nil {
		return nil, errors.New("provider transport unavailable")
	}
	if strings.Split(res.Header.Get("Content-Type"), ";")[0] == "text/event-stream" {
		res.Body = &guardedAIBody{body: res.Body, key: append([]byte(nil), key...)}
		return res, nil
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, 16385))
	res.Body.Close()
	if e != nil || len(body) > 16384 || bytes.Contains(body, key) {
		return nil, errors.New("provider response refused")
	}
	res.Body = io.NopCloser(bytes.NewReader(body))
	return res, nil
}

func validAIVaultMode(v aiVaultEnvelope) bool {
	return (v.Version == 1 && v.Mode == "") || (v.Version == 2 && v.Mode == "openai")
}
func aiVaultMode(v aiVaultEnvelope) string {
	if v.Version == 1 {
		return "test"
	}
	return v.Mode
}
func (s *Store) aiCredentialModeAllowed(mode string) bool {
	return mode == "test" || (mode == "openai" && s.provider.apiCredentials)
}
func validAICredential(mode, key string) bool {
	if mode == "test" {
		return testAICredential(key)
	}
	if mode != "openai" || len(key) < 24 || len(key) > 256 || strings.HasPrefix(key, "fased-test-") {
		return false
	}
	// Opaque bounded ASCII token: no guessed provider-specific prefix or account
	// validity claim. Actual account acceptance requires the held live check.
	for _, b := range []byte(key) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}
func (s *Store) aiVaultAAD(v aiVaultEnvelope) []byte {
	if v.Version == 1 {
		return []byte("fased-api-vault-v1:" + s.instanceID + ":" + v.Ref)
	}
	return []byte("fased-api-vault-v2:" + s.instanceID + ":openai:" + v.Mode + ":" + v.Ref)
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
