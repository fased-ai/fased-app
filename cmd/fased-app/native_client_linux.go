//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/desktop"
	"github.com/fased-ai/fased-app/internal/signer"
)

// Exact OS-owner sudo policy invokes this immutable image as AppUID. It is a
// transport for the existing core, not another engine or a signer capability.
func nativeAppClient(args []string, in io.Reader, out io.Writer) error {
	f := flag.NewFlagSet("native-client", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	data := f.String("data-dir", "", "protected app state")
	config := f.String("owner-config", "", "immutable identity")
	if f.Parse(args) != nil || f.NArg() != 0 {
		return errors.New("invalid protected native client")
	}
	cfg, err := signer.LoadConfig(*config)
	if err != nil {
		return err
	}
	root := filepath.Dir(*config)
	var m custodyinstall.Manifest
	if root == custodyinstall.Root(cfg.OwnerUID) {
		m, err = custodyinstall.Load(cfg.OwnerUID)
		if err == nil && (!m.NativeOwnerAccess || custodyinstall.Verify(m) != nil) {
			err = errors.New("protected native owner required")
		}
	} else {
		m, err = custodyinstall.LoadPackageInstance(cfg.OwnerUID, root)
		if err == nil {
			err = custodyinstall.VerifyPackageOwnerTarget(m)
		}
		if err == nil {
			err = packageRunningImageMatches(filepath.Join(m.Root, "fased-app"))
		}
	}
	if err != nil || m.Location != "local" || !m.Enrollment || m.App.UID != os.Getuid() || m.App.UID != os.Geteuid() || signer.CheckApplication(*config, m.Root, *data) != nil {
		return errors.New("protected Local owner helper required")
	}
	r, err := http.ReadRequest(bufio.NewReader(io.LimitReader(in, 64*1024)))
	if err != nil {
		return errors.New("invalid native request")
	}
	defer r.Body.Close()
	if r.ContentLength > 16*1024 || !nativeClientRequestAllowed(r) {
		return errors.New("native route unavailable")
	}
	commit, err := custodyinstall.ReadEnrollmentCommit(m)
	if err != nil {
		_, statErr := os.Lstat(filepath.Join(m.Root, "enrollment-ready.json"))
		if m.Layout != custodyinstall.PackageLayout || !errors.Is(statErr, os.ErrNotExist) || !packageOwnerBootstrapProbe(r) {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := core.NewClient(ctx, *data)
	if err != nil {
		return err
	}
	defer client.Close()
	if commit.InstanceID != "" && client.Endpoint.InstanceID != commit.InstanceID {
		return errors.New("native owner instance changed")
	}
	// Every request goes through the existing protected IPC, never a loopback
	// TCP destination which another local process could impersonate.
	body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024+1))
	if err != nil || len(body) > 16*1024 {
		return errors.New("native request too large")
	}
	defer clear(body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.TransferEncoding = nil
	endpoint, _ := url.Parse(client.Endpoint.URL)
	r.URL.Scheme, r.URL.Host = endpoint.Scheme, endpoint.Host
	r.Host = endpoint.Host
	r.RequestURI = ""
	r.Close = true
	if r.Method != "GET" {
		r.Header.Set("Origin", client.Endpoint.URL)
	}
	response, err := client.HTTP.Do(r.WithContext(ctx))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if commit.InstanceID == "" {
		if err = verifyPackageBootstrapHealth(response, client.Endpoint.InstanceID, m.Owner); err != nil {
			return err
		}
	}
	response.Close = true
	return response.Write(out)
}

// VerifyPackageOwnerTarget authenticates the installed image. Confirm the
// running process is that image too, even if a concurrent update replaced it.
func packageRunningImageMatches(installed string) error {
	running, err := os.Stat("/proc/self/exe")
	if err != nil {
		return err
	}
	current, err := os.Stat(installed)
	if err != nil || !running.Mode().IsRegular() || !current.Mode().IsRegular() || !os.SameFile(running, current) {
		return errors.New("running package client differs from the protected image")
	}
	return nil
}

// The first protected owner record must use the same IPC endpoint identity
// that NewClient just verified, not a different subsequent health response.
func verifyPackageBootstrapHealth(response *http.Response, instance string, owner int) error {
	if response == nil || response.Body == nil || response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return errors.New("Local package health is unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 32*1024+1))
	var health core.Health
	if err != nil || len(data) > 32*1024 || json.Unmarshal(data, &health) != nil || health.Status != "ok" || health.APIVersion != core.APIVersion ||
		health.InstanceID != instance || instance == "" || health.Connection == nil || health.Connection.Location != "local" ||
		health.Connection.Transport != "loopback" || health.Connection.OwnerUID != owner {
		return errors.New("Local package health differs from its verified endpoint")
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	response.TransferEncoding = nil
	return nil
}

// Before the helper records a fresh package's core identity, only its fixed
// read-only health probe may reach the private app socket.
func packageOwnerBootstrapProbe(r *http.Request) bool {
	return r != nil && r.URL != nil && r.Method == http.MethodGet && r.URL.Path == "/api/v1/health" && r.URL.RawQuery == "" &&
		r.ContentLength == 0 && len(r.TransferEncoding) == 0 && r.Header.Get("Authorization") == "" && r.Header.Get("Cookie") == ""
}

func nativeClientRequestAllowed(r *http.Request) bool {
	if r.URL.User != nil || r.URL.Opaque != "" || (r.URL.Scheme != "" && r.URL.Scheme != "http") || strings.Contains(r.URL.Path, "\\") {
		return false
	}
	return desktop.PublicRequestAllowed(r) || (r.Method == "POST" && r.URL.Path == "/api/v1/auth/native-local" && r.URL.RawQuery == "")
}
