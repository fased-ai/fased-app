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
	"unicode/utf8"

	"github.com/fased-ai/fased-app/internal/providers"
)

// HostCLIStatus describes whether a supported official vendor CLI is installed
// and already signed in under the OS account running Fased Core. Fased never
// reads or scrapes raw CLI token files; authentication stays owned by each CLI.
type HostCLIStatus struct {
	Provider       string   `json:"provider"`
	Binary         string   `json:"binary"`
	Label          string   `json:"label"`
	Transport      string   `json:"transport,omitempty"`
	Version        string   `json:"version,omitempty"`
	BinaryPath     string   `json:"binaryPath,omitempty"`
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
	Transport      string
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
		Transport:      "cli",
		SupportsDevice: true,
		LoginCommand:   "codex login --device-auth",
		InstallHint:    "npm install -g @openai/codex",
		Models:         []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex"},
	},
	{
		Provider:       "anthropic",
		Binary:         "claude",
		Label:          "Claude Code",
		Transport:      "cli",
		SupportsDevice: false,
		LoginCommand:   "claude auth login",
		InstallHint:    "npm install -g @anthropic-ai/claude-code",
		Models:         []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5"},
	},
	{
		Provider:       "google",
		Binary:         "antigravity",
		Label:          "Antigravity ACP",
		Transport:      "acp",
		SupportsDevice: false,
		LoginCommand:   antigravityLoginCommand,
		InstallHint:    antigravityInstallHint,
		Models:         []string{"gemini-4-neon", "gemini-4-carbon", "gemini-3.8-flash", "gemini-pro-orchestrator-neon"},
	},
	{
		Provider:       "google",
		Binary:         "gemini",
		Label:          "Gemini CLI",
		Transport:      "cli",
		SupportsDevice: false,
		LoginCommand:   "gemini",
		InstallHint:    "npm install -g @google/gemini-cli",
		Models:         []string{"gemini-2.5-pro", "gemini-2.5-flash"},
	},
	{
		Provider:       "xai",
		Binary:         "grok",
		Label:          "Grok CLI",
		Transport:      "cli",
		SupportsDevice: true,
		LoginCommand:   "grok login --device-auth",
		InstallHint:    "npm install -g @xai-org/grok-cli",
		Models:         []string{"grok-4.7", "grok-4-fast"},
	},
}

func cliSpecsForProvider(provider string) []cliProviderSpec {
	var out []cliProviderSpec
	for _, spec := range supportedCLIProviders {
		if spec.Provider == provider {
			out = append(out, spec)
		}
	}
	return out
}

func cliSpecForTarget(provider, binary string) (cliProviderSpec, bool) {
	if binary == "" {
		return cliSpecForProvider(provider)
	}
	for _, spec := range supportedCLIProviders {
		if spec.Provider == provider && spec.Binary == binary {
			return spec, true
		}
	}
	return cliProviderSpec{}, false
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
	Version    int    `json:"version"`
	Provider   string `json:"provider"`
	Binary     string `json:"binary"`
	BinaryPath string `json:"binaryPath,omitempty"`
	ConfigDir  string `json:"configDir,omitempty"`
	ShadowHome string `json:"shadowHome,omitempty"`
	LaunchArgs string `json:"launchArgs,omitempty"`
}

func parseCLIProviderCredential(raw []byte) (cliProviderCredential, bool) {
	var c cliProviderCredential
	if len(raw) == 0 || len(raw) > 2048 || !uniqueAIJSON(raw, 2048) || json.Unmarshal(raw, &c) != nil {
		return cliProviderCredential{}, false
	}
	spec, ok := cliSpecForTarget(c.Provider, c.Binary)
	if !ok || c.Version != 1 || c.Binary != spec.Binary {
		return cliProviderCredential{}, false
	}
	c.BinaryPath = strings.TrimSpace(c.BinaryPath)
	c.ConfigDir = strings.TrimSpace(c.ConfigDir)
	c.ShadowHome = strings.TrimSpace(c.ShadowHome)
	c.LaunchArgs = strings.TrimSpace(c.LaunchArgs)
	if c.BinaryPath != "" && !providers.ValidCustomCLIBinaryPathForVendor(c.BinaryPath, spec.Binary) {
		return cliProviderCredential{}, false
	}
	if c.ConfigDir != "" && !validCLIConfigDir(c.ConfigDir) {
		return cliProviderCredential{}, false
	}
	if c.ShadowHome != "" && (spec.Binary != "codex" || !validCLIConfigDir(c.ShadowHome)) {
		return cliProviderCredential{}, false
	}
	if c.LaunchArgs != "" {
		if _, err := parseSafeCLILaunchArgs(c.LaunchArgs); err != nil {
			return cliProviderCredential{}, false
		}
	}
	return c, true
}

