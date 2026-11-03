package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"path/filepath"
	"strings"
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
		if !strings.HasPrefix(result.URL, "https://") || !strings.HasSuffix(result.URL, ".ts.net") {
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
