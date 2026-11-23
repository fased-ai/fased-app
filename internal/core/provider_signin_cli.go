package core

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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// HostCLIStatus describes whether a supported official vendor CLI is installed
// and already signed in under the OS account running Fased Core. Fased never
// reads or scrapes raw CLI token files; authentication stays owned by each CLI.
type HostCLIStatus struct {
	Provider       string   `json:"provider"`
	Binary         string   `json:"binary"`
	Label          string   `json:"label"`
	Installed      bool     `json:"installed"`
	Authenticated  bool     `json:"authenticated"`
	Account        string   `json:"account,omitempty"`
	SupportsDevice bool     `json:"supportsDevice"`
	LoginCommand   string   `json:"loginCommand"`
	InstallHint    string   `json:"installHint"`
	Models         []string `json:"models,omitempty"`
}

// CLIAuthView exposes an active or recently finished headless CLI login attempt
// so web and remote UIs can show a device verification URL and short code.
type CLIAuthView struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	Binary          string `json:"binary"`
	State           string `json:"state"`
	VerificationURL string `json:"verificationUrl,omitempty"`
	UserCode        string `json:"userCode,omitempty"`
	Message         string `json:"message,omitempty"`
	ExpiresAt       int64  `json:"expiresAt"`
}

type cliProviderSpec struct {
	Provider       string
	Binary         string
	Label          string
	SupportsDevice bool
	LoginCommand   string
	InstallHint    string
	Models         []string
}

var supportedCLIProviders = []cliProviderSpec{
	{
		Provider:       "openai",
		Binary:         "codex",
		Label:          "Codex CLI",
		SupportsDevice: true,
		LoginCommand:   "codex login --device-auth",
		InstallHint:    "npm install -g @openai/codex",
		Models:         []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex"},
	},
	{
		Provider:       "anthropic",
		Binary:         "claude",
		Label:          "Claude Code",
		SupportsDevice: false,
		LoginCommand:   "claude auth login",
		InstallHint:    "npm install -g @anthropic-ai/claude-code",
		Models:         []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5"},
	},
	{
		Provider:       "google",
		Binary:         "gemini",
		Label:          "Gemini CLI",
		SupportsDevice: false,
		LoginCommand:   "gemini",
		InstallHint:    "npm install -g @google/gemini-cli",
		Models:         []string{"gemini-2.5-pro", "gemini-2.5-flash"},
	},
	{
		Provider:       "xai",
		Binary:         "grok",
		Label:          "Grok CLI",
		SupportsDevice: true,
		LoginCommand:   "grok login --device-auth",
		InstallHint:    "npm install -g @xai-org/grok-cli",
		Models:         []string{"grok-4.7", "grok-4-fast"},
	},
}

func cliSpecForProvider(provider string) (cliProviderSpec, bool) {
	for _, spec := range supportedCLIProviders {
		if spec.Provider == provider {
			return spec, true
		}
	}
	return cliProviderSpec{}, false
}

func validCLIProvider(provider string) bool {
	_, ok := cliSpecForProvider(provider)
	return ok
}

type cliProviderCredential struct {
	Version  int    `json:"version"`
	Provider string `json:"provider"`
	Binary   string `json:"binary"`
}

func parseCLIProviderCredential(raw []byte) (cliProviderCredential, bool) {
	var c cliProviderCredential
	if len(raw) == 0 || len(raw) > 512 || !uniqueAIJSON(raw, 512) || json.Unmarshal(raw, &c) != nil {
		return cliProviderCredential{}, false
	}
	spec, ok := cliSpecForProvider(c.Provider)
	if !ok || c.Version != 1 || c.Binary != spec.Binary {
		return cliProviderCredential{}, false
	}
	return c, true
}

func encodeCLIProviderCredential(provider string) ([]byte, error) {
	spec, ok := cliSpecForProvider(provider)
	if !ok {
		return nil, errors.New("unsupported CLI subscription provider")
	}
	return json.Marshal(cliProviderCredential{
		Version:  1,
		Provider: spec.Provider,
		Binary:   spec.Binary,
	})
}

