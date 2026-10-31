package core

import (
	"context"
	"net/http"

	"github.com/fased-ai/fased-app/internal/signer"
)

// ConfigureControlledWalletReads is a compiled acceptance constructor only.
// Start configures custodyOwnerSupported from the verified controlled-disposable
// v3 identity. No ordinary CLI, environment, browser or IPC installs a transport.
// The normal live transport and credential admission remain disabled.
func (s *Server) ConfigureControlledWalletReads(next *http.Transport) error {
	if s == nil || s.Store == nil || next == nil {
		return signer.ErrUnavailable
	}
	store := s.Store
	if !store.walletReadMu.TryLock() {
		return signer.ErrUnavailable
	}
	defer store.walletReadMu.Unlock()
	if !store.custodyOwnerSupported || solanaGenesis(store.custodyNetwork) == "" || store.rpcAdmission || store.providerFixture || store.sessionFixture || store.walletReadFixture != nil {
		return signer.ErrUnavailable
	}
	public := &publicRPCTransport{enabled: true, transport: next}
	store.walletReadFixture = func(ctx context.Context, a Account, address string, key []byte, valid func() error, selection *WalletSelection) (WalletSnapshot, error) {
		transport := &providerRPCTransport{origin: a.RPC.Endpoint, provider: rpcProvider(a.RPC.Endpoint, a.Network), key: key, valid: valid, next: public}
		return walletRPC(ctx, a.RPC.Endpoint, transport, valid, true, address, a.Network, selection)
	}
	return nil
}
