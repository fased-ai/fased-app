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

// ValidCustomCLIBinaryPathForVendor enforces ValidCustomCLIBinaryPath and requires that the
// executable basename (both literal and after symlink resolution if the file exists) matches
// the expected vendor CLI binary name (blocking arbitrary host executable invocation).
func ValidCustomCLIBinaryPathForVendor(p, expectedBinary string) bool {
	if !ValidCustomCLIBinaryPath(p) {
		return false
	}
	want := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(expectedBinary), ".exe"))
	if want == "" {
		return false
	}
	matchesBase := func(candidate string) bool {
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(candidate), ".exe"))
		if base == want {
			return true
		}
		if want == "antigravity" && (base == "agy" || base == "antigravity-acp") {
			return true
		}
		return false
	}
	expanded := ExpandCLIUserPath(p)
	if !matchesBase(expanded) {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(expanded); err == nil && resolved != "" {
		if !matchesBase(resolved) {
			return false
		}
		// Reject executables residing in world-writable directories (e.g. /tmp).
		if dirInfo, dErr := os.Stat(filepath.Dir(resolved)); dErr == nil && dirInfo.Mode().Perm()&0o002 != 0 {
			return false
		}
		if fileInfo, fErr := os.Stat(resolved); fErr == nil && fileInfo.Mode().Perm()&0o002 != 0 {
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