type providerCLIRunner interface {
	LookPath(binary string) (string, error)
	Run(ctx context.Context, binary string, args []string, stdin string) (stdout []byte, stderr []byte, exitCode int, err error)
	Start(ctx context.Context, binary string, args []string, stdin string) (stdout io.ReadCloser, stderr io.ReadCloser, wait func() error, kill func(), err error)
	FileExistsNonEmpty(path string) bool
}

type osCLIRunner struct{}

func sanitizedCLIEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "FASED_") || strings.HasPrefix(item, "NO_COLOR=") {
			continue
		}
		env = append(env, item)
	}
	env = append(env, "NO_COLOR=1")
	return env
}

func (osCLIRunner) LookPath(binary string) (string, error) {
	return exec.LookPath(binary)
}

func (osCLIRunner) Run(ctx context.Context, binary string, args []string, stdin string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = sanitizedCLIEnv()
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &outBuf, max: 128 * 1024}
	cmd.Stderr = &limitedWriter{w: &errBuf, max: 32 * 1024}
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	return outBuf.Bytes(), errBuf.Bytes(), code, err
}

func (osCLIRunner) Start(ctx context.Context, binary string, args []string, stdin string) (io.ReadCloser, io.ReadCloser, func() error, func(), error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = sanitizedCLIEnv()
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, nil, nil, nil, err
	}
	kill := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	return stdout, stderr, cmd.Wait, kill, nil
}

func (osCLIRunner) FileExistsNonEmpty(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

type limitedWriter struct {
	w   io.Writer
	n   int
	max int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n >= l.max {
		return len(p), nil
	}
	take := p
	if l.n+len(take) > l.max {
		take = p[:l.max-l.n]
	}
	n, err := l.w.Write(take)
	l.n += n
	if err != nil {
		return n, err
	}
	return len(p), nil
}

func (s *Store) cliRunner() providerCLIRunner {
	if s != nil && s.provider.cliRunner != nil {
		return s.provider.cliRunner
	}
	return osCLIRunner{}
}

func extractJSONObject(raw []byte) []byte {
	start := bytes.IndexByte(raw, '{')
	end := bytes.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return nil
	}
	return raw[start : end+1]
}

