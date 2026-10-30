package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	fasedapp "github.com/fased-ai/fased-app"
	"github.com/fased-ai/fased-app/internal/signer"
)

// The private parent pipe supplies public fixture owner approvals. This driver
// exists only in a test executable; no application HTTP/CLI route enables it.
func TestOnchainBrowserServer(t *testing.T) {
	dir := os.Getenv("FASED_ONCHAIN_BROWSER_DIR")
	if dir == "" {
		t.Skip("explicit disposable fixture only")
	}
	if os.Getuid() <= 0 || filepath.Dir(dir) != os.TempDir() || !strings.HasPrefix(filepath.Base(dir), "fased-swap-") {
		t.Fatal("disposable non-root fixture directory required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server, e := Start(ctx, dir, "127.0.0.1:0", "", fasedapp.UI())
	if e != nil {
		t.Fatal(e)
	}
	server.taskCancel()
	<-server.taskDone
	var h *swapHarness
	credentialMode := os.Getenv("FASED_CREDENTIAL_ONCHAIN_BROWSER") == "1"
	if credentialMode {
		h, _, _ = setupCredentialSwapHarness(t, server.Store, false)
	} else if os.Getenv("FASED_MANAGED_ONCHAIN_BROWSER") == "1" {
		h = setupManagedSwapHarness(t, server.Store)
		if e = h.vault.Unlock(h.password, 45*time.Second); e != nil {
			t.Fatal(e)
		}
	} else {
		h = setupSwapHarness(t, server.Store)
	}
	code, e := server.Store.IssueClaimCode(ctx)
	if e != nil {
		t.Fatal(e)
	}
	server.startAgentTasks()
	defer server.Close(context.Background())
	go func() {
		if e := server.Serve(); e != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, e)
		}
	}()
	json.NewEncoder(os.Stdout).Encode(map[string]any{"event": "listening", "url": server.URL, "claim": code.Code, "instanceId": server.Store.instanceID})
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 4096), 32768)
		for scanner.Scan() {
			p, e := signer.ParseSwapProposal(scanner.Bytes())
			if e == nil && credentialMode {
				e = h.vault.Unlock(h.password, 45*time.Second)
				if e == nil {
					_, e = h.vault.UnlockReceiptRPC(h.password, time.Minute)
				}
			}
			if e == nil {
				_, e = h.vault.ApproveSwap(p)
			}
			if e != nil {
				json.NewEncoder(os.Stdout).Encode(map[string]string{"event": "approval", "error": e.Error()})
			} else {
				json.NewEncoder(os.Stdout).Encode(map[string]string{"event": "approval", "digest": p.Digest})
			}
		}
		cancel()
	}()
	<-ctx.Done()
	h.chain.RecordEvidence(filepath.Join(dir, "transport.json"))
}
