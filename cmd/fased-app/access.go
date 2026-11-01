package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func checkLinger(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return errors.New("always-on VPS requires Linux")
	}
	b, e := exec.CommandContext(ctx, "loginctl", "show-user", strconv.Itoa(os.Getuid()), "--property=Linger", "--value").Output()
	if e != nil || strings.TrimSpace(string(b)) != "yes" {
		return errors.New("VPS background execution is not ready: ask the host administrator to enable linger for this OS account with loginctl enable-linger; Fased does not change login, SSH or firewall policy")
	}
	return nil
}
func accessCommand(args []string, out, errOut io.Writer) error {
	if len(args) < 1 {
		return errors.New("usage: fased-app access plan|enable|remove [options]")
	}
	p, e := localinstall.DefaultPaths()
	if e != nil {
		return e
	}
	f := flag.NewFlagSet("access", flag.ContinueOnError)
	f.SetOutput(errOut)
	mode := f.String("mode", "ssh", "local, ssh or tailscale")
	listen := f.String("listen", "127.0.0.1:7331", "fixed loopback listener")
	root := f.String("prefix", p.Root, "installation directory")
	data := f.String("data-dir", p.Data, "instance state")
	if e = f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	manager := privateaccess.System()
	if args[0] == "remove" {
		return manager.Remove(ctx, *root)
	}
	if args[0] == "enable" {
		m, e := localinstall.Load(localinstall.Paths{Root: *root, Data: *data})
		if e != nil {
			return e
		}
		*listen = m.Listen
		*mode = "tailscale"
		plan, e := manager.Plan(ctx, *mode, *listen)
		if e != nil {
			return e
		}
		if m.PublicOrigin != plan.Origin {
			return errors.New("install with --access tailscale first; the core must explicitly authorize this private HTTPS origin")
		}
		c, e := core.NewClient(ctx, *data)
		if e != nil {
			return e
		}
		id := c.Endpoint.InstanceID
		c.Close()
		if e = manager.Enable(ctx, m.Root, plan, id); e != nil {
			return e
		}
		setupURL, e := localOwnerSetupLink(ctx, *data, plan.Origin)
		if e != nil {
			return e
		}
		fmt.Fprintln(errOut, setupURL)
		fmt.Fprintln(out, plan.Origin)
		return nil
	}
	if args[0] != "plan" {
		return errors.New("access action must be plan, enable or remove")
	}
	plan, e := manager.Plan(ctx, *mode, *listen)
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(plan)
}
