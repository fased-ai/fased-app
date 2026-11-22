package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	fasedapp "github.com/fased-ai/fased-app"
	"github.com/fased-ai/fased-app/internal/core"
)

func TestPairCLILiveIPCAndOfflineFallback(t *testing.T) {
	t.Parallel()

	// Offline issuance, QR rendering, SSH tunnel command, list, and revoke.
	offlineDir := shortFixtureDir(t, "pair-off-")
	if err := os.MkdirAll(offlineDir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := core.OpenStore(offlineDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	var textOut bytes.Buffer
	if err = run([]string{"pair", "--data-dir", offlineDir, "--label", "Laptop"}, nil, &textOut, io.Discard); err != nil {
		t.Fatalf("offline pair failed: %v", err)
	}
	outStr := textOut.String()
	if !strings.Contains(outStr, "#pair=") || !strings.Contains(outStr, "ssh -N -L 7331:127.0.0.1:7331") || !strings.Contains(outStr, "█") {
		t.Fatalf("unexpected pair text output:\n%s", outStr)
	}

	// JSON issuance against a running Remote Cloudflare daemon via owner Unix IPC.
	liveDir := shortFixtureDir(t, "pair-live-")
	srv, err := core.Start(t.Context(), liveDir, "127.0.0.1:0", "https://fased.example.com", fasedapp.UI())
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.ConfigureConnectionWithTransport("remote", "cloudflare"); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	defer srv.Close(context.Background())

	var jsonOut bytes.Buffer
	if err = run([]string{"pair", "--data-dir", liveDir, "--label", "Phone", "--json"}, nil, &jsonOut, io.Discard); err != nil {
		t.Fatalf("live IPC pair failed: %v", err)
	}
	var issued struct {
		ID              string `json:"id"`
		Code            string `json:"code"`
		URL             string `json:"url"`
		RemoteTransport string `json:"remoteTransport"`
	}
	if err = json.Unmarshal(jsonOut.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if len(issued.Code) != 43 || issued.RemoteTransport != "cloudflare" || !strings.HasPrefix(issued.URL, "https://fased.example.com/?instance=") {
		t.Fatalf("unexpected live pair JSON: %+v", issued)
	}

	// Exchange code directly in the store, then verify pair list and pair revoke over IPC.
	if _, _, err = srv.Store.ExchangePairingCode(t.Context(), issued.Code, "", "Browser", "cloudflare", ""); err != nil {
		t.Fatal(err)
	}

	var listOut bytes.Buffer
	if err = run([]string{"pair", "list", "--data-dir", liveDir, "--json"}, nil, &listOut, io.Discard); err != nil {
		t.Fatalf("pair list failed: %v", err)
	}
	var listed struct {
		Devices []core.PairedDevice `json:"devices"`
	}
	if err = json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Devices) != 1 || listed.Devices[0].Label != "Phone" {
		t.Fatalf("expected 1 Phone device, got %+v", listed)
	}

	// Server status reflects live transport and paired device count.
	var statusOut bytes.Buffer
	if err = run([]string{"server", "status", "--data-dir", liveDir, "--json"}, nil, &statusOut, io.Discard); err != nil {
		t.Fatalf("server status failed: %v", err)
	}
	if !strings.Contains(statusOut.String(), `"transport":"cloudflare"`) || !strings.Contains(statusOut.String(), `"pairedDevices":1`) {
		t.Fatalf("unexpected server status: %s", statusOut.String())
	}

	var revOut bytes.Buffer
	if err = run([]string{"pair", "revoke", "--data-dir", liveDir, listed.Devices[0].ID}, nil, &revOut, io.Discard); err != nil {
		t.Fatalf("pair revoke failed: %v", err)
	}
	if !strings.Contains(revOut.String(), "Revoked 1 paired device(s).") {
		t.Fatalf("unexpected revoke output: %s", revOut.String())
	}
	devicesAfter, _, err := srv.Store.ListPairedDevices(t.Context(), "")
	if err != nil || len(devicesAfter) != 0 {
		t.Fatalf("device not revoked: %+v %v", devicesAfter, err)
	}
}
