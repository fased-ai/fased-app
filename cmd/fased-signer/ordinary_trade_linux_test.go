//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
	"github.com/fased-ai/fased-app/internal/signer"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProtectedOrdinarySwapApprovalUsesScopedCapability(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	chain := &onchaintest.Chain{}
	client := onchaintest.Client(chain)
	v, e := signer.OpenControlledOwnerCustody(dir, strings.Repeat("e", 32), "mainnet-beta")
	if e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	if e = v.ConfigureControlledOrdinarySwapObserver(client); e != nil {
		t.Fatal(e)
	}
	if e = v.Create(flowUnlock); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	pub, _ := hex.DecodeString(ref.PublicKey)
	now := time.Now().Unix()
	q, e := client.Prepare(t.Context(), onchain.Quote{Version: onchain.Version, ID: strings.Repeat("1", 32), ProfileID: "trading-analyst", InstanceID: strings.Repeat("2", 32), AccountID: strings.Repeat("3", 32), Network: ref.Network, Owner: onchain.Base58(pub), RPCSHA256: strings.Repeat("4", 64), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000, CreatedAt: now, ExpiresAt: now + 45})
	if e != nil {
		t.Fatal(e)
	}
	p := signer.NewSwapProposal(ref, q)
	call := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		switch r.Action {
		case "status":
			return signer.Response{Status: v.Status()}, nil
		case "unlock":
			e := v.Unlock(r.Password, time.Duration(r.UnlockSeconds)*time.Second)
			return signer.Response{Status: v.Status()}, e
		case "lock":
			v.Lock()
			return signer.Response{}, nil
		case "swap-approve":
			x, e := v.ApproveSwap(*r.Swap)
			return signer.Response{Swap: x, Status: v.Status()}, e
		}
		return signer.Response{}, errors.New("unexpected owner operation")
	}
	var review, out bytes.Buffer
	asked := false
	ask := func(string, bool) ([]byte, error) { asked = true; return bytes.Clone(flowUnlock), nil }
	if e = conductSwapApproval(t.Context(), p, ask, func(string) error { return errors.New("cancel") }, &review, &out, call); e == nil || asked {
		t.Fatal("cancel unlocked")
	}
	if e = conductSwapApproval(t.Context(), p, ask, func(s string) error {
		if s != "APPROVE "+p.Digest {
			return errors.New("wrong identity")
		}
		return nil
	}, &review, &out, call); e != nil {
		t.Fatal(e)
	}
	if _, e = v.SignSwap(q.ID); e != nil {
		t.Fatal(e)
	}
	if !v.Status().RecoveryRequired || v.Status().UnlockUntil.Unix() > q.ExpiresAt {
		t.Fatal("generic hold or bounded lease lost")
	}
	if !strings.Contains(review.String(), q.Owner) || !strings.Contains(review.String(), "12.5 USDC") {
		t.Fatal("trusted review incomplete")
	}
}
