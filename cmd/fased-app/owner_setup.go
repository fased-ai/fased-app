package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"os"
	"strings"
)

// Runs only as the data-owning OS user (or the verified Remote UID helper).
// No browser endpoint issues a proof; opening a claimed workspace never resets it.
var ownerSetupClient = newApplicationUpdatesClient

func ownerSetupLink(ctx context.Context, dir, origin string, out io.Writer) error {
	client, enrolled, err := ownerSetupClient(ctx, dir)
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
	var health core.Health
	if err = client.Do(ctx, "GET", "/api/v1/health", nil, &health); err != nil {
		return err
	}
	if health.InstanceID != client.Endpoint.InstanceID {
		return errors.New("Local owner instance changed; reopen Fased")
	}
	if origin == client.Endpoint.URL && health.Connection != nil && health.Connection.Location == "local" && health.Connection.Transport == "loopback" && (health.Connection.OwnerUID == 0 && !enrolled || enrolled && os.Getuid() > 0 && health.Connection.OwnerUID == os.Getuid()) {
		var proof string
		var e error
		if enrolled {
			proof, e = client.NativeLocalLaunch(ctx)
		} else {
			proof, e = client.LocalLaunch(ctx)
		}
		if e != nil {
			return e
		}
		address, e := connection.OwnerSetupURL(origin, client.Endpoint.InstanceID, proof)
		if e != nil {
			return e
		}
		_, e = fmt.Fprintln(out, strings.Replace(address, "#setup=", "#local=", 1))
		return e
	}
	if enrolled {
		return errors.New("protected Local owner instance or browser origin differs")
	}
	setupLock, err := core.LockOwnerSetup(dir)
	if err != nil {
		return err
	}
	defer setupLock.Close()
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