func firstStatusLine(raw string) string {
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r", "\n"), "\n") {
		trimmed := sanitizeDiagValue(stripANSI(line), 80)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func probeSingleCLI(ctx context.Context, runner providerCLIRunner, spec cliProviderSpec) HostCLIStatus {
	status := HostCLIStatus{
		Provider:       spec.Provider,
		Binary:         spec.Binary,
		Label:          spec.Label,
		SupportsDevice: spec.SupportsDevice,
		LoginCommand:   spec.LoginCommand,
		InstallHint:    spec.InstallHint,
		Models:         append([]string(nil), spec.Models...),
	}
	binPath, err := runner.LookPath(spec.Binary)
	if err != nil || binPath == "" {
		return status
	}
	status.Installed = true

	switch spec.Provider {
	case "anthropic":
		pctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		out, _, _, runErr := runner.Run(pctx, binPath, []string{"auth", "status", "--json"}, "")
		cancel()
		if obj := extractJSONObject(out); len(obj) > 0 {
			var parsed struct {
				LoggedIn bool   `json:"loggedIn"`
				Email    string `json:"email"`
				Method   string `json:"authMethod"`
				Sub      string `json:"subscriptionType"`
			}
			if json.Unmarshal(obj, &parsed) == nil && parsed.LoggedIn {
				status.Authenticated = true
				switch {
				case parsed.Email != "":
					status.Account = sanitizeDiagValue(parsed.Email, 80)
				case parsed.Sub != "":
					status.Account = sanitizeDiagValue(parsed.Sub, 64)
				case parsed.Method != "":
					status.Account = sanitizeDiagValue(parsed.Method, 64)
				}
			}
		} else if runErr == nil && strings.Contains(strings.ToLower(string(out)), `"loggedin":true`) {
			status.Authenticated = true
		}

	case "openai":
		pctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		out, errOut, code, runErr := runner.Run(pctx, binPath, []string{"login", "status"}, "")
		cancel()
		combined := string(out) + "\n" + string(errOut)
		lower := strings.ToLower(stripANSI(combined))
		if code == 0 && runErr == nil && strings.Contains(lower, "logged in") {
			status.Authenticated = true
			status.Account = firstStatusLine(combined)
		}

	case "google":
		home, _ := os.UserHomeDir()
		if home != "" && (runner.FileExistsNonEmpty(filepath.Join(home, ".gemini", "oauth_creds.json")) || runner.FileExistsNonEmpty(filepath.Join(home, ".gemini", "google_accounts.json"))) {
			status.Authenticated = true
			status.Account = "Google CLI"
		} else if os.Getenv("GEMINI_API_KEY") != "" {
			status.Authenticated = true
			status.Account = "GEMINI_API_KEY"
		}

	case "xai":
		pctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		out, errOut, code, runErr := runner.Run(pctx, binPath, []string{"auth", "status"}, "")
		cancel()
		if code != 0 || runErr != nil {
			pctx2, cancel2 := context.WithTimeout(ctx, 4*time.Second)
			out, errOut, code, runErr = runner.Run(pctx2, binPath, []string{"login", "status"}, "")
			cancel2()
		}
		combined := string(out) + "\n" + string(errOut)
		lower := strings.ToLower(stripANSI(combined))
		if code == 0 && runErr == nil && (strings.Contains(lower, "logged in") || strings.Contains(lower, "authenticated")) {
			status.Authenticated = true
			status.Account = firstStatusLine(combined)
		}
	}
	return status
}

func runParallelCLIProbe(ctx context.Context, runner providerCLIRunner) []HostCLIStatus {
	results := make([]HostCLIStatus, len(supportedCLIProviders))
	var wg sync.WaitGroup
	for i, spec := range supportedCLIProviders {
		wg.Add(1)
		go func(idx int, s cliProviderSpec) {
			defer wg.Done()
			results[idx] = probeSingleCLI(ctx, runner, s)
		}(i, spec)
	}
	wg.Wait()
	return results
}

func fastInitialCLIStatuses(runner providerCLIRunner) []HostCLIStatus {
	out := make([]HostCLIStatus, len(supportedCLIProviders))
	for i, spec := range supportedCLIProviders {
		st := HostCLIStatus{
			Provider:       spec.Provider,
			Binary:         spec.Binary,
			Label:          spec.Label,
			SupportsDevice: spec.SupportsDevice,
			LoginCommand:   spec.LoginCommand,
			InstallHint:    spec.InstallHint,
			Models:         append([]string(nil), spec.Models...),
		}
		if p, err := runner.LookPath(spec.Binary); err == nil && p != "" {
			st.Installed = true
		}
		out[i] = st
	}
	return out
}

func cloneHostCLIStatuses(items []HostCLIStatus) []HostCLIStatus {
	if len(items) == 0 {
		return nil
	}
	out := make([]HostCLIStatus, len(items))
	for i, item := range items {
		item.Models = append([]string(nil), item.Models...)
		out[i] = item
	}
	return out
}

// Caller holds s.provider.mu.
func (s *Store) hostCLIStatusesLocked() []HostCLIStatus {
	if s.provider.cliRunner == nil && (s.providerFixture || !s.provider.apiCredentials) {
		return nil
	}
	runner := s.cliRunner()
	if s.provider.cliRunner != nil && s.provider.cliCacheAt.IsZero() {
		s.provider.cliCache = runParallelCLIProbe(context.Background(), runner)
		s.provider.cliCacheAt = s.now()
		return cloneHostCLIStatuses(s.provider.cliCache)
	}
	if !s.provider.cliProbing && (s.provider.cliCacheAt.IsZero() || s.now().Sub(s.provider.cliCacheAt) > 60*time.Second) {
		s.provider.cliProbing = true
		go s.refreshHostCLICache(runner)
	}
	if len(s.provider.cliCache) > 0 {
		return cloneHostCLIStatuses(s.provider.cliCache)
	}
	return fastInitialCLIStatuses(runner)
}

func (s *Store) refreshHostCLICache(runner providerCLIRunner) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	results := runParallelCLIProbe(ctx, runner)
	s.provider.mu.Lock()
	s.provider.cliCache = results
	s.provider.cliCacheAt = s.now()
	s.provider.cliProbing = false
	s.provider.mu.Unlock()
	signalStoreChange(s)
}

