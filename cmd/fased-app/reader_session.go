package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"io"
	"os"
	"strconv"
	"time"
)

func readerSessionClient(args []string, out io.Writer) error {
	if len(args) == 4 && args[0] == "--custody-owner" && args[2] == "--binding" {
		owner, err := strconv.Atoi(args[1])
		if err != nil || owner <= 0 {
			return errors.New("exact Local custody owner required")
		}
		image, err := os.Executable()
		if err != nil {
			return err
		}
		m, instance, err := custodyinstall.NativeUpdateSessionIdentity(owner, image, args[3])
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		client, err := core.NewClient(ctx, m.Root+"/app-state")
		if err != nil {
			return err
		}
		defer client.Close()
		defer func() { client.Token, client.CSRF = "", "" }()
		if err = prepareCustodyReaderSession(ctx, client, m, instance); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, "Ready")
		return err
	}
	return errors.New("legacy app-only Remote source session helper has been retired")
}

// The exact AppUID helper is the only process that reads/writes this app-private
// CLI session. Existing immutable update consumers can use it without receiving
// an OS-owner/browser credential or changing owner authentication.
func prepareLocalReaderSession(ctx context.Context, c *core.Client, m custodyinstall.Manifest, instance string) error {
	if instance == "" || c.Endpoint.InstanceID != instance || m.Location != "local" || !m.NativeOwnerAccess || m.Owner <= 0 {
		return errors.New("exact Local custody instance required")
	}
	var h core.Health
	if err := c.Do(ctx, "GET", "/api/v1/health", nil, &h); err != nil {
		return err
	}
	if h.InstanceID != instance || h.Connection == nil || h.Connection.Location != "local" || h.Connection.OwnerUID != m.Owner {
		return errors.New("Local custody owner changed")
	}
	var state struct {
		Claimed bool `json:"claimed"`
	}
	if err := c.Do(ctx, "GET", "/api/v1/auth/state", nil, &state); err != nil {
		return err
	}
	if !state.Claimed {
		return errors.New("Local custody must already be claimed")
	}
	dir := m.Root + "/app-state"
	if err := c.LoadSession(ctx, dir); err == nil {
		return nil
	} else if !errors.Is(err, core.ErrUnauthorized) {
		return err
	}
	if err := c.NativeLocalOwner(ctx); err != nil {
		return err
	}
	return c.SaveSession(dir)
}

// The root-owned bound helper has already proved its exact custody AppUID,
// image, generation and enrollment. The session remains app-private so the
// immutable predecessor can consume it through its unchanged update commands.
func prepareCustodyReaderSession(ctx context.Context, c *core.Client, m custodyinstall.Manifest, instance string) error {
	if m.Location == "local" {
		return prepareLocalReaderSession(ctx, c, m, instance)
	}
	if m.Location != "remote" || m.NativeOwnerAccess || m.Owner <= 0 || instance == "" || c.Endpoint.InstanceID != instance {
		return errors.New("exact Remote custody reader required")
	}
	var h core.Health
	if err := c.Do(ctx, "GET", "/api/v1/health", nil, &h); err != nil {
		return err
	}
	if h.InstanceID != instance || h.Connection == nil || h.Connection.Location != "remote" || h.Connection.OwnerKind != "custody" || h.Connection.OwnerUID != m.Owner {
		return errors.New("Remote custody reader owner changed")
	}
	return loadRemoteAppUpdateSession(ctx, c, m.Root+"/app-state", m.Owner, m.App.UID)
}
