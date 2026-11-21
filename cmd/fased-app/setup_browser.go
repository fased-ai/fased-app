package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func needsPrivateLaunchFile(target string) bool {
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	return strings.HasPrefix(u.Fragment, "local=") || strings.HasPrefix(u.Fragment, "setup=")
}

func writePrivateLaunchFile(target string) (string, error) {
	u, err := url.Parse(target)
	if err != nil || u.User != nil {
		return "", errors.New("invalid private browser launch target")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("unsafe browser launch target")
	}
	for i := 0; i < len(target); i++ {
		if target[i] < 32 || target[i] == 127 {
			return "", errors.New("invalid character in browser launch target")
		}
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		return "", errors.New("user cache directory unavailable for private launch")
	}
	dir := filepath.Join(cacheDir, "fased-launch")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("unsafe browser launch directory")
	}
	_ = os.Chmod(dir, 0700)
	if entries, err := os.ReadDir(dir); err == nil {
		cutoff := time.Now().Add(-5 * time.Minute)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "launch-") && strings.HasSuffix(entry.Name(), ".html") {
				if fi, e := entry.Info(); e == nil && fi.ModTime().Before(cutoff) {
					_ = os.Remove(filepath.Join(dir, entry.Name()))
				}
			}
		}
	}
	var raw [16]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "launch-"+hex.EncodeToString(raw[:])+".html")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	body := fmt.Sprintf("<!doctype html>\n<meta charset=\"utf-8\">\n<meta name=\"referrer\" content=\"no-referrer\">\n<meta http-equiv=\"refresh\" content=\"0;url=%s\">\n<title>Opening Fased</title>\n", html.EscapeString(target))
	_, writeErr := io.WriteString(f, body)
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return "", writeErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: absPath}).String(), nil
}

func openBrowserLaunch(target string) error {
	launch := target
	if needsPrivateLaunchFile(target) {
		var err error
		launch, err = writePrivateLaunchFile(target)
		if err != nil {
			return err
		}
	}
	var c *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		c = exec.Command("xdg-open", launch)
	case "darwin":
		c = exec.Command("open", launch)
	case "windows":
		c = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", launch)
	default:
		return errors.New("no browser opener on this platform")
	}
	c.Stdout, c.Stderr = io.Discard, io.Discard
	if err := c.Start(); err != nil {
		return errors.New("cannot open the browser; use open --print for the private URL")
	}
	_ = c.Process.Release()
	return nil
}

// Use the exact already-issued setup link. Reissuing a proof while opening a
// browser would invalidate the terminal fallback. Headless setup prints it only.
func openSetupBrowser(url string, _ io.Writer) {
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return
	}
	_ = openBrowserLaunch(url)
}