func (s *Store) probeHostCLIs(ctx context.Context, force bool) []HostCLIStatus {
	s.provider.mu.Lock()
	if s.provider.cliRunner == nil && (s.providerFixture || !s.provider.apiCredentials) {
		s.provider.mu.Unlock()
		return nil
	}
	if !force && len(s.provider.cliCache) > 0 && s.now().Sub(s.provider.cliCacheAt) <= 15*time.Second {
		cached := cloneHostCLIStatuses(s.provider.cliCache)
		s.provider.mu.Unlock()
		return cached
	}
	runner := s.cliRunner()
	s.provider.mu.Unlock()

	results := runParallelCLIProbe(ctx, runner)
	s.provider.mu.Lock()
	s.provider.cliCache = results
	s.provider.cliCacheAt = s.now()
	s.provider.cliProbing = false
	out := cloneHostCLIStatuses(results)
	s.provider.mu.Unlock()
	return out
}

func (s *Store) changeProviderCLI(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	if c.Key != "" || c.CredentialMode != "" || c.Pilot != nil || c.Allowance != nil || c.Revision != 0 || c.ProfileID != "" || len(c.TaskIDs) > 0 || c.AuthorizationSeconds != 0 || c.ConsentDigest != "" || c.GoogleProject != "" {
		return AIConnectionView{}, errors.New("CLI subscription operation accepts no key or task grant")
	}
	var phase string
	if err := s.db.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); err != nil {
		return AIConnectionView{}, err
	}
	if phase == "paused" {
		return AIConnectionView{}, ErrCorePaused
	}

	switch c.Action {
	case "cli-probe":
		if !s.cliSubscriptionsAvailable() {
			return AIConnectionView{}, errors.New("CLI subscriptions are held in this build")
		}
		s.probeHostCLIs(ctx, true)
		return s.AIConnection(c.Ref)

	case "cli-auth-start":
		if !s.cliSubscriptionsAvailable() {
			return AIConnectionView{}, errors.New("CLI subscriptions are held in this build")
		}
		return s.startProviderCLIAuth(ctx, c)

	case "cli-auth-cancel":
		return s.cancelProviderCLIAuth(c)

	case "cli-connect":
		if !s.cliSubscriptionsAvailable() {
			return AIConnectionView{}, errors.New("CLI subscriptions are held in this build")
		}
		spec, ok := cliSpecForProvider(c.Provider)
		if !ok || !validAgentText(c.Name, 80, false) || !s.validPassphraseOrOS(ctx, c.Passphrase) {
			return AIConnectionView{}, errors.New("select a supported CLI subscription provider and vault passphrase")
		}
		if s.pilotBindingActive() {
			return AIConnectionView{}, errors.New("pilot permits one initial OpenAI account; preserve the existing vault")
		}
		status := probeSingleCLI(ctx, s.cliRunner(), spec)
		if !status.Installed {
			return AIConnectionView{}, errors.New(spec.Label + " is not installed on this host")
		}
		if !status.Authenticated {
			return AIConnectionView{}, errors.New("sign in to " + spec.Label + " first")
		}
		name := strings.TrimSpace(c.Name)
		if name == "" {
			name = spec.Label
		}
		v, err := s.persistCLIConnection(ctx, spec.Provider, name, c.Ref, c.Passphrase)
		if err != nil {
			return AIConnectionView{}, err
		}
		signalStoreChange(s)
		return v, nil

	default:
		return AIConnectionView{}, errors.New("unknown CLI operation")
	}
}

func (s *Store) cliSubscriptionsAvailable() bool {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	return s.provider.apiCredentials || s.providerFixture || s.provider.cliRunner != nil
}

func (s *Store) pilotBindingActive() bool {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	return s.pilotBindingActiveLocked()
}

