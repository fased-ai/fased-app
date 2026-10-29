//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func TestEnrolledEntryPointsUseOneInstanceAndPreserveProfile(t *testing.T) {
	for _, profile := range []string{"local", "remote"} {
		t.Run(profile, func(t *testing.T) {
			m := custodyinstall.Manifest{Owner: os.Getuid(), Enrollment: true, Location: profile, Listen: "127.0.0.1:19090"}
			if profile == "remote" {
				m.PublicOrigin = "https://controlled.owner.ts.net"
			}
			active := false
			var calls []string
			id := strings.Repeat("a", 43)
			o := enrolledOps{
				commit: func(custodyinstall.Manifest) (custodyinstall.EnrollmentCommit, error) {
					return custodyinstall.EnrollmentCommit{InstanceID: id}, nil
				},
				runtime: func(custodyinstall.Manifest) error {
					if active {
						return nil
					}
					return errors.New("stopped")
				},
				control: func(_ context.Context, _ custodyinstall.Manifest, role, action string) error {
					calls = append(calls, role+":"+action)
					if role == "app" {
						active = action == "start"
					}
					return nil
				},
				probe: func(_ context.Context, url, expected string) (connection.Result, error) {
					if expected != id || url != "http://"+m.Listen {
						t.Fatal("wrong instance/transport")
					}
					return connection.Result{URL: url, InstanceID: id, Location: profile}, nil
				},
				open: func(url, expected string) error {
					if url != enrolledURL(m) || expected != id {
						t.Fatal("wrong browser target")
					}
					calls = append(calls, "browser")
					return nil
				},
				helper: func(_ context.Context, _ custodyinstall.Manifest, recovery bool, _ io.Writer) error {
					calls = append(calls, "helper")
					return nil
				},
				remove: func(context.Context, custodyinstall.Manifest) error {
					calls = append(calls, "remove-preserving-state")
					return nil
				},
			}
			var out bytes.Buffer
			if e := enrolledLifecycle(t.Context(), m, "start", false, false, &out, o); e == nil || len(calls) != 0 {
				t.Fatal("unconsented OS action")
			}
			if e := enrolledLifecycle(t.Context(), m, "start", true, false, &out, o); e != nil {
				t.Fatal(e)
			}
			if strings.Join(calls, ",") != "signer:start,app:start" {
				t.Fatal(calls)
			}
			if !strings.Contains(out.String(), enrolledURL(m)) {
				t.Fatal(out.String())
			}
			out.Reset()
			if e := enrolledLifecycle(t.Context(), m, "status", false, false, &out, o); e != nil || !strings.Contains(out.String(), `"instanceId":"`+id+`"`) {
				t.Fatal(out.String(), e)
			}
			if e := enrolledLifecycle(t.Context(), m, "open", false, false, io.Discard, o); e != nil {
				t.Fatal(e)
			}
			if e := enrolledLifecycle(t.Context(), m, "stop", true, false, io.Discard, o); e != nil {
				t.Fatal(e)
			}
			if strings.Join(calls, ",") != "signer:start,app:start,browser,app:stop,signer:stop" {
				t.Fatal(calls)
			}
			out.Reset()
			if e := enrolledLifecycle(t.Context(), m, "status", false, false, &out, o); e != nil || strings.Contains(out.String(), `"url"`) {
				t.Fatal("inactive target advertised a connection", out.String(), e)
			}
			for _, action := range []string{"claim-code", "recover", "remove"} {
				before := len(calls)
				if e := enrolledLifecycle(t.Context(), m, action, false, false, io.Discard, o); e == nil || len(calls) != before {
					t.Fatal("missing consent", action)
				}
				if e := enrolledLifecycle(t.Context(), m, action, true, false, io.Discard, o); e != nil {
					t.Fatal(e)
				}
			}
			// No source path, service registration or core constructor exists in this adapter.
			active = true
			o.probe = func(context.Context, string, string) (connection.Result, error) {
				return connection.Result{URL: "http://" + m.Listen, InstanceID: "foreign", Location: profile}, nil
			}
			if e := enrolledLifecycle(t.Context(), m, "open", true, false, io.Discard, o); e == nil {
				t.Fatal("foreign runtime opened")
			}
			o.commit = func(custodyinstall.Manifest) (custodyinstall.EnrollmentCommit, error) {
				return custodyinstall.EnrollmentCommit{}, errors.New("pending")
			}
			before := len(calls)
			if e := enrolledLifecycle(t.Context(), m, "start", true, false, io.Discard, o); e == nil || len(calls) != before {
				t.Fatal("pending enrollment started")
			}
		})
	}
}

func TestPendingRemoteFenceDoesNotBlockNetworkRecoveryDispatch(t *testing.T) {
	pending := errors.New("pending source fence")
	for _, action := range []string{"rollback", "network", "harden", "confirm", "status", "start", "stop", "access", "remove", "claim-code", "recover"} {
		called := false
		_, found, err := vpsEnrollmentTarget(action, "/original/app-state", func(string) (custodyinstall.Manifest, bool, error) {
			called = true
			return custodyinstall.Manifest{}, false, pending
		})
		recovery := action == "rollback" || action == "network" || action == "harden" || action == "confirm"
		if recovery {
			if called || err != nil || found {
				t.Fatal("network policy guard is unreachable", action, err)
			}
		} else if !called || !errors.Is(err, pending) || found {
			t.Fatal("pending lifecycle started source", action, err)
		}
	}
}
