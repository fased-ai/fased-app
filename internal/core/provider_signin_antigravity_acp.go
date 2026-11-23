package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/providers"
)

type interactiveCLIRunner interface {
	StartInteractive(ctx context.Context, binary string, args []string, env []string) (stdin io.WriteCloser, stdout io.ReadCloser, stderr io.ReadCloser, wait func() error, kill func(), err error)
}

const antigravityLoginCommand = "antigravity acp --login"
const antigravityInstallHint = "Install Antigravity desktop or run antigravity acp"

func validCustomCLIBinaryPath(p string) bool { return providers.ValidCustomCLIBinaryPath(p) }
func validCLIConfigDir(p string) bool        { return providers.ValidCLIConfigDir(p) }
func expandCLIUserPath(p string) string      { return providers.ExpandCLIUserPath(p) }
func isElectronGUIAntigravity(binPath string) bool {
	return providers.IsElectronGUIAntigravity(binPath)
}

func antigravityPlatformArch() string {
	osName := runtime.GOOS
	archName := runtime.GOARCH
	switch osName {
	case "windows":
		osName = "win32"
	}
	switch archName {
	case "amd64":
		archName = "x64"
	case "386":
		archName = "ia32"
	}
	return osName + "-" + archName
}

func antigravityBinaryName() string {
	if runtime.GOOS == "windows" {
		return "antigravity.exe"
	}
	return "antigravity"
}

func antigravityCandidatePaths() []string {
	var candidates []string
	if envBin := strings.TrimSpace(os.Getenv("FASED_ANTIGRAVITY_ACP_BIN")); envBin != "" && validCustomCLIBinaryPath(envBin) {
		candidates = append(candidates, expandCLIUserPath(envBin))
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return candidates
	}
	arch := antigravityPlatformArch()
	binName := antigravityBinaryName()

	// Prefer Fased-managed ACP install, then T3 Code's standalone antigravity-acp runtime.
	fasedRoot := filepath.Join(home, ".fased", "tools", "antigravity-acp", arch)
	candidates = append(candidates,
		filepath.Join(fasedRoot, binName),
	)
	if entries, err := os.ReadDir(filepath.Join(fasedRoot, "versions")); err == nil && len(entries) > 0 {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() {
				candidates = append(candidates, filepath.Join(fasedRoot, "versions", entry.Name(), binName))
			}
		}
	}

	t3Root := filepath.Join(home, ".t3", "tools", "antigravity-acp", arch)
	candidates = append(candidates, filepath.Join(t3Root, binName))
	if entries, err := os.ReadDir(filepath.Join(t3Root, "versions")); err == nil && len(entries) > 0 {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() {
				vDir := filepath.Join(t3Root, "versions", entry.Name())
				candidates = append(candidates, filepath.Join(vDir, binName))
			}
		}
	}
	return candidates
}

func regularExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return false
	}
	return true
}

func discoverAntigravityACP(runner providerCLIRunner, customBinaryPath string) (string, error) {
	if customBinaryPath != "" {
		if !validCustomCLIBinaryPath(customBinaryPath) {
			return "", errors.New("invalid Antigravity ACP binary path")
		}
		expanded := expandCLIUserPath(customBinaryPath)
		if p, err := runner.LookPath(expanded); err == nil && p != "" && !isElectronGUIAntigravity(p) {
			return p, nil
		}
		if _, isOS := runner.(osCLIRunner); isOS && regularExecutableFile(expanded) && !isElectronGUIAntigravity(expanded) {
			return expanded, nil
		}
		return "", errors.New("configured Antigravity ACP binary was not found")
	}

	if _, isOS := runner.(osCLIRunner); isOS {
		for _, cand := range antigravityCandidatePaths() {
			if regularExecutableFile(cand) && !isElectronGUIAntigravity(cand) {
				return cand, nil
			}
		}
	}

	for _, name := range []string{"antigravity-acp", "antigravity"} {
		if p, err := runner.LookPath(name); err == nil && p != "" && !isElectronGUIAntigravity(p) {
			return p, nil
		}
	}
	return "", errors.New("Antigravity ACP binary is not installed on this host")
}

