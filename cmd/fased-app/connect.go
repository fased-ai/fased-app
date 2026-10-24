package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/connection"
	"io"
	"time"
)

func connectCommand(args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("connect", flag.ContinueOnError)
	f.SetOutput(errOut)
	address := f.String("url", "", "existing Fased HTTPS origin or loopback SSH tunnel")
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
