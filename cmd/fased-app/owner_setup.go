package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"io"
	"os"
)

// Runs only as the data-owning OS user (or the verified Remote UID helper).
// No browser endpoint issues a proof; opening a claimed workspace never resets it.
func ownerSetupLink(ctx context.Context, dir, origin string, out io.Writer) error {
	client, err := core.NewClient(ctx, dir)
	if err != nil {
		return err
	}
	defer client.Close()
	if origin == "" {
		origin = client.Endpoint.URL
	}
	if _, err = connection.OwnerSetupURL(origin, client.Endpoint.InstanceID, ""); err != nil {
		return err
	}
	store, err := core.OpenStore(dir)
	if err != nil {
		return err
	}
	defer store.Close()
	claimed, err := store.Claimed(ctx)
	if err != nil {
		return err
	}
	proof := ""
	if !claimed {
		code, e := store.IssueClaimCode(ctx)
		if e != nil {
			return e
		}
		proof = code.Code
	}
	address, err := connection.OwnerSetupURL(origin, client.Endpoint.InstanceID, proof)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, address)
	return err
}
func localOwnerSetupLink(ctx context.Context, dir, origin string) (string, error) {
	var out bytes.Buffer
	if err := ownerSetupLink(ctx, dir, origin, &out); err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(out.Bytes())), nil
}

// The fixed OS helper derives its origin from verified configuration, never
// caller-selected sudo arguments. Only the trusted terminal receives its proof.
func remoteOwnerSetupLink(args []string, out io.Writer) error {
	if len(args) != 2 || args[0] != "--config" {
		return errors.New("fixed Remote setup configuration required")
	}
	image, e := os.Executable()
	if e != nil {
		return e
	}
	m, e := vpsinstall.ValidateApplicationOwnerHelper(args[1], image, os.Getuid())
	if e != nil {
		return e
	}
	return ownerSetupLink(context.Background(), m.Data, m.PublicOrigin, out)
}
