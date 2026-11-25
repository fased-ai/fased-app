package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/providersecrets"
)

const serverLaunchdLabel = "org.fased.server"

var (
	serverLaunchctl = runLaunchctl
	serverSchtasks  = runSchtasks
)

func serverServiceManagerForOS(goos string) string {
	switch goos {
	case "darwin":
		return "launchd"
	case "windows":
		return "windows"
	default:
		return "systemd"
	}
}

func defaultServerServiceManager() string {
	return serverServiceManagerForOS(runtime.GOOS)
}

func normalizeServerServiceManager(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "", "auto":
		return defaultServerServiceManager(), nil
	case "systemd", "launchd", "windows":
		return v, nil
	default:
		return "", errors.New("service manager must be auto, systemd, launchd, or windows")
	}
}

func serverServiceFileName(manager string) string {
	switch manager {
	case "launchd":
		return "org.fased.server.plist"
	case "windows":
		return "fased-server.task.xml"
	default:
		return "fased-server.service"
	}
}

func serverBinaryName(goos string) string {
	if goos == "windows" {
		return "fased-app.exe"
	}
	return "fased-app"
}

func defaultServerPrefixForOS(goos string) (string, error) {
	switch goos {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("cannot determine server installation prefix")
		}
		return filepath.Join(home, "Library", "Application Support", "Fased App", "server"), nil
	case "windows":
		base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil || home == "" {
				return "", errors.New("cannot determine server installation prefix")
			}
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, "Fased App", "server"), nil
	default:
		if base := os.Getenv("XDG_DATA_HOME"); base != "" {
			return filepath.Join(base, "fased-app"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("cannot determine server installation prefix")
		}
		return filepath.Join(home, ".local", "share", "fased-app"), nil
	}
}

func defaultServerServiceDirForOS(goos string) (string, error) {
	switch goos {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("cannot determine user LaunchAgents directory")
		}
		return filepath.Join(home, "Library", "LaunchAgents"), nil
	case "windows":
		return defaultServerPrefixForOS("windows")
	default:
		if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
			return filepath.Join(base, "systemd", "user"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("cannot determine user systemd directory")
		}
		return filepath.Join(home, ".config", "systemd", "user"), nil
	}
}

func defaultServerServiceDirForManager(manager string) (string, error) {
	switch manager {
	case "launchd":
		return defaultServerServiceDirForOS("darwin")
	case "windows":
		return defaultServerServiceDirForOS("windows")
	case "systemd":
		return defaultServerServiceDirForOS("linux")
	default:
		return "", errors.New("unsupported service manager")
	}
}

func serverWindowsTaskUserID() string {
	if u, err := user.Current(); err == nil && strings.TrimSpace(u.Uid) != "" {
		return u.Uid
	}
	return "S-1-5-11"
}

func serverWindowsTaskName() string {
	if u, err := user.Current(); err == nil && strings.TrimSpace(u.Uid) != "" {
		return "FasedServer-" + u.Uid
	}
	return "FasedServer-user"
}

func escapeXMLText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func launchdServerLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "/dev/null"
	}
	return filepath.Join(home, "Library", "Logs", "Fased", "server.log")
}

func rotateLogIfOversized(path string, maxBytes int64) {
	if path == "" || path == "/dev/null" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < maxBytes {
		return
	}
	_ = os.Rename(path, path+".1")
}

func formatLaunchdServerPlist(execArgs []string) string {
	logPath := launchdServerLogPath()
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("  <key>Label</key>\n  <string>" + serverLaunchdLabel + "</string>\n")
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, arg := range execArgs {
		b.WriteString("    <string>" + escapeXMLText(arg) + "</string>\n")
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")
	b.WriteString("  <key>KeepAlive</key>\n  <dict>\n    <key>SuccessfulExit</key>\n    <false/>\n  </dict>\n")
	b.WriteString("  <key>ThrottleInterval</key>\n  <integer>3</integer>\n")
	b.WriteString("  <key>Umask</key>\n  <integer>63</integer>\n")
	b.WriteString("  <key>ProcessType</key>\n  <string>Background</string>\n")
	b.WriteString("  <key>StandardOutPath</key>\n  <string>" + escapeXMLText(logPath) + "</string>\n")
	b.WriteString("  <key>StandardErrorPath</key>\n  <string>" + escapeXMLText(logPath) + "</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// escapeWindowsCommandArg formats an argument using Windows CommandLineToArgvW rules.
func escapeWindowsCommandArg(s string) string {
	if len(s) == 0 {
		return `""`
	}
	needsQuote := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\v', '"':
			needsQuote = true
		}
	}
	if !needsQuote {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			slashes++
		case '"':
			for ; slashes > 0; slashes-- {
				b.WriteString(`\\`)
			}
			b.WriteString(`\"`)
		default:
			for ; slashes > 0; slashes-- {
				b.WriteByte('\\')
			}
			b.WriteByte(s[i])
		}
	}
	for ; slashes > 0; slashes-- {
		b.WriteString(`\\`)
	}
	b.WriteByte('"')
	return b.String()
}

