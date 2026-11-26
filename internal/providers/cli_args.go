package providers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	codexLaunchArgsMaxLen     = 512
	codexLaunchArgsMaxTokens  = 16
	CodexShadowConfigMaxBytes = 64 * 1024
)

var disallowedCLILongFlags = map[string]bool{
	"--dangerously-bypass-approvals-and-sandbox": true,
	"--yolo":                               true,
	"--dangerously-skip-permissions":       true,
	"--allow-dangerously-skip-permissions": true,
	"--full-auto":                          true,
	"--sandbox":                            true,
	"--ask-for-approval":                   true,
	"--approval-mode":                      true,
	"--permission-mode":                    true,
	"--allowedtools":                       true,
	"--allowed-tools":                      true,
	"--disallowedtools":                    true,
	"--disallowed-tools":                   true,
	"--tools":                              true,
	"--add-dir":                            true,
	"--stdio":                              true,
	"--listen":                             true,
	"--output-format":                      true,
	"--input-format":                       true,
	"--json":                               true,
	"--print":                              true,
	"--no-session-persistence":             true,
	"--ephemeral":                          true,
	"--skip-git-repo-check":                true,
	"--remote":                             true,
	"--remote-auth-token-env":              true,
	"--ws-auth":                            true,
	"--ws-token-file":                      true,
	"--ws-token-sha256":                    true,
	"--ws-shared-secret-file":              true,
	"--ws-issuer":                          true,
	"--ws-audience":                        true,
	"--ws-max-clock-skew-seconds":          true,
	"--cd":                                 true,
	"--mcp-config":                         true,
	"--strict-mcp-config":                  true,
	"--system-prompt":                      true,
	"--append-system-prompt":               true,
	"--output-schema":                      true,
	"--startup-client-info":                true,
	"--startup-sign-out":                   true,
	"--code-mode-host":                     true,
	"--print-startup-config":               true,
	"--help":                               true,
	"--version":                            true,
}

var disallowedCLIShortFlags = map[string]bool{
	"-s": true,
	"-a": true,
	"-p": true,
	"-C": true,
	"-h": true,
	"-v": true,
	"-V": true,
}

var allowedCLILongFlags = map[string]bool{
	"--config":                true,
	"--enable":                true,
	"--disable":               true,
	"--profile":               true,
	"--cloud-skills-snapshot": true,
	"--effort":                true,
	"--reasoning-effort":      true,
	"--personality":           true,
	"--service-tier":          true,
	"--model":                 true,
	"--search":                true,
	"--chrome":                true,
}

var allowedCLIShortFlags = map[string]bool{
	"-c": true,
	"-m": true,
}

var valueTakingCLIFlags = map[string]bool{
	"-c":                      true,
	"-m":                      true,
	"--config":                true,
	"--enable":                true,
	"--disable":               true,
	"--profile":               true,
	"--cloud-skills-snapshot": true,
	"--effort":                true,
	"--reasoning-effort":      true,
	"--personality":           true,
	"--service-tier":          true,
	"--model":                 true,
}

var disallowedCodexConfigKeys = []string{
	"sandbox_mode",
	"sandbox_permissions",
	"approval_policy",
	"approvals_reviewer",
	"mcp_servers",
	"shell_environment_policy",
	"tools",
	"experimental_instructions_file",
	"project_doc_fallback_paths",
	"chatgpt_base_url",
	"openai_base_url",
	"base_url",
	"model_providers",
	"cli_auth_credentials_store",
	"auth",
	"execpolicy",
}

