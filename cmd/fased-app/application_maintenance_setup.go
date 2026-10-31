package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

func vpsUpdatesCommand(args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) != 1 || args[0] != "prepare" {
		return errors.New("usage: fased-app vps updates prepare")
	}
	m, e := vpsinstall.Load(os.Getuid())
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	system := &custodyinstall.System{In: in, Out: errOut}
	return prepareRemoteUpdateAuthority(ctx, m, system, out)
}
func prepareRemoteUpdateAuthority(ctx context.Context, m vpsinstall.Manifest, system *custodyinstall.System, out io.Writer) error {
	// The OS ceremony describes the durable grant explicitly; this is unrelated to
	// a provider, wallet or choosing Automatic in application preferences.
	return prepareRemoteUpdateAuthorityPlatform(ctx, m, system, out)
}
func remoteUpdateAuthorityResult(out io.Writer) error {
	return json.NewEncoder(out).Encode(map[string]any{"prepared": true, "automaticUpdates": false, "publicActivation": false})
}
