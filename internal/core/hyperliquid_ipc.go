package core

import (
	"context"
	"errors"
	"time"

	"github.com/fased-ai/fased-app/internal/hyperliquid"
	"github.com/fased-ai/fased-app/internal/signer"
)

const hyperliquidSignerTimeout = 5 * time.Second

type ipcHyperliquidSigner struct {
	call         func(context.Context, signer.Request) (signer.Response, error)
	agentVaultID string
}

func (b ipcHyperliquidSigner) Address() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), hyperliquidSignerTimeout)
	defer cancel()
	resp, err := b.call(ctx, signer.Request{Action: "hyperliquid-status"})
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", errors.New(resp.Error)
	}
	if resp.HyperliquidStatus == nil || !resp.HyperliquidStatus.Enabled || resp.HyperliquidStatus.Address == "" {
		return "", signer.ErrUnavailable
	}
	return resp.HyperliquidStatus.Address, nil
}

func (b ipcHyperliquidSigner) SignUpdateLeverage(ctx context.Context, intent signer.HyperliquidUpdateLeverageIntent, _ signer.HyperliquidPolicy) (*signer.HyperliquidSignedAction, error) {
	resp, err := b.call(ctx, signer.Request{
		Action:              "hyperliquid-sign-leverage",
		HyperliquidLeverage: &intent,
		AgentVaultID:        b.agentVaultID,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	if resp.HyperliquidSigned == nil {
		return nil, errors.New("missing signed hyperliquid updateLeverage action")
	}
	return resp.HyperliquidSigned, nil
}

func (b ipcHyperliquidSigner) SignOrder(ctx context.Context, intent signer.HyperliquidOrderIntent, _ signer.HyperliquidPolicy) (*signer.HyperliquidSignedAction, error) {
	resp, err := b.call(ctx, signer.Request{
		Action:           "hyperliquid-sign-order",
		HyperliquidOrder: &intent,
		AgentVaultID:     b.agentVaultID,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	if resp.HyperliquidSigned == nil {
		return nil, errors.New("missing signed hyperliquid order action")
	}
	return resp.HyperliquidSigned, nil
}

func (b ipcHyperliquidSigner) SignCancel(ctx context.Context, intent signer.HyperliquidCancelIntent, _ signer.HyperliquidPolicy) (*signer.HyperliquidSignedAction, error) {
	resp, err := b.call(ctx, signer.Request{
		Action:            "hyperliquid-sign-cancel",
		HyperliquidCancel: &intent,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	if resp.HyperliquidSigned == nil {
		return nil, errors.New("missing signed hyperliquid cancel action")
	}
	return resp.HyperliquidSigned, nil
}

// ConfigureHyperliquidIPC wires Core's controlled Hyperliquid runtime to an external
// fased-signer IPC channel without holding a *signer.Vault pointer inside Core.
// Signer-persisted policy and nonces govern all signing; mainnet stays blocked while
// hyperliquidActivationEnabled == false.
func (s *Store) ConfigureHyperliquidIPC(client *hyperliquid.Client, signerCall func(context.Context, signer.Request) (signer.Response, error), agentVaultID string) error {
	if s == nil || client == nil || signerCall == nil || !client.Controlled() {
		return signer.ErrUnavailable
	}
	if hyperliquidActivationEnabled || signer.HyperliquidActivationEnabled {
		return signer.ErrUnavailable
	}
	statusCtx, cancel := context.WithTimeout(context.Background(), hyperliquidSignerTimeout)
	defer cancel()
	resp, err := signerCall(statusCtx, signer.Request{Action: "hyperliquid-status"})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	st := resp.HyperliquidStatus
	if st == nil || !st.Enabled || st.Policy == nil || st.Address == "" {
		return signer.ErrUnavailable
	}
	return s.configureHyperliquidRuntime(
		client,
		ipcHyperliquidSigner{call: signerCall, agentVaultID: agentVaultID},
		*st.Policy,
		st.Address,
		st.LastNonce,
	)
}

// ConfigureHyperliquidIPC exposes controlled signer-IPC Hyperliquid wiring on Server.
func (server *Server) ConfigureHyperliquidIPC(client *hyperliquid.Client, signerCall func(context.Context, signer.Request) (signer.Response, error), agentVaultID string) error {
	if server == nil || server.Store == nil {
		return signer.ErrUnavailable
	}
	return server.Store.ConfigureHyperliquidIPC(client, signerCall, agentVaultID)
}