// ParseSafeCLILaunchArgs tokenizes optional user launch flags without shell
// expansion and blocks security, sandbox, approval, wire-protocol, and config overrides.
func ParseSafeCLILaunchArgs(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if len(trimmed) > codexLaunchArgsMaxLen || !utf8.ValidString(trimmed) {
		return nil, errors.New("launch arguments exceed length or UTF-8 limit")
	}
	tokens, err := TokenizeSafeCLILaunchArgs(trimmed)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, nil
	}
	if len(tokens) > codexLaunchArgsMaxTokens {
		return nil, errors.New("too many launch arguments")
	}

	expectValueFor := ""
	for i, tok := range tokens {
		if tok == "-" || tok == "--" || strings.HasPrefix(tok, "---") {
			return nil, errors.New("launch argument separator is not allowed")
		}
		if expectValueFor != "" {
			if strings.HasPrefix(tok, "-") {
				return nil, fmt.Errorf("missing value for %s", expectValueFor)
			}
			if expectValueFor == "-c" || expectValueFor == "--config" {
				if err := ValidateSafeCodexConfigOverride(tok); err != nil {
					return nil, err
				}
			}
			expectValueFor = ""
			continue
		}
		if !strings.HasPrefix(tok, "-") {
			return nil, errors.New("launch arguments must be flags only")
		}
		flagName, eqVal, hasEq := strings.Cut(tok, "=")
		lowerFlag := strings.ToLower(flagName)
		if disallowedCLILongFlags[lowerFlag] || disallowedCLIShortFlags[flagName] {
			return nil, fmt.Errorf("disallowed launch flag: %s", flagName)
		}
		if !allowedCLILongFlags[lowerFlag] && !allowedCLIShortFlags[flagName] {
			return nil, fmt.Errorf("unsupported launch flag: %s", flagName)
		}
		if lowerFlag == "-c" || lowerFlag == "--config" {
			if hasEq {
				if err := ValidateSafeCodexConfigOverride(eqVal); err != nil {
					return nil, err
				}
				continue
			}
			if i+1 >= len(tokens) {
				return nil, fmt.Errorf("missing value for %s", flagName)
			}
			expectValueFor = lowerFlag
			continue
		}
		if !hasEq && valueTakingCLIFlags[lowerFlag] {
			if i+1 >= len(tokens) {
				return nil, fmt.Errorf("missing value for %s", flagName)
			}
			expectValueFor = lowerFlag
		}
	}
	return tokens, nil
}

