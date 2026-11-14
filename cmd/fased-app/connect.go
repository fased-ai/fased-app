package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/tailnet"
	"io"
	"path/filepath"
	"time"
)

func connectCommand(args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("connect", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := core.DefaultDataDir()
	if e != nil {
		return e
	}
	dataDir := f.String("data-dir", base, "private client preferences")
	address := f.String("url", "", "existing Local or Tailscale HTTPS instance")
	expected := f.String("instance-id", "", "optional expected instance identity")
	printOnly := f.Bool("print", false, "verify and print without opening a browser")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || *address == "" {
		return errors.New("usage: fased-app connect --url https://your-instance [--instance-id ID] [--print]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, e := connection.Probe(ctx, *address, *expected)
	if e != nil {
		return e
	}
	if result.Location == "remote" {
		if _, _, err := tailnet.Origin(result.URL); err != nil {
			return errors.New("Remote requires its Tailscale HTTPS address")
		}
		dir := *dataDir
		if e = core.EnsurePrivateDirectory(dir); e != nil {
			return e
		}
		if e = core.WritePrivateJSON(filepath.Join(dir, "remote-connection.json"), result); e != nil {
			return e
		}
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		return err
	}
	if !*printOnly {
		if err := connection.OpenBrowser(result.URL, result.InstanceID); err != nil {
			return fmt.Errorf("instance verified; open %s in your browser", result.URL)
		}
	}
	return nil
}

// Reuse the same device-bound connection for Remote UI-equivalent CLI paths.
func connectedRemoteClient(ctx context.Context, dir string) (*core.Client, error) {
	data, err := core.ReadPrivateFile(filepath.Join(dir, "remote-connection.json"))
	if err != nil {
		return nil, errors.New("choose your Remote instance with fased connect --url https://your-instance.ts.net")
	}
	var profile connection.Result
	if json.Unmarshal(data, &profile) != nil || profile.Location != "remote" || profile.Transport != "private-https" {
		return nil, errors.New("invalid Remote profile; preserve it and reconnect")
	}
	return core.NewRemoteClient(ctx, profile.URL, profile.InstanceID)
}
