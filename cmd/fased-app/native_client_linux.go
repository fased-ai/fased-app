//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"os"
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
	m, err := custodyinstall.Load(cfg.OwnerUID)
	if err != nil || !m.NativeOwnerAccess || m.Location != "local" || !m.Enrollment || m.App.UID != os.Getuid() || custodyinstall.Verify(m) != nil || signer.CheckApplication(*config, m.Root, *data) != nil {
		return errors.New("protected Local owner helper required")
	}
	commit, err := custodyinstall.ReadEnrollmentCommit(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := core.NewClient(ctx, *data)
	if err != nil {
		return err
	}
	defer client.Close()
	if client.Endpoint.InstanceID != commit.InstanceID {
		return errors.New("native owner instance changed")
	}
	r, err := http.ReadRequest(bufio.NewReader(io.LimitReader(in, 64*1024)))
	if err != nil {
		return errors.New("invalid native request")
	}
	defer r.Body.Close()
	if r.ContentLength > 16*1024 || !nativeClientRequestAllowed(r) {
		return errors.New("native route unavailable")
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
	response.Close = true
	return response.Write(out)
}

func nativeClientRequestAllowed(r *http.Request) bool {
	if r.URL.User != nil || r.URL.Opaque != "" || (r.URL.Scheme != "" && r.URL.Scheme != "http") || strings.Contains(r.URL.Path, "\\") {
		return false
	}
	return desktop.PublicRequestAllowed(r) || (r.Method == "POST" && r.URL.Path == "/api/v1/auth/native-local" && r.URL.RawQuery == "")
}