func encodeCLIProviderCredential(provider string) ([]byte, error) {
	spec, ok := cliSpecForProvider(provider)
	if !ok {
		return nil, errors.New("unsupported CLI subscription provider")
	}
	return encodeCLIProviderCredentialWithConfig(spec.Provider, spec.Binary, "", "")
}

func encodeCLIProviderCredentialWithConfig(provider, binary, binaryPath, configDir string) ([]byte, error) {
	return encodeCLIProviderCredentialFull(provider, binary, binaryPath, configDir, "", "")
}

func encodeCLIProviderCredentialFull(provider, binary, binaryPath, configDir, shadowHome, launchArgs string) ([]byte, error) {
	spec, ok := cliSpecForTarget(provider, binary)
	if !ok {
		return nil, errors.New("unsupported CLI subscription provider")
	}
	binaryPath = strings.TrimSpace(binaryPath)
	configDir = strings.TrimSpace(configDir)
	shadowHome = strings.TrimSpace(shadowHome)
	launchArgs = strings.TrimSpace(launchArgs)
	if binaryPath != "" && !validCustomCLIBinaryPath(binaryPath) {
		return nil, errors.New("invalid CLI binary path")
	}
	if configDir != "" && !validCLIConfigDir(configDir) {
		return nil, errors.New("invalid CLI config directory")
	}
	if shadowHome != "" {
		if spec.Binary != "codex" {
			return nil, errors.New("shadow home is only supported for Codex CLI")
		}
		if !validCLIConfigDir(shadowHome) {
			return nil, errors.New("invalid CLI shadow home path")
		}
	}
	if launchArgs != "" {
		if !utf8.ValidString(launchArgs) {
			return nil, errors.New("invalid launch arguments")
		}
		if _, err := parseSafeCLILaunchArgs(launchArgs); err != nil {
			return nil, err
		}
	}
	return json.Marshal(cliProviderCredential{
		Version:    1,
		Provider:   spec.Provider,
		Binary:     spec.Binary,
		BinaryPath: binaryPath,
		ConfigDir:  configDir,
		ShadowHome: shadowHome,
		LaunchArgs: launchArgs,
	})
}

type providerCLIRunner interface {
	LookPath(binary string) (string, error)
	Run(ctx context.Context, binary string, args []string, stdin string) (stdout []byte, stderr []byte, exitCode int, err error)
	Start(ctx context.Context, binary string, args []string, stdin string) (stdout io.ReadCloser, stderr io.ReadCloser, wait func() error, kill func(), err error)
	FileExistsNonEmpty(path string) bool
}

type envCLIRunner interface {
	StartWithEnv(ctx context.Context, binary string, args []string, stdin string, extraEnv []string) (stdout io.ReadCloser, stderr io.ReadCloser, wait func() error, kill func(), err error)
}

type osCLIRunner struct{}

func sanitizedCLIEnv() []string {
	return safeCLIEnv("NO_COLOR=1")
}

func (osCLIRunner) LookPath(binary string) (string, error) {
	return exec.LookPath(binary)
}

func (osCLIRunner) Run(ctx context.Context, binary string, args []string, stdin string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	configureSubprocessGroup(cmd)
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
	return osCLIRunner{}.StartWithEnv(ctx, binary, args, stdin, nil)
}

func (osCLIRunner) StartWithEnv(ctx context.Context, binary string, args []string, stdin string, extraEnv []string) (io.ReadCloser, io.ReadCloser, func() error, func(), error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	configureSubprocessGroup(cmd)
	cmd.Env = mergeCLIEnv(sanitizedCLIEnv(), extraEnv)
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
		killSubprocessGroup(cmd)
	}
	return stdout, stderr, cmd.Wait, kill, nil
}