func safeCLIEnv(extra ...string) []string {
	keepPrefix := []string{
		"PATH=",
		"HOME=",
		"USER=",
		"LOGNAME=",
		"LANG=",
		"LC_",
		"TERM=",
		"TMPDIR=",
		"TEMP=",
		"TMP=",
		"XDG_",
		"DBUS_SESSION_BUS_ADDRESS=",
		"DISPLAY=",
		"WAYLAND_DISPLAY=",
		"SSL_CERT_FILE=",
		"SSL_CERT_DIR=",
		"NODE_EXTRA_CA_CERTS=",
		"HTTP_PROXY=",
		"http_proxy=",
		"HTTPS_PROXY=",
		"https_proxy=",
		"NO_PROXY=",
		"no_proxy=",
		"SYSTEMROOT=",
		"COMSPEC=",
		"PATHEXT=",
		"USERPROFILE=",
		"LOCALAPPDATA=",
		"APPDATA=",
	}
	overridden := map[string]bool{}
	var validExtra []string
	for _, entry := range extra {
		if k, _, ok := strings.Cut(entry, "="); ok && k != "" {
			overridden[k] = true
			validExtra = append(validExtra, entry)
		}
	}
	var out []string
	for _, entry := range os.Environ() {
		upper := strings.ToUpper(entry)
		if strings.HasPrefix(upper, "FASED_") {
			continue
		}
		if k, _, ok := strings.Cut(entry, "="); ok && overridden[k] {
			continue
		}
		for _, prefix := range keepPrefix {
			if strings.HasPrefix(entry, prefix) {
				out = append(out, entry)
				break
			}
		}
	}
	return append(out, validExtra...)
}

func antigravityACPEnv(configDir string) []string {
	var extra []string
	if configDir != "" && validCLIConfigDir(configDir) {
		expanded := expandCLIUserPath(configDir)
		extra = append(extra,
			"ANTIGRAVITY_HOME="+expanded,
			"ANTIGRAVITY_CONFIG_DIR="+expanded,
			"XDG_CONFIG_HOME="+filepath.Join(expanded, "config"),
			"XDG_DATA_HOME="+filepath.Join(expanded, "data"),
		)
	}
	return safeCLIEnv(extra...)
}

type acpRPCEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *acpRPCError    `json:"error,omitempty"`
}

type acpRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type acpInitializeResult struct {
	AgentInfo *struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"agentInfo,omitempty"`
}

type acpConfigOption struct {
	ID           string `json:"id"`
	Category     string `json:"category"`
	CurrentValue string `json:"currentValue"`
	Options      []struct {
		Value string `json:"value"`
		Name  string `json:"name"`
	} `json:"options"`
}

type acpSessionNewResult struct {
	SessionID string `json:"sessionId"`
	Models    *struct {
		AvailableModels []struct {
			ModelID string `json:"modelId"`
			Name    string `json:"name"`
		} `json:"availableModels"`
		CurrentModelID string `json:"currentModelId"`
	} `json:"models,omitempty"`
	ConfigOptions []acpConfigOption `json:"configOptions,omitempty"`
	Meta          *struct {
		Antigravity *struct {
			Tier *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"tier,omitempty"`
		} `json:"antigravity,omitempty"`
	} `json:"_meta,omitempty"`
}

type acpPromptResult struct {
	StopReason string `json:"stopReason"`
	Usage      *struct {
		InputTokens      int64 `json:"inputTokens"`
		OutputTokens     int64 `json:"outputTokens"`
		CachedReadTokens int64 `json:"cachedReadTokens"`
		TotalTokens      int64 `json:"totalTokens"`
	} `json:"usage,omitempty"`
}

type acpConn struct {
	stdin   io.WriteCloser
	reader  *bufio.Reader
	writeMu sync.Mutex
	nextID  int64
}

func newACPConn(stdin io.WriteCloser, stdout io.Reader) *acpConn {
	return &acpConn{
		stdin:  stdin,
		reader: bufio.NewReaderSize(stdout, 64*1024),
	}
}

