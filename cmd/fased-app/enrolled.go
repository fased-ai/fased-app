package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
)

func enrolledURL(m custodyinstall.Manifest) string {
	if m.PublicOrigin != "" {
		return m.PublicOrigin
	}
	return "http://" + m.Listen
}

// A pending/foreign fence is an error, never permission to launch the old core.
func enrolledTarget(data string) (custodyinstall.Manifest, bool, error) {
	if runtime.GOOS != "linux" {
		return custodyinstall.Manifest{}, false, nil
	}
	m, err := enrollment.EnrolledTarget(data, os.Getuid())
	if errors.Is(err, os.ErrNotExist) {
		return m, false, nil
	}
	return m, err == nil, err
}

type enrolledOps struct {
	commit  func(custodyinstall.Manifest) (custodyinstall.EnrollmentCommit, error)
	runtime func(custodyinstall.Manifest) error
	control func(context.Context, custodyinstall.Manifest, string, string) error
	probe   func(context.Context, string, string) (connection.Result, error)
	helper  func(context.Context, custodyinstall.Manifest, bool, io.Writer) error
	remove  func(context.Context, custodyinstall.Manifest) error
	open    func(string, string) error
}

func enrolledSystem(s *custodyinstall.System) enrolledOps {
	return enrolledOps{
		commit: custodyinstall.ReadEnrollmentCommit, runtime: custodyinstall.Runtime,
		control: func(ctx context.Context, m custodyinstall.Manifest, role, action string) error {
			return custodyinstall.Control(ctx, m, role, action, s)
		},
		probe: connection.Probe,
		helper: func(ctx context.Context, m custodyinstall.Manifest, recover bool, out io.Writer) error {
			return s.AppHelper(ctx, m, recover, false, out)
		},
		remove: func(ctx context.Context, m custodyinstall.Manifest) error { return custodyinstall.Remove(ctx, m, s) },
		open:   func(url, id string) error { return connection.OpenBrowser(url, id) },
	}
}

// All ordinary entry points share this adapter. It uses the existing managed
// services; the source's archived data is never opened or restarted.
func enrolledLifecycle(ctx context.Context, m custodyinstall.Manifest, action string, prepare, printOnly bool, out io.Writer, o enrolledOps) error {
	switch action {
	case "status", "start", "stop", "open", "access", "claim-code", "recover", "remove":
	default:
		return errors.New("unsupported enrolled installation action")
	}
	c, err := o.commit(m)
	if err != nil {
		return err
	}
	if !m.Enrollment || m.Owner != os.Getuid() || c.InstanceID == "" {
		return errors.New("verified owner enrollment required")
	}
	if action == "stop" || action == "remove" || action == "claim-code" || action == "recover" {
		if !prepare {
			return errors.New("protected installation management requires --prepare-host")
		}
		if action == "remove" {
			if err = o.remove(ctx, m); err != nil {
				return err
			}
			fmt.Fprintln(out, "Services removed. Original instance, signer keys and recovery data were preserved:", m.Root)
			return nil
		}
		if action == "claim-code" || action == "recover" {
			return o.helper(ctx, m, action == "recover", out)
		}
		for _, role := range []string{"app", "signer"} {
			if err = o.control(ctx, m, role, "stop"); err != nil {
				return err
			}
		}
		return nil
	}
	active := o.runtime(m) == nil
	if !active && (action == "start" || action == "open") {
		if !prepare {
			return errors.New("start the protected instance with --prepare-host")
		}
		for _, role := range []string{"signer", "app"} {
			if err = o.control(ctx, m, role, "start"); err != nil {
				return err
			}
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		until := time.NewTimer(15 * time.Second)
		defer until.Stop()
		for !active {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-until.C:
				return errors.New("protected instance is not ready; inspect service status; original data remains preserved")
			case <-ticker.C:
				active = o.runtime(m) == nil
			}
		}
	}
	url := enrolledURL(m)
	if active {
		probeURL := "http://" + m.Listen
		result, e := o.probe(ctx, probeURL, c.InstanceID)
		if e != nil {
			return e
		}
		if result.InstanceID != c.InstanceID || result.Location != m.Location || result.URL != probeURL {
			return errors.New("protected instance identity/profile changed; connection refused")
		}
	}
	if action == "status" {
		state := "unavailable"
		if active {
			state = "active"
		} else {
			url = ""
		}
		return json.NewEncoder(out).Encode(struct {
			Installed          bool   `json:"installed"`
			Active             bool   `json:"active"`
			State              string `json:"state"`
			InstanceID         string `json:"instanceId"`
			URL                string `json:"url,omitempty"`
			Location           string `json:"location"`
			WalletSigner       bool   `json:"walletSigner"`
			FinancialExecution bool   `json:"financialExecution"`
		}{true, active, state, c.InstanceID, url, m.Location, false, false})
	}
	if !active {
		return errors.New("protected instance is not running; use service start --prepare-host")
	}
	if action == "open" && !printOnly {
		if err = o.open(url, c.InstanceID); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, url)
	return nil
}
