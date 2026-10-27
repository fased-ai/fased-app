package localinstall

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const engineName = "fased-app"
const desktopName = "fased-desktop"
const definitionName = "org.fased.app.plist"
const label = "org.fased.app"

func defaultRoot(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Fased App", "app")
}
func supported() error {
	if os.Geteuid() == 0 {
		return errors.New("run Fased App as an ordinary OS user")
	}
	return nil
}
func escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func definition(p Paths, o Options) ([]byte, error) {
	if _, err := validateDefinition(p, o); err != nil {
		return nil, err
	}
	args := []string{filepath.Join(p.Root, engineName), "serve", "--data-dir", p.Data, "--listen", o.Listen}
	if o.Location != "" {
		args = append(args, "--instance-location", o.Location)
	}
	if o.PublicOrigin != "" {
		args = append(args, "--public-origin", o.PublicOrigin)
	}
	x := "<?xml version=\"1.0\" encoding=\"UTF-8\"?><!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\"><plist version=\"1.0\"><dict><key>Label</key><string>" + label + "</string><key>ProgramArguments</key><array>"
	for _, a := range args {
		x += "<string>" + escape(a) + "</string>"
	}
	x += "</array><key>RunAtLoad</key><true/><key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict><key>ThrottleInterval</key><integer>3</integer><key>Umask</key><integer>63</integer><key>ProcessType</key><string>Background</string><key>StandardOutPath</key><string>/dev/null</string><key>StandardErrorPath</key><string>/dev/null</string></dict></plist>"
	return []byte(x), nil
}
func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }
func launch(ctx context.Context, args ...string) (string, error) {
	b, e := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	if e != nil {
		return string(b), fmt.Errorf("user launch agent refused %s", args[0])
	}
	return string(b), nil
}
func loaded(ctx context.Context, m Manifest) (bool, error) {
	b, err := launch(ctx, "print", domain()+"/"+label)
	if err != nil {
		return false, nil
	}
	if !strings.Contains(b, "program = "+filepath.Join(m.Root, engineName)+"\n") || !strings.Contains(b, "path = "+filepath.Join(m.Root, definitionName)+"\n") {
		return false, errors.New("another launch agent uses this label; refusing modification")
	}
	return true, nil
}
func register(ctx context.Context, m Manifest) error { _, err := loaded(ctx, m); return err }
func agentLink() (string, error) {
	h, e := os.UserHomeDir()
	return filepath.Join(h, "Library", "LaunchAgents", definitionName), e
}
func checkLink(m Manifest) (bool, error) {
	p, e := agentLink()
	if e != nil {
		return false, e
	}
	s, e := os.Readlink(p)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil || s != filepath.Join(m.Root, definitionName) {
		return false, errors.New("another login agent occupies the Fased path")
	}
	return true, nil
}
func enable(ctx context.Context, m Manifest) error {
	exists, e := checkLink(m)
	if e != nil || exists {
		return e
	}
	p, e := agentLink()
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	return os.Symlink(filepath.Join(m.Root, definitionName), p)
}
func start(ctx context.Context, m Manifest) error {
	ok, e := loaded(ctx, m)
	if e != nil || ok {
		return e
	}
	_, e = launch(ctx, "bootstrap", domain(), filepath.Join(m.Root, definitionName))
	return e
}
func stop(ctx context.Context, m Manifest) error {
	ok, e := loaded(ctx, m)
	if e != nil || !ok {
		return e
	}
	_, e = launch(ctx, "bootout", domain()+"/"+label)
	return e
}
func status(ctx context.Context, m Manifest) (State, error) {
	ok, e := loaded(ctx, m)
	if e != nil {
		return State{}, e
	}
	on, e := checkLink(m)
	return State{Installed: true, Active: ok && coreAlive(ctx, m.Data), Autostart: on}, e
}
func unregister(ctx context.Context, m Manifest) error {
	on, e := checkLink(m)
	if e != nil {
		return e
	}
	if e = stop(ctx, m); e != nil {
		return e
	}
	if on {
		p, e := agentLink()
		if e != nil {
			return e
		}
		return os.Remove(p)
	}
	return nil
}