// TokenizeSafeCLILaunchArgs splits safe CLI flags while blocking shell metacharacters and expansions.
func TokenizeSafeCLILaunchArgs(input string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inSingle := false
	inDouble := false
	hasToken := false

	for _, r := range input {
		if r == 0 || r == 0x7f || (r < 0x20 && r != ' ' && r != '\t') {
			return nil, errors.New("launch arguments contain invalid control characters")
		}
		if !inSingle && !inDouble {
			if strings.ContainsRune(";&|`$<>(){}\\*?~", r) {
				return nil, errors.New("launch arguments cannot contain shell metacharacters")
			}
			if r == ' ' || r == '\t' {
				if hasToken {
					if cur.Len() > 256 {
						return nil, errors.New("launch argument token too long")
					}
					tokens = append(tokens, cur.String())
					cur.Reset()
					hasToken = false
				}
				continue
			}
			if r == '\'' {
				inSingle = true
				hasToken = true
				continue
			}
			if r == '"' {
				inDouble = true
				hasToken = true
				continue
			}
			cur.WriteRune(r)
			hasToken = true
			continue
		}
		if inSingle {
			if r == '\'' {
				inSingle = false
				continue
			}
			cur.WriteRune(r)
			continue
		}
		// inDouble: block subshells, backticks, and escapes.
		if strings.ContainsRune("`$\\", r) {
			return nil, errors.New("launch arguments cannot contain shell expansions")
		}
		if r == '"' {
			inDouble = false
			continue
		}
		cur.WriteRune(r)
	}
	if inSingle || inDouble {
		return nil, errors.New("unterminated quote in launch arguments")
	}
	if hasToken {
		if cur.Len() > 256 {
			return nil, errors.New("launch argument token too long")
		}
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}

// ValidateSafeCodexConfigOverride rejects dangerous Codex -c/--config keys such as sandbox_mode, auth, or base_url.
func ValidateSafeCodexConfigOverride(assignment string) error {
	key, val, ok := strings.Cut(assignment, "=")
	key = strings.ToLower(strings.TrimSpace(key))
	if !ok || key == "" || strings.TrimSpace(val) == "" {
		return errors.New("config override requires key=value")
	}
	for _, blocked := range disallowedCodexConfigKeys {
		if key == blocked || strings.HasPrefix(key, blocked+".") {
			return fmt.Errorf("disallowed config override key: %s", key)
		}
	}
	if key == "features" {
		return errors.New("disallowed config override key: features")
	}
	return nil
}

// PrepareCodexShadowHome initializes an account-specific Codex home directory
// with strict 0700 permissions and copies only config.toml (0600), never auth.json.
func PrepareCodexShadowHome(configDir, shadowHome string) (string, error) {
	shadowHome = strings.TrimSpace(shadowHome)
	if shadowHome == "" {
		return "", nil
	}
	if !ValidCLIConfigDir(shadowHome) {
		return "", errors.New("invalid CLI shadow home path")
	}
	shadowDir := ExpandCLIUserPath(shadowHome)
	if shadowDir == "" {
		return "", errors.New("invalid CLI shadow home path")
	}
	cleanShadow := filepath.Clean(shadowDir)
	allowedRoot := false
	if home, hErr := os.UserHomeDir(); hErr == nil && home != "" {
		cleanHome := filepath.Clean(home)
		if rel, rErr := filepath.Rel(cleanHome, cleanShadow); rErr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			allowedRoot = true
		}
	}
	if tmpDir := filepath.Clean(os.TempDir()); tmpDir != "" {
		if rel, rErr := filepath.Rel(tmpDir, cleanShadow); rErr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			allowedRoot = true
		}
	}
	if !allowedRoot {
		return "", errors.New("CLI shadow home must reside inside user home directory")
	}
	if info, err := os.Lstat(shadowDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", errors.New("refusing symlink or non-directory shadow home")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	} else {
		if mkErr := os.MkdirAll(shadowDir, 0o700); mkErr != nil {
			return "", mkErr
		}
		if info, statErr := os.Lstat(shadowDir); statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", errors.New("invalid shadow home directory")
		}
	}
	if err := os.Chmod(shadowDir, 0o700); err != nil {
		return "", err
	}

	var baseDir string
	configDir = strings.TrimSpace(configDir)
	if configDir != "" {
		if !ValidCLIConfigDir(configDir) {
			return "", errors.New("invalid CLI config directory")
		}
		baseDir = ExpandCLIUserPath(configDir)
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		baseDir = filepath.Join(home, ".codex")
	}
	if baseDir == "" || filepath.Clean(baseDir) == filepath.Clean(shadowDir) {
		return shadowDir, nil
	}

	srcCfg := filepath.Join(baseDir, "config.toml")
	srcInfo, err := os.Lstat(srcCfg)
	if errors.Is(err, os.ErrNotExist) {
		return shadowDir, nil
	}
	if err != nil {
		return "", err
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 || !srcInfo.Mode().IsRegular() {
		return "", errors.New("refusing symlink or non-regular config.toml")
	}
	if srcInfo.Size() > CodexShadowConfigMaxBytes {
		return "", errors.New("config.toml exceeds 64 KiB limit")
	}

	dstCfg := filepath.Join(shadowDir, "config.toml")
	if dstInfo, dstErr := os.Lstat(dstCfg); dstErr == nil {
		if dstInfo.Mode()&os.ModeSymlink != 0 || !dstInfo.Mode().IsRegular() {
			return "", errors.New("refusing symlink or non-regular shadow config.toml")
		}
		return shadowDir, nil
	} else if !errors.Is(dstErr, os.ErrNotExist) {
		return "", dstErr
	}

	data, err := os.ReadFile(srcCfg)
	if err != nil {
		return "", err
	}
	if int64(len(data)) > CodexShadowConfigMaxBytes {
		return "", errors.New("config.toml exceeds 64 KiB limit")
	}
	if err := os.WriteFile(dstCfg, data, 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(dstCfg, 0o600); err != nil {
		return "", err
	}
	return shadowDir, nil
}
