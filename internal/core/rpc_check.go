package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

const FixtureRPCURL = "https://rpc.fased.invalid/solana"
const fixtureGenesis = "11111111111111111111111111111112"

type RPCCheckRequest struct {
	AccountID       string `json:"accountId,omitempty"`
	AccountRevision *int64 `json:"accountRevision,omitempty"`
	Endpoint        string `json:"endpoint"`
	Network         string `json:"network"`
}
type RPCCheckResult struct {
	Account         *Account  `json:"account,omitempty"`
	AccountRevision *int64    `json:"accountRevision,omitempty"`
	AccountState    string    `json:"accountState,omitempty"`
	Endpoint        string    `json:"endpoint"`
	Network         string    `json:"network"`
	GenesisHash     string    `json:"genesisHash"`
	CheckedAt       time.Time `json:"checkedAt"`
	FixtureOnly     bool      `json:"fixtureOnly"`
	LiveRPC         bool      `json:"liveRPC"`
}

// CheckFixtureRPC preserves fixture behavior; public endpoints reach the dormant
// production checker, whose ordinary constructor refuses before DNS or dispatch.
func CheckFixtureRPC(ctx context.Context, input RPCCheckRequest) (RPCCheckResult, error) {
	if input.Endpoint == FixtureRPCURL {
		return checkRPC(ctx, input, fixtureRPCTransport{})
	}
	return checkPublicRPC(ctx, input, newPublicRPCTransport())
}
func checkRPC(ctx context.Context, input RPCCheckRequest, rt http.RoundTripper) (RPCCheckResult, error) {
	var empty RPCCheckResult
	// Exact admission precedes any transport. Reject credentials, alternate hosts,
	// ports, queries, fragments and private destinations without echoing input.
	if input.Endpoint != FixtureRPCURL || input.Network != signer.FixtureNetwork || rt == nil {
		return empty, errors.New("only the disposable RPC endpoint and network are available")
	}
	return checkRPCProtocol(ctx, input, rt, fixtureGenesis, true)
}

func checkRPCProtocol(ctx context.Context, input RPCCheckRequest, rt http.RoundTripper, genesis string, fixture bool) (RPCCheckResult, error) {
	var empty RPCCheckResult
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	client := http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("RPC redirects refused") }}
	for i, method := range []string{"getGenesisHash", "getHealth"} {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": method})
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, input.Endpoint, bytes.NewReader(body))
		if e != nil {
			return empty, errors.New("RPC request unavailable")
		}
		req.Header.Set("Content-Type", "application/json")
		response, e := client.Do(req)
		if e != nil {
			return empty, errors.New("RPC check failed")
		}
		data, e := io.ReadAll(io.LimitReader(response.Body, 4097))
		response.Body.Close()
		if e != nil || response.StatusCode != 200 || len(data) > 4096 {
			return empty, errors.New("RPC response refused")
		}
		result, e := rpcResult(data, i+1)
		if e != nil {
			return empty, e
		}
		if i == 0 && result != genesis {
			return empty, errors.New("RPC network mismatch")
		}
		if i == 1 && result != "ok" {
			return empty, errors.New("RPC unhealthy")
		}
	}
	return RPCCheckResult{Endpoint: input.Endpoint, Network: input.Network, GenesisHash: genesis, CheckedAt: time.Now().UTC(), FixtureOnly: fixture, LiveRPC: !fixture}, nil
}

func rpcResult(data []byte, id int) (string, error) {
	fail := errors.New("invalid RPC response")
	d := json.NewDecoder(bytes.NewReader(data))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return "", fail
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return "", fail
		}
		name, ok := key.(string)
		if !ok {
			return "", fail
		}
		if _, exists := fields[name]; exists {
			return "", fail
		}
		if name != "jsonrpc" && name != "id" && name != "result" {
			return "", fail
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return "", fail
		}
		fields[name] = value
	}
	if _, e = d.Token(); e != nil || d.Decode(new(any)) != io.EOF || len(fields) != 3 {
		return "", fail
	}
	var version, result string
	var replyID int
	if json.Unmarshal(fields["jsonrpc"], &version) != nil || version != "2.0" || json.Unmarshal(fields["id"], &replyID) != nil || replyID != id || json.Unmarshal(fields["result"], &result) != nil || result == "" {
		return "", fail
	}
	return result, nil
}

type fixtureRPCTransport struct{}

func (fixtureRPCTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if e := r.Context().Err(); e != nil {
		return nil, e
	}
	if r.URL.String() != FixtureRPCURL || r.Method != http.MethodPost || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
		return nil, errors.New("fixture RPC request refused")
	}
	var request struct {
		Method string `json:"method"`
		ID     int    `json:"id"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		return nil, errors.New("invalid fixture RPC request")
	}
	result := fixtureGenesis
	if request.Method == "getHealth" {
		result = "ok"
	} else if request.Method != "getGenesisHash" {
		return nil, errors.New("fixture RPC method refused")
	}
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
}

// CheckAccountRPC binds a diagnostic to a saved public account and checks the
// signer epoch both before and after the request. It grants no wallet authority.
func (s *Store) CheckAccountRPC(ctx context.Context, input RPCCheckRequest) (RPCCheckResult, error) {
	if s.rpcCheckFixture != nil {
		return s.checkAccountRPC(ctx, input, s.rpcCheckFixture)
	}
	return s.checkAccountRPC(ctx, input, CheckFixtureRPC)
}
func (s *Store) checkAccountRPC(ctx context.Context, input RPCCheckRequest, check func(context.Context, RPCCheckRequest) (RPCCheckResult, error)) (RPCCheckResult, error) {
	if input.AccountID == "" && input.AccountRevision == nil {
		return check(ctx, input)
	}
	var empty RPCCheckResult
	if input.Endpoint == "" && input.Network == "" && input.AccountID != "" {
		record, err := s.accountsRecord(ctx)
		if err != nil {
			return empty, err
		}
		for _, a := range record.Accounts {
			if a.ID == input.AccountID && a.RPC != nil {
				input.Endpoint = a.RPC.Endpoint
				input.Network = a.RPC.Network
			}
		}
		if input.Endpoint == "" {
			return empty, errors.New("save RPC settings first")
		}
	}
	if input.AccountID == "" || input.AccountRevision == nil || *input.AccountRevision < 0 {
		return empty, errors.New("choose a saved wallet revision")
	}
	lookup := func() (Account, string, error) {
		record, err := s.accountsRecord(ctx)
		if err != nil {
			return Account{}, "", err
		}
		if record.Revision != *input.AccountRevision {
			return Account{}, "", errors.New("wallet changed; refresh and check again")
		}
		for _, a := range record.Accounts {
			if a.ID != input.AccountID {
				continue
			}
			if a.Network != input.Network {
				return Account{}, "", errors.New("wallet network mismatch")
			}
			state := "public-record-only"
			if a.Mode == "fixture-managed" {
				live := s.fixtureView(ctx)
				if !live.Available || live.Status == nil || live.Status.Account == nil || a.Signer == nil || *live.Status.Account != *a.Signer {
					return Account{}, "", errors.New("wallet reference is stale or signer unavailable")
				}
				state = "current-fixture"
			}
			return a, state, nil
		}
		return Account{}, "", errors.New("wallet unavailable; refresh and check again")
	}
	if _, _, err := lookup(); err != nil {
		return empty, err
	}
	result, err := check(ctx, input)
	if err != nil {
		return empty, err
	}
	account, state, err := lookup()
	if err != nil {
		return empty, err
	}
	result.Account = &account
	revision := *input.AccountRevision
	result.AccountRevision = &revision
	result.AccountState = state
	return result, nil
}
