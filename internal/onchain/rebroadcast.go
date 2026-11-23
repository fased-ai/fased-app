package onchain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

const (
	// DefaultRebroadcastMaxAttempts bounds leader rebroadcast retries per signed packet.
	DefaultRebroadcastMaxAttempts = 3

	// MaxRebroadcastAttempts hard-caps caller-requested rebroadcast attempts.
	MaxRebroadcastAttempts = 5

	// DefaultRebroadcastInitialDelay sets the initial backoff before rebroadcast checks.
	DefaultRebroadcastInitialDelay = 400 * time.Millisecond

	// DefaultRebroadcastMaxDelay bounds exponential rebroadcast backoff.
	DefaultRebroadcastMaxDelay = 4 * time.Second
)

// RebroadcastConfig configures bounded rebroadcast behavior for an already-signed
// transaction packet without mutating or re-signing transaction bytes.
type RebroadcastConfig struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Sleep        func(context.Context, time.Duration) error
}

// RebroadcastResult summarizes bounded submission/rebroadcast progress for callers.
type RebroadcastResult struct {
	Attempted bool
	Confirmed bool
	Expired   bool
	Attempts  int
	Signature string
	LastError string
}

func defaultRebroadcastSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// SendWithRebroadcast submits an immutable signed transaction packet up to
// cfg.MaxAttempts (default 3, hard cap 5), checking getSignatureStatuses and
// LastValidHeight before rebroadcasting identical signed bytes.
// Client.Send remains strictly single-shot for policy callers that require one send.
func (c *Client) SendWithRebroadcast(ctx context.Context, q Quote, packet []byte, cfg RebroadcastConfig) (RebroadcastResult, error) {
	if cfg.MaxAttempts < 0 || cfg.MaxAttempts > MaxRebroadcastAttempts {
		return RebroadcastResult{}, errors.New("rebroadcast attempts outside supported bounds")
	}
	if err := c.admitNetwork(q); err != nil {
		return RebroadcastResult{}, err
	}
	signature, err := VerifyPacket(q, packet)
	if err != nil {
		return RebroadcastResult{}, err
	}

	maxAttempts := cfg.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = DefaultRebroadcastMaxAttempts
	}
	delay := cfg.InitialDelay
	if delay <= 0 {
		delay = DefaultRebroadcastInitialDelay
	}
	maxDelay := cfg.MaxDelay
	if maxDelay <= 0 {
		maxDelay = DefaultRebroadcastMaxDelay
	}
	if maxDelay < delay {
		maxDelay = delay
	}
	sleepFn := cfg.Sleep
	if sleepFn == nil {
		sleepFn = defaultRebroadcastSleep
	}

	res := RebroadcastResult{Signature: signature}
	packetB64 := base64.StdEncoding.EncodeToString(packet)

	checkStatus := func() (confirmed, failedOnChain bool, statusErr error) {
		var status struct {
			Value []*struct {
				Err          json.RawMessage `json:"err"`
				Confirmation string          `json:"confirmationStatus"`
			} `json:"value"`
		}
		if callErr := c.rpcCall(ctx, "getSignatureStatuses", []any{
			[]string{signature},
			map[string]any{"searchTransactionHistory": true},
		}, &status); callErr != nil {
			return false, false, callErr
		}
		if len(status.Value) != 1 || status.Value[0] == nil {
			return false, false, nil
		}
		v := status.Value[0]
		if len(v.Err) > 0 && string(v.Err) != "null" {
			return false, true, nil
		}
		if string(v.Err) == "null" && (v.Confirmation == "confirmed" || v.Confirmation == "finalized") {
			return true, false, nil
		}
		return false, false, nil
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if attempt > 1 {
			confirmed, failedOnChain, statusErr := checkStatus()
			if statusErr == nil {
				if confirmed {
					res.Confirmed = true
					res.LastError = ""
					return res, nil
				}
				if failedOnChain {
					res.LastError = "transaction failed on-chain; reconcile original signature"
					return res, errors.New(res.LastError)
				}
			}
		}

		var height uint64
		if err := c.rpcCall(ctx, "getBlockHeight", []any{map[string]any{"commitment": "confirmed"}}, &height); err != nil {
			res.LastError = err.Error()
			if attempt == 1 {
				return res, err
			}
			return res, errors.New("submission uncertain; reconcile the original signature")
		}
		if height > q.LastValidHeight {
			res.Expired = true
			res.LastError = "swap blockhash expired"
			return res, errors.New("swap blockhash expired")
		}

		var actual string
		sendErr := c.rpcCall(ctx, "sendTransaction", []any{
			packetB64,
			map[string]any{
				"encoding":            "base64",
				"skipPreflight":       false,
				"preflightCommitment": "confirmed",
				"maxRetries":          0,
				"minContextSlot":      q.ObservedSlot,
			},
		}, &actual)
		res.Attempted = true
		res.Attempts++

		sendOK := sendErr == nil && actual == signature
		if sendOK {
			res.LastError = ""
			if maxAttempts == 1 {
				return res, nil
			}
		} else if sendErr != nil {
			res.LastError = sendErr.Error()
		} else {
			res.LastError = "submission signature mismatch"
		}

		if sendOK || attempt < maxAttempts {
			if err := sleepFn(ctx, delay); err != nil {
				return res, err
			}
		}
		if sendOK {
			confirmed, failedOnChain, statusErr := checkStatus()
			if statusErr == nil {
				if confirmed {
					res.Confirmed = true
					return res, nil
				}
				if failedOnChain {
					res.LastError = "transaction failed on-chain; reconcile original signature"
					return res, errors.New(res.LastError)
				}
			}
			if attempt == maxAttempts {
				return res, nil
			}
		} else if attempt == maxAttempts {
			return res, errors.New("submission uncertain; reconcile the original signature")
		}

		nextDelay := delay * 2
		if nextDelay > maxDelay {
			nextDelay = maxDelay
		}
		delay = nextDelay
	}

	return res, nil
}