func (c *acpConn) sendJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func (c *acpConn) readEnvelope() (acpRPCEnvelope, error) {
	for {
		line, err := c.reader.ReadBytes('\n')
		if len(line) > 2*1024*1024 {
			return acpRPCEnvelope{}, errors.New("Antigravity ACP message exceeded frame limit")
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			var env acpRPCEnvelope
			if jsonErr := json.Unmarshal(trimmed, &env); jsonErr == nil {
				return env, nil
			}
		}
		if err != nil {
			return acpRPCEnvelope{}, err
		}
	}
}

func (c *acpConn) handlePermissionRequest(env acpRPCEnvelope) error {
	var params struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(env.Params, &params)
	for _, opt := range params.Options {
		if opt.Kind == "reject_once" || opt.Kind == "reject_always" {
			return c.sendJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(env.ID),
				"result": map[string]any{
					"outcome": map[string]any{
						"outcome":  "selected",
						"optionId": opt.OptionID,
					},
				},
			})
		}
	}
	return c.sendJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(env.ID),
		"result": map[string]any{
			"outcome": map[string]any{
				"outcome": "cancelled",
			},
		},
	})
}

func (c *acpConn) handleServerRequest(env acpRPCEnvelope) error {
	if env.Method == "session/request_permission" {
		return c.handlePermissionRequest(env)
	}
	return c.sendJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(env.ID),
		"error": map[string]any{
			"code":    -32601,
			"message": "host tool execution is disabled under non-custodial ACP policy",
		},
	})
}

func (c *acpConn) call(ctx context.Context, method string, params any) (acpRPCEnvelope, error) {
	c.nextID++
	idNum := c.nextID
	wantID := fmt.Sprintf("%d", idNum)
	if err := c.sendJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      idNum,
		"method":  method,
		"params":  params,
	}); err != nil {
		return acpRPCEnvelope{}, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return acpRPCEnvelope{}, err
		}
		env, err := c.readEnvelope()
		if err != nil {
			return acpRPCEnvelope{}, err
		}
		if env.Method != "" && len(env.ID) > 0 {
			_ = c.handleServerRequest(env)
			continue
		}
		if env.Method != "" {
			continue
		}
		if strings.TrimSpace(string(env.ID)) == wantID {
			return env, nil
		}
	}
}

func antigravityModelsFromSession(res acpSessionNewResult) ([]string, map[string]bool) {
	configModelSet := map[string]bool{}
	seen := map[string]bool{}
	var models []string
	addModel := func(id string) {
		id = strings.TrimSpace(id)
		if !validProviderModelID(id) || seen[id] || len(models) >= 32 {
			return
		}
		seen[id] = true
		models = append(models, id)
	}
	for _, opt := range res.ConfigOptions {
		if opt.ID == "model" || opt.Category == "model" {
			for _, item := range opt.Options {
				configModelSet[item.Value] = true
				addModel(item.Value)
			}
		}
	}
	if len(models) == 0 && res.Models != nil {
		for _, item := range res.Models.AvailableModels {
			addModel(item.ModelID)
		}
	}
	return models, configModelSet
}

func resolveAntigravityModelOption(targetModel string, configModelSet map[string]bool) string {
	targetModel = strings.TrimSpace(targetModel)
	if targetModel == "" {
		return ""
	}
	if configModelSet[targetModel] {
		return targetModel
	}
	for _, suffix := range []string{"-xhigh", "-high", "-medium", "-low"} {
		if strings.HasSuffix(targetModel, suffix) {
			trimmed := strings.TrimSuffix(targetModel, suffix)
			if configModelSet[trimmed] {
				return trimmed
			}
		}
	}
	return targetModel
}

func isAntigravityAuthError(errObj *acpRPCError, stderrMsg string) bool {
	if errObj != nil && errObj.Code == -32000 {
		return true
	}
	combined := strings.ToLower(stderrMsg)
	if errObj != nil {
		combined += " " + strings.ToLower(errObj.Message+" "+string(errObj.Data))
	}
	return strings.Contains(combined, "auth") ||
		strings.Contains(combined, "login") ||
		strings.Contains(combined, "no browser") ||
		strings.Contains(combined, "sign in") ||
		strings.Contains(combined, "credentials")
}