func (osCLIRunner) StartInteractive(ctx context.Context, binary string, args []string, env []string) (io.WriteCloser, io.ReadCloser, io.ReadCloser, func() error, func(), error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	configureSubprocessGroup(cmd)
	if len(env) > 0 {
		cmd.Env = env
	} else {
		cmd.Env = safeCLIEnv()
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, nil, nil, nil, nil, err
	}
	kill := func() {
		killSubprocessGroup(cmd)
	}
	return stdin, stdout, stderr, cmd.Wait, kill, nil
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

func extractCLIVersion(raw string) string {
	cleaned := stripANSI(raw)
	for _, field := range strings.Fields(cleaned) {
		token := strings.Trim(field, "(),:;[]\"'")
		token = strings.TrimPrefix(token, "v")
		if len(token) < 3 || token[0] < '0' || token[0] > '9' || !strings.Contains(token, ".") {
			continue
		}
		return sanitizeDiagValue(token, 32)
	}
	return ""
}

func probeCLIVersion(ctx context.Context, runner providerCLIRunner, binPath string) string {
	vctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	out, errOut, code, runErr := runner.Run(vctx, binPath, []string{"--version"}, "")
	cancel()
	if code != 0 || runErr != nil {
		return ""
	}
	return extractCLIVersion(string(out) + "\n" + string(errOut))
}

func probeSingleCLI(ctx context.Context, runner providerCLIRunner, spec cliProviderSpec) HostCLIStatus {
	return probeSingleCLIWithConfig(ctx, runner, spec, "", "")
}

type runEnvCLIRunner interface {
	RunWithEnv(ctx context.Context, binary string, args []string, stdin string, extraEnv []string) (stdout []byte, stderr []byte, exitCode int, err error)
}

func runCLIWithOptionalEnv(ctx context.Context, runner providerCLIRunner, binary string, args []string, stdin string, extraEnv []string) ([]byte, []byte, int, error) {
	if len(extraEnv) > 0 {
		if er, ok := runner.(runEnvCLIRunner); ok {
			return er.RunWithEnv(ctx, binary, args, stdin, extraEnv)
		}
	}
	return runner.Run(ctx, binary, args, stdin)
}

func probeSingleCLIWithConfig(ctx context.Context, runner providerCLIRunner, spec cliProviderSpec, customBinaryPath, configDir string) HostCLIStatus {
	return probeSingleCLIWithShadow(ctx, runner, spec, customBinaryPath, configDir, "")
}

func probeSingleCLIWithShadow(ctx context.Context, runner providerCLIRunner, spec cliProviderSpec, customBinaryPath, configDir, shadowHome string) HostCLIStatus {
	if spec.Binary == "antigravity" {
		return probeAntigravityACPWithConfig(ctx, runner, spec, customBinaryPath, configDir)
	}
	transport := spec.Transport
	if transport == "" {
		transport = "cli"
	}
	status := HostCLIStatus{
		Provider:       spec.Provider,
		Binary:         spec.Binary,
		Label:          spec.Label,
		Transport:      transport,
		SupportsDevice: spec.SupportsDevice,
		LoginCommand:   spec.LoginCommand,
		InstallHint:    spec.InstallHint,
		Models:         append([]string(nil), spec.Models...),
	}
	lookupTarget := spec.Binary
	if customBinaryPath != "" {
		if !providers.ValidCustomCLIBinaryPathForVendor(customBinaryPath, spec.Binary) {
			return status
		}
		lookupTarget = expandCLIUserPath(customBinaryPath)
	}
	binPath, err := runner.LookPath(lookupTarget)
	if (err != nil || binPath == "") && customBinaryPath != "" {
		if _, isOS := runner.(osCLIRunner); isOS && regularExecutableFile(lookupTarget) {
			binPath, err = lookupTarget, nil
		}
	}
	if err != nil || binPath == "" {
		return status
	}
	status.Installed = true
	status.BinaryPath = binPath
	status.Version = probeCLIVersion(ctx, runner, binPath)
	if spec.Binary == "codex" && strings.TrimSpace(shadowHome) != "" {
		if _, isOS := runner.(osCLIRunner); isOS {
			if _, prepErr := prepareCodexShadowHome(configDir, shadowHome); prepErr != nil {
				return status
			}
		}
	}
	extraEnv := cliConfigEnvWithShadow(spec.Provider, spec.Binary, configDir, shadowHome)

	switch spec.Provider {
	case "anthropic":
		pctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		out, _, _, runErr := runCLIWithOptionalEnv(pctx, runner, binPath, []string{"auth", "status", "--json"}, "", extraEnv)
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
		out, errOut, code, runErr := runCLIWithOptionalEnv(pctx, runner, binPath, []string{"login", "status"}, "", extraEnv)
		cancel()
		combined := string(out) + "\n" + string(errOut)
		lower := strings.ToLower(stripANSI(combined))
		if code == 0 && runErr == nil && strings.Contains(lower, "logged in") {
			status.Authenticated = true
			status.Account = firstStatusLine(combined)
		}

	case "google":
		home, _ := os.UserHomeDir()
		if configDir != "" && validCLIConfigDir(configDir) {
			home = expandCLIUserPath(configDir)
		}
		if home != "" && (runner.FileExistsNonEmpty(filepath.Join(home, ".gemini", "oauth_creds.json")) || runner.FileExistsNonEmpty(filepath.Join(home, ".gemini", "google_accounts.json")) || runner.FileExistsNonEmpty(filepath.Join(home, "oauth_creds.json"))) {
			status.Authenticated = true
			status.Account = "Google CLI"
		} else if os.Getenv("GEMINI_API_KEY") != "" {
			status.Authenticated = true
			status.Account = "GEMINI_API_KEY"
		}

	case "xai":
		pctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		out, errOut, code, runErr := runCLIWithOptionalEnv(pctx, runner, binPath, []string{"auth", "status"}, "", extraEnv)
		cancel()
		if code != 0 || runErr != nil {
			pctx2, cancel2 := context.WithTimeout(ctx, 4*time.Second)
			out, errOut, code, runErr = runCLIWithOptionalEnv(pctx2, runner, binPath, []string{"login", "status"}, "", extraEnv)
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
		transport := spec.Transport
		if transport == "" {
			transport = "cli"
		}
		st := HostCLIStatus{
			Provider:       spec.Provider,
			Binary:         spec.Binary,
			Label:          spec.Label,
			Transport:      transport,
			SupportsDevice: spec.SupportsDevice,
			LoginCommand:   spec.LoginCommand,
			InstallHint:    spec.InstallHint,
			Models:         append([]string(nil), spec.Models...),
		}
		if spec.Binary == "antigravity" {
			if p, err := discoverAntigravityACP(runner, ""); err == nil && p != "" {
				st.Installed = true
				st.BinaryPath = p
			}
		} else if p, err := runner.LookPath(spec.Binary); err == nil && p != "" {
			st.Installed = true
			st.BinaryPath = p
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
		s.provider.cliCacheAt = time.Now()
		return cloneHostCLIStatuses(s.provider.cliCache)
	}
	if !s.provider.cliProbing && (s.provider.cliCacheAt.IsZero() || time.Since(s.provider.cliCacheAt) > 60*time.Second) {
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
	s.provider.cliCacheAt = time.Now()
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
	if !force && len(s.provider.cliCache) > 0 && time.Since(s.provider.cliCacheAt) <= 15*time.Second {
		cached := cloneHostCLIStatuses(s.provider.cliCache)
		s.provider.mu.Unlock()
		return cached
	}
	runner := s.cliRunner()
	s.provider.mu.Unlock()

	results := runParallelCLIProbe(ctx, runner)
	s.provider.mu.Lock()
	s.provider.cliCache = results
	s.provider.cliCacheAt = time.Now()
	s.provider.cliProbing = false
	out := cloneHostCLIStatuses(results)
	s.provider.mu.Unlock()
	return out
}

func validHexAccentColor(s string) bool {
	if s == "" {
		return true
	}
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func cliConfigEnv(provider, binary, configDir string) []string {
	return cliConfigEnvWithShadow(provider, binary, configDir, "")
}

func cliConfigEnvWithShadow(provider, binary, configDir, shadowHome string) []string {
	shadowHome = strings.TrimSpace(shadowHome)
	if binary == "codex" && shadowHome != "" && validCLIConfigDir(shadowHome) {
		return []string{"CODEX_HOME=" + expandCLIUserPath(shadowHome)}
	}
	configDir = strings.TrimSpace(configDir)
	if configDir == "" || !validCLIConfigDir(configDir) {
		return nil
	}
	expanded := expandCLIUserPath(configDir)
	switch {
	case binary == "antigravity":
		return []string{
			"ANTIGRAVITY_HOME=" + expanded,
			"ANTIGRAVITY_CONFIG_DIR=" + expanded,
			"XDG_CONFIG_HOME=" + filepath.Join(expanded, "config"),
			"XDG_DATA_HOME=" + filepath.Join(expanded, "data"),
		}
	case provider == "anthropic":
		return []string{"CLAUDE_CONFIG_DIR=" + expanded}
	case provider == "openai":
		return []string{"CODEX_HOME=" + expanded}
	case provider == "google":
		return []string{"GEMINI_CLI_HOME=" + expanded}
	default:
		return nil
	}
}

func (osCLIRunner) RunWithEnv(ctx context.Context, binary string, args []string, stdin string, extraEnv []string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	configureSubprocessGroup(cmd)
	cmd.Env = mergeCLIEnv(sanitizedCLIEnv(), extraEnv)
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

func mergeCLIEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	overridden := map[string]bool{}
	var validExtra []string
	for _, entry := range extra {
		if k, _, ok := strings.Cut(entry, "="); ok && k != "" {
			overridden[k] = true
			validExtra = append(validExtra, entry)
		}
	}
	dedup := make([]string, 0, len(base)+len(validExtra))
	for _, entry := range base {
		if k, _, ok := strings.Cut(entry, "="); ok && overridden[k] {
			continue
		}
		dedup = append(dedup, entry)
	}
	return append(dedup, validExtra...)
}

func resolveCLIConnectTarget(ctx context.Context, runner providerCLIRunner, provider, binary, binaryPath, configDir string) (cliProviderSpec, HostCLIStatus, bool) {
	return resolveCLIConnectTargetWithShadow(ctx, runner, provider, binary, binaryPath, configDir, "")
}

func resolveCLIConnectTargetWithShadow(ctx context.Context, runner providerCLIRunner, provider, binary, binaryPath, configDir, shadowHome string) (cliProviderSpec, HostCLIStatus, bool) {
	if binary != "" {
		spec, ok := cliSpecForTarget(provider, binary)
		if !ok {
			return cliProviderSpec{}, HostCLIStatus{}, false
		}
		return spec, probeSingleCLIWithShadow(ctx, runner, spec, binaryPath, configDir, shadowHome), true
	}
	specs := cliSpecsForProvider(provider)
	if len(specs) == 0 {
		return cliProviderSpec{}, HostCLIStatus{}, false
	}
	var installedSpec *cliProviderSpec
	var installedStatus HostCLIStatus
	firstSpec := specs[0]
	var firstStatus HostCLIStatus
	for i, sp := range specs {
		st := probeSingleCLIWithShadow(ctx, runner, sp, binaryPath, configDir, shadowHome)
		if i == 0 {
			firstStatus = st
		}
		if st.Installed && st.Authenticated {
			return sp, st, true
		}
		if st.Installed && installedSpec == nil {
			copySpec := sp
			installedSpec = &copySpec
			installedStatus = st
		}
	}
	if installedSpec != nil {
		return *installedSpec, installedStatus, true
	}
	return firstSpec, firstStatus, true
}

func (s *Store) changeProviderCLI(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	if c.Key != "" || c.CredentialMode != "" || c.Pilot != nil || c.Allowance != nil || c.Revision != 0 || c.ProfileID != "" || len(c.TaskIDs) > 0 || c.AuthorizationSeconds != 0 || c.ConsentDigest != "" || c.GoogleProject != "" || c.Disabled != nil {
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
		if !validAgentText(c.Name, 80, false) || !s.validPassphraseOrOS(ctx, c.Passphrase) || !validHexAccentColor(c.AccentColor) {
			return AIConnectionView{}, errors.New("select a supported CLI subscription provider and vault passphrase")
		}
		if c.BinaryPath != "" && !validCustomCLIBinaryPath(c.BinaryPath) {
			return AIConnectionView{}, errors.New("invalid CLI binary path")
		}
		if c.ConfigDir != "" && !validCLIConfigDir(c.ConfigDir) {
			return AIConnectionView{}, errors.New("invalid CLI config directory")
		}
		if c.ShadowHome != "" && !validCLIConfigDir(c.ShadowHome) {
			return AIConnectionView{}, errors.New("invalid CLI shadow home path")
		}
		if strings.TrimSpace(c.LaunchArgs) != "" {
			if _, err := parseSafeCLILaunchArgs(c.LaunchArgs); err != nil {
				return AIConnectionView{}, err
			}
		}
		if s.pilotBindingActive() {
			return AIConnectionView{}, errors.New("pilot permits one initial OpenAI account; preserve the existing vault")
		}
		// Validate custom binary vendor basename BEFORE probing/invoking any binary!
		if preSpec, okSpec := cliSpecForTarget(c.Provider, c.Binary); okSpec && c.BinaryPath != "" {
			if !providers.ValidCustomCLIBinaryPathForVendor(c.BinaryPath, preSpec.Binary) {
				return AIConnectionView{}, errors.New("custom CLI binary path must match the selected vendor binary")
			}
		}
		spec, status, ok := resolveCLIConnectTargetWithShadow(ctx, s.cliRunner(), c.Provider, c.Binary, c.BinaryPath, c.ConfigDir, c.ShadowHome)
		if !ok {
			return AIConnectionView{}, errors.New("select a supported CLI subscription provider and vault passphrase")
		}
		if c.BinaryPath != "" && !providers.ValidCustomCLIBinaryPathForVendor(c.BinaryPath, spec.Binary) {
			return AIConnectionView{}, errors.New("custom CLI binary path must match the selected vendor binary")
		}
		if c.ShadowHome != "" && spec.Binary != "codex" {
			return AIConnectionView{}, errors.New("shadow home is only supported for Codex CLI")
		}
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
		v, err := s.persistCLIConnectionFull(ctx, spec.Provider, spec.Binary, c.BinaryPath, c.ConfigDir, c.ShadowHome, c.LaunchArgs, name, c.AccentColor, c.Ref, c.Passphrase)
		if err != nil {
			return AIConnectionView{}, err
		}
		if c.Ref != "" {
			evictCodexAppServerSessions()
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
	return s.persistCLIConnectionWithSpec(ctx, spec.Provider, spec.Binary, "", "", name, "", existingRef, passphrase)
}

func (s *Store) persistCLIConnectionWithSpec(ctx context.Context, provider, binary, binaryPath, configDir, name, accentColor, existingRef, passphrase string) (AIConnectionView, error) {
	return s.persistCLIConnectionFull(ctx, provider, binary, binaryPath, configDir, "", "", name, accentColor, existingRef, passphrase)
}

func (s *Store) persistCLIConnectionFull(ctx context.Context, provider, binary, binaryPath, configDir, shadowHome, launchArgs, name, accentColor, existingRef, passphrase string) (AIConnectionView, error) {
	spec, ok := cliSpecForTarget(provider, binary)
	if !ok {
		return AIConnectionView{}, errors.New("unsupported CLI subscription provider")
	}
	if !validAgentText(name, 80, false) || !validHexAccentColor(accentColor) {
		return AIConnectionView{}, errors.New("invalid connection name")
	}
	plain, err := encodeCLIProviderCredentialFull(spec.Provider, spec.Binary, binaryPath, configDir, shadowHome, launchArgs)
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
		Provider:    spec.Provider,
		Name:        strings.TrimSpace(name),
		AccentColor: accentColor,
		Mode:        "cli",
		Version:     8,
		TestOnly:    s.providerFixture && !s.provider.apiCredentials,
		Salt:        make([]byte, 16),
		Nonce:       make([]byte, 12),
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
