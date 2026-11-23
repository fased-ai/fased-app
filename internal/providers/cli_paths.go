package providers

import (
	"os"
	"path/filepath"
	"strings"
)

// ValidCustomCLIBinaryPath checks length, NUL/newline safety, and parent traversal in user CLI binary paths.
func ValidCustomCLIBinaryPath(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || len(p) > 260 {
		return false
	}
	if strings.ContainsAny(p, "\x00\r\n") {
		return false
	}
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return false
		}
	}
	return true
}

// ValidCLIConfigDir validates a user-supplied CLI config or shadow home directory path.
func ValidCLIConfigDir(p string) bool {
	return ValidCustomCLIBinaryPath(p)
}

// ExpandCLIUserPath resolves leading "~/" or "~\" against os.UserHomeDir.
func ExpandCLIUserPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// IsElectronGUIAntigravity detects Electron desktop bundles that must not be launched headless as CLI binaries.
func IsElectronGUIAntigravity(binPath string) bool {
	if binPath == "" {
		return false
	}
	resolved, err := filepath.EvalSymlinks(binPath)
	if err != nil || resolved == "" {
		resolved = binPath
	}
	if strings.Contains(resolved, ".app/Contents/MacOS/") {
		return true
	}
	dir := filepath.Dir(resolved)
	for _, marker := range []string{"resources.pak", "LICENSE.electron.txt", "chrome_100_percent.pak"} {
		if info, err := os.Stat(filepath.Join(dir, marker)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}