func probeAntigravityACP(ctx context.Context, runner providerCLIRunner, spec cliProviderSpec) HostCLIStatus {
	return probeAntigravityACPWithConfig(ctx, runner, spec, "", "")
}

func probeAntigravityACPWithConfig(ctx context.Context, runner providerCLIRunner, spec cliProviderSpec, customBinaryPath, configDir string) HostCLIStatus {
	status := HostCLIStatus{
		Provider:       spec.Provider,
		Binary:         spec.Binary,
		Label:          spec.Label,
		Transport:      "acp",
		SupportsDevice: spec.SupportsDevice,
		LoginCommand:   spec.LoginCommand,
		InstallHint:    spec.InstallHint,
		Models:         append([]string(nil), spec.Models...),
	}
	binPath, err := discoverAntigravityACP(runner, customBinaryPath)
	if err != nil || binPath == "" {
		return status
	}
	status.Installed = true
	status.BinaryPath = binPath

	interactive, ok := runner.(interactiveCLIRunner)
	if !ok {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		out, _, code, runErr := runner.Run(pctx, binPath, []string{"--version"}, "")
		cancel()
		if code == 0 && runErr == nil {
			status.Version = sanitizeDiagValue(firstStatusLine(string(out)), 32)
		}
		home, _ := os.UserHomeDir()
		if home != "" && (runner.FileExistsNonEmpty(filepath.Join(home, ".gemini", "oauth_creds.json")) || runner.FileExistsNonEmpty(filepath.Join(home, ".config", "Antigravity", "app_storage.json"))) {
			status.Authenticated = true
			status.Account = "Google account"
		}
		return status
	}

	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	stdin, stdout, stderr, waitFn, killFn, startErr := interactive.StartInteractive(
		probeCtx,
		binPath,
		[]string{"acp", "--stdio", "--disable-telemetry"},
		antigravityACPEnv(configDir),
	)
	if startErr != nil {
		return status
	}
	doneWatch := make(chan struct{})
	go func() {
		select {
		case <-probeCtx.Done():
			if killFn != nil {
				killFn()
			}
		case <-doneWatch:
		}
	}()
	defer func() {
		close(doneWatch)
		if stdin != nil {
			_ = stdin.Close()
		}
		if killFn != nil {
			killFn()
		}
		if waitFn != nil {
			_ = waitFn()
		}
	}()
	if stderr != nil {
		go func() {
			defer stderr.Close()
			_, _ = io.Copy(io.Discard, io.LimitReader(stderr, 16*1024))
		}()
	}

	conn := newACPConn(stdin, stdout)
	initResp, err := conn.call(probeCtx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]any{"name": "fased", "version": "0.1.35"},
	})
	if err != nil || initResp.Error != nil {
		return status
	}
	var initRes acpInitializeResult
	if json.Unmarshal(initResp.Result, &initRes) == nil && initRes.AgentInfo != nil {
		status.Version = sanitizeDiagValue(strings.TrimPrefix(strings.TrimSpace(initRes.AgentInfo.Version), "v"), 32)
	}

	cwd, err := os.MkdirTemp("", "fased-acp-probe-*")
	if err != nil {
		return status
	}
	defer os.RemoveAll(cwd)

	sessResp, err := conn.call(probeCtx, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	})
	if err != nil || sessResp.Error != nil {
		return status
	}
	var sessRes acpSessionNewResult
	if json.Unmarshal(sessResp.Result, &sessRes) != nil || sessRes.SessionID == "" {
		return status
	}

	if models, _ := antigravityModelsFromSession(sessRes); len(models) > 0 {
		status.Models = models
	}
	if sessRes.Meta != nil && sessRes.Meta.Antigravity != nil && sessRes.Meta.Antigravity.Tier != nil {
		tier := sessRes.Meta.Antigravity.Tier
		if tier.Name != "" || tier.ID != "" {
			status.Authenticated = true
			if tier.Name != "" {
				status.Account = sanitizeDiagValue(tier.Name, 64)
			} else {
				status.Account = "Google account"
			}
		}
	}
	return status
}
