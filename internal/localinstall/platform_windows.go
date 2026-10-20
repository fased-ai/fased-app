package localinstall

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"
)

const engineName = "fased-app.exe"
const desktopName = "fased-desktop.exe"
const definitionName = "fased-app.task.xml"

func defaultRoot(home string) string {
	return filepath.Join(home, "AppData", "Local", "Fased App", "app")
}
func supported() error { return nil }
func taskName() string {
	u, e := user.Current()
	if e != nil {
		return "FasedApp-unknown"
	}
	return "FasedApp-" + u.Uid
}
func escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func taskArgs(p Paths, o Options) string {
	args := []string{"serve", "--data-dir", p.Data, "--listen", o.Listen}
	if o.PublicOrigin != "" {
		args = append(args, "--public-origin", o.PublicOrigin)
	}
	for i, a := range args {
		args[i] = syscall.EscapeArg(a)
	}
	return strings.Join(args, " ")
}
func definition(p Paths, o Options) ([]byte, error) {
	if _, e := validateDefinition(p, o); e != nil {
		return nil, e
	}
	u, e := user.Current()
	if e != nil {
		return nil, e
	}
	// InteractiveToken stores no OS password and never requests elevation. The
	// desktop engine ends at logout; always-on VPS is Linux, not this task.
	x := `<?xml version="1.0" encoding="UTF-8"?><Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers></Triggers><Principals><Principal id="Owner"><UserId>` + escape(u.Uid) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><Enabled>true</Enabled></Settings><Actions Context="Owner"><Exec><Command>` + escape(filepath.Join(p.Root, engineName)) + `</Command><Arguments>` + escape(taskArgs(p, o)) + `</Arguments></Exec></Actions></Task>`
	if o.Autostart {
		x = strings.Replace(x, "<Triggers></Triggers>", "<Triggers><LogonTrigger><Enabled>true</Enabled><UserId>"+escape(u.Uid)+"</UserId></LogonTrigger></Triggers>", 1)
	}
	return encodeTaskXML(x), nil
}

type taskXML struct {
	Principals struct {
		Principal struct {
			UserID    string `xml:"UserId"`
			LogonType string
			RunLevel  string
		}
	}
	Actions struct {
		Exec []struct{ Command, Arguments string }
	}
}

func scheduler(ctx context.Context, args ...string) ([]byte, error) {
	b, e := exec.CommandContext(ctx, "schtasks.exe", args...).CombinedOutput()
	if e != nil {
		return b, fmt.Errorf("user task scheduler refused %s: %.2048s", args[0], strings.TrimSpace(string(b)))
	}
	return b, nil
}
func taskExists(ctx context.Context, m Manifest) (bool, error) {
	b, e := scheduler(ctx, "/Query", "/TN", taskName(), "/XML")
	if e != nil {
		return false, nil
	}
	want, e := os.ReadFile(filepath.Join(m.Root, definitionName))
	if e != nil {
		return false, e
	}
	var a, w taskXML
	b = decodeTaskXML(b)
	want = decodeTaskXML(want)
	if xml.Unmarshal(b, &a) != nil || xml.Unmarshal(want, &w) != nil || !reflect.DeepEqual(a, w) {
		return false, errors.New("another scheduled task occupies this name or changed its identity")
	}
	return true, nil
}
func register(ctx context.Context, m Manifest) error {
	ok, e := taskExists(ctx, m)
	if e != nil || ok {
		return e
	}
	_, e = scheduler(ctx, "/Create", "/TN", taskName(), "/XML", filepath.Join(m.Root, definitionName))
	return e
}
func enable(ctx context.Context, m Manifest) error {
	// Login triggers require a different definition; never silently rewrite a
	// task which may belong to another installation. Use the explicit option at
	// initial install to generate the trigger as part of its verified identity.
	return nil
}
func start(ctx context.Context, m Manifest) error {
	ok, e := taskExists(ctx, m)
	if e != nil {
		return e
	}
	if !ok {
		return errors.New("owned background task is missing")
	}
	_, e = scheduler(ctx, "/Run", "/TN", taskName())
	return e
}
func stop(ctx context.Context, m Manifest) error {
	ok, e := taskExists(ctx, m)
	if e != nil || !ok {
		return e
	}
	if !coreAlive(ctx, m.Data) {
		return nil
	}
	_, e = scheduler(ctx, "/End", "/TN", taskName())
	if e != nil {
		return e
	}
	for i := 0; i < 50; i++ {
		if !coreAlive(ctx, m.Data) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("background engine has not stopped")
}
func status(ctx context.Context, m Manifest) (State, error) {
	ok, e := taskExists(ctx, m)
	if e != nil {
		return State{}, e
	}
	return State{Installed: true, Active: ok && coreAlive(ctx, m.Data), Autostart: m.Autostart}, nil
}
func unregister(ctx context.Context, m Manifest) error {
	ok, e := taskExists(ctx, m)
	if e != nil || !ok {
		return e
	}
	if coreAlive(ctx, m.Data) {
		if e = stop(ctx, m); e != nil {
			return e
		}
	}
	_, e = scheduler(ctx, "/Delete", "/TN", taskName(), "/F")
	return e
}
