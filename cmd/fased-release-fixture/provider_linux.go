//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Existing maintainer-only binary; never packaged or exposed in the wizard.
// The supplied executable is the unmodified, independently verified package.
func installedProvider(args []string, out io.Writer) error {
	if os.Getuid() != 1500 || os.Geteuid() != 1500 || os.Getenv("FASED_DISPOSABLE_VM") != "1" {
		return errors.New("dedicated disposable provider owner required")
	}
	dmi, err := os.ReadFile("/sys/class/dmi/id/product_name")
	if err != nil || !strings.Contains(string(dmi), "Standard PC") {
		return errors.New("disposable QEMU guest required")
	}
	if len(args) == 1 && args[0] == "--inspect" {
		v, e := installedProviderSandbox()
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	}
	paths, err := localinstall.DefaultPaths()
	if err != nil {
		return err
	}
	options, err := providerOptions(args)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(filepath.Join(paths.Root, "install.json")); !os.IsNotExist(err) {
		return errors.New("fresh installer-owned provider profile required; preserve existing installation")
	}
	m, err := localinstall.Install(context.Background(), paths, options)
	if err != nil {
		return err
	}
	if !m.ProviderFixture {
		return errors.New("installer did not retain provider mode")
	}
	return json.NewEncoder(out).Encode(m)
}
func providerOptions(args []string) (localinstall.Options, error) {
	var o localinstall.Options
	f := flag.NewFlagSet("installed-provider", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	disposable := f.Bool("disposable-fixture", false, "")
	f.StringVar(&o.Engine, "engine", "", "")
	f.StringVar(&o.EngineSHA256, "engine-sha256", "", "")
	if f.Parse(args) != nil || f.NArg() != 0 || !*disposable || !filepath.IsAbs(o.Engine) {
		return o, errors.New("exact disposable package identity required")
	}
	for _, v := range []string{o.EngineSHA256} {
		b, e := hex.DecodeString(v)
		if e != nil || len(b) != 32 || hex.EncodeToString(b) != v {
			return o, errors.New("canonical package digest required")
		}
	}
	o.Listen = "127.0.0.1:17485"
	o.Start = true
	o.Autostart = true
	o.ProviderFixture = true
	return o, nil
}

// Observe the actual installed core's namespace, not the helper's namespace.
func installedProviderSandbox() (map[string]any, error) {
	paths, e := localinstall.DefaultPaths()
	if e != nil {
		return nil, e
	}
	m, e := localinstall.Load(paths)
	if e != nil || !m.ProviderFixture {
		return nil, errors.New("installed provider manifest required")
	}
	status, e := localinstall.Status(context.Background(), paths)
	if e != nil || !status.Active {
		return nil, errors.New("owned installed service must be active")
	}
	group, e := exec.Command("/usr/bin/systemctl", "--user", "show", "fased-app.service", "--property=ControlGroup", "--value").Output()
	if e != nil || strings.TrimSpace(string(group)) == "" {
		return nil, errors.New("owned service cgroup required")
	}
	entries, e := os.ReadDir("/proc")
	if e != nil {
		return nil, e
	}
	var observed map[string]any
	for _, entry := range entries {
		if _, e := strconv.Atoi(entry.Name()); e != nil {
			continue
		}
		base := filepath.Join("/proc", entry.Name())
		image, e := os.Readlink(filepath.Join(base, "exe"))
		if e != nil || image != filepath.Join(paths.Root, "fased-app") {
			continue
		}
		cgroup, e := os.ReadFile(filepath.Join(base, "cgroup"))
		if e != nil || !strings.Contains(string(cgroup), ":"+strings.TrimSpace(string(group))+"\n") {
			continue
		}
		status, e := os.ReadFile(filepath.Join(base, "status"))
		if e != nil {
			continue
		}
		mounts, e := os.ReadFile(filepath.Join(base, "mountinfo"))
		if e != nil {
			continue
		}
		if !providerNamespace(string(status), string(mounts), paths.Root, paths.Data) {
			continue
		}
		if observed != nil {
			return nil, errors.New("ambiguous installed service process")
		}
		observed = map[string]any{"sandboxed": true, "pid": entry.Name(), "engineSha256": m.Files["fased-app"]}
	}
	if observed == nil {
		return nil, errors.New("actual installed provider sandbox not observed")
	}
	image, e := os.ReadFile(filepath.Join(paths.Root, "fased-app"))
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256(image)
	if hex.EncodeToString(sum[:]) != m.Files["fased-app"] {
		return nil, errors.New("installed engine changed")
	}
	return observed, nil
}
func providerNamespace(status, mounts, root, data string) bool {
	uid := strconv.Itoa(os.Getuid())
	owned, restricted := false, false
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 5 && fields[0] == "Uid:" {
			owned = true
			for _, v := range fields[1:] {
				owned = owned && v == uid
			}
		}
		if len(fields) == 2 && fields[0] == "NoNewPrivs:" && fields[1] == "1" {
			restricted = true
		}
	}
	readonly := func(path string) bool {
		best, ro := -1, false
		for _, line := range strings.Split(mounts, "\n") {
			f := strings.Fields(line)
			if len(f) < 6 {
				continue
			}
			mount := f[4]
			if mount != "/" && path != mount && !strings.HasPrefix(path, mount+"/") {
				continue
			}
			if len(mount) > best {
				best = len(mount)
				ro = strings.Contains(","+f[5]+",", ",ro,")
			}
		}
		return best >= 0 && ro
	}
	return owned && restricted && readonly(root) && !readonly(data)
}