func (s *Store) persistCLIConnection(ctx context.Context, provider, name, existingRef, passphrase string) (AIConnectionView, error) {
	spec, ok := cliSpecForProvider(provider)
	if !ok {
		return AIConnectionView{}, errors.New("unsupported CLI subscription provider")
	}
	if !validAgentText(name, 80, false) {
		return AIConnectionView{}, errors.New("invalid connection name")
	}
	plain, err := encodeCLIProviderCredential(spec.Provider)
	if err != nil {
		return AIConnectionView{}, err
	}
	defer clear(plain)

	var preSeal preDerivedKDF
	defer preSeal.clear()
	if passphrase != "" && validPassword(passphrase) {
		if preSeal, err = deriveNewPasswordKey(passphrase); err != nil {
			return AIConnectionView{}, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AIConnectionView{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE instance SET revision=revision WHERE id=1"); err != nil {
		return AIConnectionView{}, metadataWriteError(err)
	}

	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	col, err := s.readAIVaultCollection()
	if errors.Is(err, os.ErrNotExist) {
		col = aiVaultCollection{Version: 3}
	} else if err != nil {
		return AIConnectionView{}, err
	}
	if existingRef == "" && len(col.Accounts) >= aiMaxAccounts {
		return AIConnectionView{}, errors.New("connection limit reached")
	}

	env := aiVaultEnvelope{
		Provider: spec.Provider,
		Name:     strings.TrimSpace(name),
		Mode:     "cli",
		Version:  8,
		TestOnly: s.providerFixture && !s.provider.apiCredentials,
		Salt:     make([]byte, 16),
		Nonce:    make([]byte, 12),
	}
	if env.Ref, err = randomToken(); err != nil {
		return AIConnectionView{}, err
	}
	var key []byte
	var osProtected bool
	if passphrase != "" && len(preSeal.key) == 32 && len(preSeal.salt) == 16 {
		copy(env.Salt, preSeal.salt)
		key, osProtected = preSeal.keyFor(passphrase, env.Salt), false
	} else {
		if _, err = rand.Read(env.Salt); err != nil {
			return AIConnectionView{}, err
		}
		key, osProtected, err = s.vaultSealKey(ctx, passphrase, env.Salt, true)
		if err != nil {
			return AIConnectionView{}, err
		}
	}
	if _, err = rand.Read(env.Nonce); err != nil {
		clear(key)
		return AIConnectionView{}, err
	}
	env.OSProtected = osProtected
	block, _ := aes.NewCipher(key)
	clear(key)
	gcm, _ := cipher.NewGCM(block)
	env.Ciphertext = gcm.Seal(nil, env.Nonce, plain, s.aiVaultAAD(env))

	replaced := false
	for i, a := range col.Accounts {
		if existingRef != "" && a.Ref == existingRef {
			col.Accounts[i] = env
			replaced = true
		}
	}
	if existingRef != "" && !replaced {
		return AIConnectionView{}, ErrConflict
	}
	if !replaced {
		col.Accounts = append(col.Accounts, env)
	}
	if col.Default == "" || col.Default == existingRef {
		col.Default = env.Ref
	}
	if err = s.writeAIVaultCollection(col); err != nil {
		return AIConnectionView{}, errors.New("credential storage failed; inspect vault before retry")
	}
	if existingRef != "" {
		s.clearAIConnectionLocked(existingRef)
		delete(s.provider.accounts, existingRef)
	}
	state := s.credentialLocked(env.Ref)
	encoded, _ := json.Marshal(env)
	state.digest = sha256.Sum256(encoded)
	state.ref, state.key = env.Ref, append([]byte(nil), plain...)
	state.osProtected = env.OSProtected
	state.manuallyLocked = false
	if env.OSProtected {
		state.expires = 0
	} else {
		state.expires = s.now().Add(15 * time.Minute).Unix()
	}
	if s.provider.cliAuth != nil && s.provider.cliAuth.view.Provider == spec.Provider {
		s.cancelCLIAuthLocked()
	}
	out, err := s.aiConnectionLocked(env.Ref)
	if err != nil {
		return AIConnectionView{}, err
	}
	for _, acc := range col.Accounts {
		a, err := s.aiConnectionLocked(acc.Ref)
		if err == nil {
			out.Connections = append(out.Connections, a)
		}
	}
	return out, nil
}
