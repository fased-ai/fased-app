package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUniqueJSONAndPartialAIAnswer(t *testing.T) {
	t.Parallel()
	if !UniqueJSON([]byte(`{"ok":true,"n":[1,2]}`)) {
		t.Fatal("rejected valid JSON")
	}
	if UniqueJSON([]byte(`{"a":1,"a":2}`)) || UniqueJSON([]byte(`{"\u0061":1,"a":2}`)) {
		t.Fatal("admitted duplicate object key")
	}
	if UniqueJSON([]byte(strings.Repeat("[", 20) + strings.Repeat("]", 20))) {
		t.Fatal("admitted nesting > 16")
	}
	if got := PartialAIAnswer(`{"answer":"Hello wor`); got != "Hello wor" {
		t.Fatalf("unexpected partial answer: %q", got)
	}
	if got := PartialAIAnswer(`{"status":"pending"}`); got != "" {
		t.Fatalf("expected empty on non-answer key, got %q", got)
	}
}

func TestCLIPathsElectronArgsAndShadowHome(t *testing.T) {
	t.Parallel()
	if ValidCLIConfigDir("../bad") || ValidCustomCLIBinaryPath("a/../../b") {
		t.Fatal("admitted parent traversal path")
	}
	if !ValidCustomCLIBinaryPath("/usr/local/bin/codex") {
		t.Fatal("rejected safe binary path")
	}

	guiDir := t.TempDir()
	guiBin := filepath.Join(guiDir, "antigravity")
	if err := os.WriteFile(guiBin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(guiDir, "resources.pak"), []byte("pak"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsElectronGUIAntigravity(guiBin) {
		t.Fatal("failed to detect Electron bundle marker")
	}

	args, err := ParseSafeCLILaunchArgs(`--effort "high" -c model_reasoning_effort=high`)
	if err != nil || len(args) != 4 {
		t.Fatalf("expected safe args, got %v err=%v", args, err)
	}
	for _, bad := range []string{
		"--yolo",
		"-s workspace-write",
		"-c sandbox_mode=danger-full-access",
		"-c auth=something",
		"--profile safe-mode",
		"--profile $(id)",
		"positional",
	} {
		if _, err := ParseSafeCLILaunchArgs(bad); err == nil {
			t.Fatalf("admitted unsafe launch args %q", bad)
		}
	}

	baseDir := t.TempDir()
	shadowDir := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(filepath.Join(baseDir, "config.toml"), []byte("model = \"o3\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "auth.json"), []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	outDir, err := PrepareCodexShadowHome(baseDir, shadowDir)
	if err != nil || outDir != shadowDir {
		t.Fatalf("PrepareCodexShadowHome failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(shadowDir, "config.toml")); err != nil {
		t.Fatalf("expected config.toml copied to shadow home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(shadowDir, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("auth.json must never be copied into shadow home")
	}
	symBase := t.TempDir()
	_ = os.Symlink(filepath.Join(baseDir, "config.toml"), filepath.Join(symBase, "config.toml"))
	if _, err := PrepareCodexShadowHome(symBase, filepath.Join(t.TempDir(), "sym-shadow")); err == nil {
		t.Fatal("admitted symlink config.toml")
	}
}