func formatWindowsServerTaskXML(userID, binPath string, serveArgs []string) string {
	escapedArgs := make([]string, len(serveArgs))
	for i, arg := range serveArgs {
		escapedArgs[i] = escapeWindowsCommandArg(arg)
	}
	argsStr := strings.Join(escapedArgs, " ")
	uidXML := escapeXMLText(userID)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%s</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Owner">
      <UserId>%s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Owner">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, uidXML, uidXML, escapeXMLText(binPath), escapeXMLText(argsStr))
}

func encodeWindowsTaskXMLUTF16(xmlStr string) []byte {
	xmlStr = strings.Replace(xmlStr, `encoding="UTF-8"`, `encoding="UTF-16"`, 1)
	words := utf16.Encode([]rune(xmlStr))
	out := make([]byte, 2+len(words)*2)
	out[0] = 0xFF
	out[1] = 0xFE
	for i, w := range words {
		binary.LittleEndian.PutUint16(out[2+i*2:], w)
	}
	return out
}

func runLaunchctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "/bin/launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("launchctl %s: %s: %w", strings.Join(args, " "), msg, err)
		}
		return fmt.Errorf("launchctl %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func runSchtasks(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "schtasks.exe", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("schtasks %s: %s: %w", strings.Join(args, " "), msg, err)
		}
		return fmt.Errorf("schtasks %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func launchdDomain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

func provisionServerUserCredential(ctx context.Context, absData string) (string, error) {
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		return "", errors.New("systemd user credentials require a non-root Linux host")
	}
	credDir := filepath.Join(absData, "server")
	if err := core.EnsurePrivateDirectory(credDir); err != nil {
		return "", err
	}
	credPath := filepath.Join(credDir, providersecrets.CredentialName+".cred")
	if info, err := os.Lstat(credPath); err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Mode().Perm()&0077 == 0 {
		return credPath, nil
	}
	bin, err := exec.LookPath("systemd-creds")
	if err != nil {
		return "", err
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return "", err
	}
	defer clear(key)
	tmp, err := os.CreateTemp(credDir, ".cred-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	_ = os.Chmod(tmpPath, 0600)
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()
	cmd := exec.CommandContext(ctx, bin, "encrypt", "--user", "--name="+providersecrets.CredentialName, "-", tmpPath)
	cmd.Stdin = bytes.NewReader(key)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("systemd-creds encrypt failed: %s: %w", strings.TrimSpace(string(out)), runErr)
	}
	if err = os.Chmod(tmpPath, 0600); err != nil {
		return "", err
	}
	if err = os.Rename(tmpPath, credPath); err != nil {
		return "", err
	}
	tmpPath = ""
	return credPath, nil
}

func enableAndStartServerService(ctx context.Context, manager, unitPath string) error {
	switch manager {
	case "systemd":
		if err := serverSystemctl(ctx, "daemon-reload"); err != nil {
			return err
		}
		if err := serverSystemctl(ctx, "enable", "fased-server.service"); err != nil {
			return err
		}
		if curUser, uErr := user.Current(); uErr == nil && curUser.Username != "" {
			if loginctl, lErr := exec.LookPath("loginctl"); lErr == nil {
				_ = exec.CommandContext(ctx, loginctl, "enable-linger", curUser.Username).Run()
			}
		}
		return serverSystemctl(ctx, "restart", "fased-server.service")
	case "launchd":
		rotateLogIfOversized(launchdServerLogPath(), 5*1024*1024)
		dom := launchdDomain()
		_ = serverLaunchctl(ctx, "bootout", dom+"/"+serverLaunchdLabel)
		return serverLaunchctl(ctx, "bootstrap", dom, unitPath)
	case "windows":
		task := serverWindowsTaskName()
		_ = serverSchtasks(ctx, "/End", "/TN", task)
		if err := serverSchtasks(ctx, "/Create", "/TN", task, "/XML", unitPath, "/F"); err != nil {
			return err
		}
		return serverSchtasks(ctx, "/Run", "/TN", task)
	default:
		return errors.New("unsupported service manager")
	}
}

func stopAndDisableServerService(ctx context.Context, manager string) error {
	switch manager {
	case "systemd":
		_ = serverSystemctl(ctx, "disable", "--now", "fased-server.service")
		_ = serverSystemctl(ctx, "daemon-reload")
		return nil
	case "launchd":
		_ = serverLaunchctl(ctx, "bootout", launchdDomain()+"/"+serverLaunchdLabel)
		return nil
	case "windows":
		task := serverWindowsTaskName()
		_ = serverSchtasks(ctx, "/End", "/TN", task)
		_ = serverSchtasks(ctx, "/Delete", "/TN", task, "/F")
		return nil
	default:
		return errors.New("unsupported service manager")
	}
}
