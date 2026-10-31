package signer

import (
	"context"
	"errors"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/update"
)

const ManagedFixtureMode = "disposable-custody:v2"

type Config struct {
	Profile    string `json:"profile,omitempty"`
	Provenance string `json:"provenance,omitempty"`
	Network    string `json:"network,omitempty"`
	Version    int    `json:"version"`
	Mode       string `json:"mode"`
	SignerID   string `json:"signerId"`
	OwnerUID   int    `json:"ownerUid"`
	AppUID     int    `json:"appUid"`
	SignerUID  int    `json:"signerUid"`
	StateDir   string `json:"stateDir"`
	SocketDir  string `json:"socketDir"`
}

func (c Config) Validate() error {
	modeOK := c.Version == 1 && c.Mode == FixtureNetwork && c.Network == "" || c.Version == 2 && c.Mode == ManagedFixtureMode && managedNetwork(c.Network) || c.Version == 3 && c.Mode == OrdinaryCustodyMode && managedNetwork(c.Network)
	contextOK := c.Version != 3 && c.Profile == "" && c.Provenance == "" || c.Version == 3 && c.Profile == OwnerCustodyProfile && (c.Provenance == ControlledCustodyProfile || c.Provenance == OwnerKeyProvenance)
	if !modeOK || !contextOK || !validID(c.SignerID) || c.OwnerUID <= 0 || c.AppUID <= 0 || c.SignerUID <= 0 ||
		c.OwnerUID == c.AppUID || c.OwnerUID == c.SignerUID || c.AppUID == c.SignerUID || c.StateDir == c.SocketDir {
		return errors.New("fixture requires a versioned identity and three distinct non-root UIDs")
	}
	return nil
}

// Request is a closed set of owner and fixture operations. No real sign/submit.
// Passwords are bytes so callers can clear their own buffers after use.
type Request struct {
	RPCOrigin     string                   `json:"rpcOrigin,omitempty"`
	RPCKey        []byte                   `json:"rpcKey,omitempty"`
	Install       *update.InstallRequest   `json:"install,omitempty"`
	Preflight     *update.PreflightRequest `json:"preflight,omitempty"`
	Stage         *update.StageRequest     `json:"stage,omitempty"`
	Update        *update.IntentRequest    `json:"update,omitempty"`
	ProbeID       string                   `json:"probeId,omitempty"`
	IntentID      string                   `json:"intentId,omitempty"`
	Action        string                   `json:"action"`
	Password      []byte                   `json:"password,omitempty"`
	NewPassword   []byte                   `json:"newPassword,omitempty"`
	Document      []byte                   `json:"document,omitempty"`
	UnlockSeconds int                      `json:"unlockSeconds,omitempty"`
	PublicInput
}
type Response struct {
	Swap            *SwapReceipt           `json:"swap,omitempty"`
	Installation    *update.Installation   `json:"installation,omitempty"`
	Preflight       *update.Preflight      `json:"preflight,omitempty"`
	Stage           *update.StageStatus    `json:"stage,omitempty"`
	UpdateInventory *update.SignerSnapshot `json:"updateInventory,omitempty"`
	UpdateIntent    *update.Intent         `json:"updateIntent,omitempty"`
	Approvals       []ApprovalReceipt      `json:"approvals,omitempty"`
	Status          Status                 `json:"status"`
	Document        []byte                 `json:"document,omitempty"`
	Error           string                 `json:"error,omitempty"`
	Fixture         *FixtureSummary        `json:"fixture,omitempty"`
	Operation       *FixtureOperation      `json:"operation,omitempty"`
}

func execute(v *Vault, r Request, owner bool) Response {
	defer wipe(r.RPCKey)
	defer wipe(r.Password)
	defer wipe(r.NewPassword)
	if r.Action == "import-fixture-seed" || r.Action == "import-managed-seed" || r.Action == "import-ordinary-seed" {
		defer wipe(r.Document)
	}
	if v.ordinary {
		switch r.Action {
		case "status", "create", "import-ordinary-seed", "unlock", "lock", "backup", "verify-backup", "restore":
		default:
			return Response{Error: ErrUnavailable.Error()}
		}
	}
	var document []byte
	var err error
	if !owner && !AppActionAllowed(r.Action) {
		return Response{Error: "owner administration denied"}
	}
	if r.RPCOrigin != "" || len(r.RPCKey) > 0 || r.Action == "receipt-rpc-save" || r.Action == "receipt-rpc-unlock" {
		expected := Request{Action: r.Action, Password: r.Password}
		if r.Action == "receipt-rpc-save" {
			expected.RPCOrigin = r.RPCOrigin
			expected.RPCKey = r.RPCKey
		} else if r.Action == "receipt-rpc-unlock" {
			expected.UnlockSeconds = r.UnlockSeconds
		} else {
			return Response{Error: "unexpected receipt RPC fields"}
		}
		if onchain.Hash(expected) != onchain.Hash(r) {
			return Response{Error: "one typed owner receipt RPC action required"}
		}
	}
	if r.Swap != nil && r.Action != "swap-approve" {
		return Response{Error: "unexpected swap approval fields"}
	}
	if r.Action == "swap-approve" || r.Action == "swap-sign" || r.Action == "swap-receipt" || r.Action == "swap-reconcile" || r.Action == "swap-cancel" {
		expected := Request{Action: r.Action}
		if r.Action == "swap-approve" {
			expected.Swap = r.Swap
		} else {
			expected.OperationID = r.OperationID
		}
		if onchain.Hash(expected) != onchain.Hash(r) {
			return Response{Error: "one typed swap action required"}
		}
	}
	if v.network != "" {
		switch r.Action {
		case "receipt-rpc-save", "receipt-rpc-unlock", "swap-approve", "swap-sign", "swap-receipt", "swap-reconcile", "swap-cancel", "status", "create", "import-managed-seed", "import-ordinary-seed", "unlock", "lock", "backup", "verify-backup", "restore", "fixture-update-inventory", "fixture-update-status", "fixture-update-prepare", "fixture-update-abort", "fixture-update-stage-write", "fixture-update-stage-status", "fixture-update-stage-finish", "fixture-update-preflight", "fixture-install-controller":
		default:
			return Response{Error: ErrUnavailable.Error()}
		}
	}
	switch r.Action {
	case "receipt-rpc-save":
		_, err = v.ProvisionReceiptRPC(r.RPCOrigin, r.RPCKey, r.Password)
	case "receipt-rpc-unlock":
		_, err = v.UnlockReceiptRPC(r.Password, time.Duration(r.UnlockSeconds)*time.Second)
	case "swap-approve":
		if r.Swap == nil {
			return Response{Error: "swap approval document required"}
		}
		x, e := v.ApproveSwap(*r.Swap)
		out := Response{Swap: x, Status: v.Status()}
		if e != nil {
			out.Error = e.Error()
		}
		return out
	case "swap-sign", "swap-receipt", "swap-reconcile", "swap-cancel":
		var x *SwapReceipt
		var e error
		switch r.Action {
		case "swap-cancel":
			x, e = v.CancelSwap(r.OperationID)
		case "swap-sign":
			x, e = v.SignSwap(r.OperationID)
		case "swap-receipt":
			x, e = v.SwapReceipt(r.OperationID)
		case "swap-reconcile":
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			x, e = v.ReconcileSwap(ctx, r.OperationID)
		}
		out := Response{Swap: x, Status: v.Status()}
		if e != nil {
			out.Error = e.Error()
		}
		return out
	case "import-managed-seed":
		err = v.importManagedSeed(r.Document, r.Password)
	case "fixture-install-controller":
		if r.Install == nil {
			return Response{Error: "bound installation command required"}
		}
		i, e := v.Installation(*r.Install)
		out := Response{Installation: i, Status: v.Status()}
		if e != nil {
			out.Error = e.Error()
		}
		return out
	case "fixture-update-preflight":
		if r.Preflight == nil {
			return Response{Error: "bound preflight request required"}
		}
		p, e := v.PreflightUpdate(*r.Preflight)
		out := Response{Preflight: p, Status: v.Status()}
		if e != nil {
			out.Error = e.Error()
		}
		return out

	case "fixture-update-stage-write", "fixture-update-stage-status", "fixture-update-stage-finish":
		if r.Stage == nil {
			return Response{Error: "bound staging request required"}
		}
		s, e := v.StageUpdate(r.Action, *r.Stage)
		out := Response{Stage: s, Status: v.Status()}
		if e != nil {
			out.Error = e.Error()
		}
		return out

	case "fixture-update-status":
		i, e := v.UpdateIntent(r.IntentID)
		out := Response{UpdateIntent: i, Status: v.Status()}
		if e != nil {
			out.Error = e.Error()
		}
		return out
	case "fixture-update-inventory":
		s, e := v.UpdateInventory(r.ProbeID)
		out := Response{UpdateInventory: s, Status: v.Status()}
		if e != nil {
			out.Error = e.Error()
		}
		return out
	case "fixture-update-prepare":
		if r.Update == nil {
			return Response{Error: "reviewed update request required"}
		}
		i, e := v.PrepareUpdate(*r.Update)
		out := Response{UpdateIntent: i, Status: v.Status()}
		if e != nil {
			out.Error = e.Error()
		}
		return out
	case "fixture-update-abort":
		i, e := v.AbortUpdate(r.IntentID)
		out := Response{UpdateIntent: i, Status: v.Status()}
		if e != nil {
			out.Error = e.Error()
		}
		return out
	case "status":
	case "import-fixture-seed":
		err = v.ImportFixtureSeed(r.Document, r.Password)
	case "import-ordinary-seed":
		err = v.ImportControlledOrdinarySeed(r.Document, r.Password)
	case "create":
		err = v.Create(r.Password)
	case "unlock":
		if r.UnlockSeconds <= 0 || r.UnlockSeconds > int(MaxUnlock/time.Second) {
			err = errors.New("manual unlock seconds must be between 1 and 3600")
		} else {
			err = v.Unlock(r.Password, time.Duration(r.UnlockSeconds)*time.Second)
		}
	case "lock":
		v.Lock()
	case "backup":
		document, err = v.Backup(r.Password)
	case "verify-backup":
		err = v.VerifyBackup(r.Document, r.Password)
	case "restore":
		err = v.Restore(r.Document, r.Password, r.NewPassword)
	case "export-fixture-seed":
		document, err = v.ExportFixtureSeed()
	case "fixture-approve-proposal":
		if r.Proposal == nil {
			err = errors.New("reviewed proposal required")
			break
		}
		receipts, e := v.ApproveProposal(*r.Proposal)
		result := fixtureResponse(v, nil, nil, e)
		result.Approvals = receipts
		return result
	case "fixture-approvals":
		receipts, e := v.ApprovalReceipts(r.ProposalIDs)
		result := fixtureResponse(v, nil, nil, e)
		result.Approvals = receipts
		return result
	case "fixture-state":
		state, e := v.FixtureState()
		return fixtureResponse(v, state, nil, e)
	case "fixture-policy":
		if r.Policy == nil {
			err = errors.New("bounded policy required")
		} else {
			err = v.SetFixturePolicy(*r.Policy)
		}
	case "fixture-grant":
		if r.Grant == nil {
			err = errors.New("bounded grant required")
		} else {
			err = v.AddFixtureGrant(*r.Grant)
		}
	case "fixture-revoke":
		if r.Account == nil {
			err = errors.New("account reference required")
		} else {
			err = v.RevokeFixtureGrant(*r.Account, r.GrantID)
		}
	case "fixture-recover-authority":
		if r.Account == nil {
			err = errors.New("restored account reference required")
		} else {
			err = v.RecoverFixtureAuthority(*r.Account)
		}
	case "fixture-reserve":
		if r.Intent == nil {
			err = errors.New("typed fixture intent required")
			break
		}
		op, e := v.ReserveFixture(*r.Intent)
		return fixtureResponse(v, nil, op, e)
	case "fixture-execute":
		op, e := v.ExecuteFixture(r.OperationID)
		return fixtureResponse(v, nil, op, e)
	case "fixture-reconcile":
		op, e := v.ReconcileFixture(r.OperationID)
		return fixtureResponse(v, nil, op, e)
	case "fixture-operation":
		op, e := v.FixtureOperation(r.OperationID)
		return fixtureResponse(v, nil, op, e)
	default:
		err = ErrUnavailable
	}
	response := Response{Status: v.Status(), Document: document}
	if err != nil {
		response.Error = err.Error()
	}
	return response
}

// AppActionAllowed is a client convenience. The server independently enforces
// this allowlist after authenticating the actual kernel peer UID.
func AppActionAllowed(action string) bool {
	switch action {
	case "status", "fixture-approvals", "fixture-state", "fixture-reserve", "fixture-execute", "fixture-reconcile", "fixture-operation", "swap-sign", "swap-receipt", "swap-reconcile", "swap-cancel":
		return true
	}
	return false
}
func fixtureResponse(v *Vault, state *FixtureSummary, op *FixtureOperation, err error) Response {
	r := Response{Status: v.Status(), Fixture: state, Operation: op}
	if err != nil {
		r.Error = err.Error()
	}
	return r
}

type wireRequest struct {
	Version   int     `json:"version"`
	SignerID  string  `json:"signerId"`
	Challenge string  `json:"challenge"`
	Request   Request `json:"request"`
}
type greeting struct {
	Version   int    `json:"version"`
	SignerID  string `json:"signerId"`
	Challenge string `json:"challenge"`
}

// Call authenticates the server before sending a password or backup.
func Call(ctx context.Context, c Config, owner bool, request Request) (Response, error) {
	return call(ctx, c, owner, request)
}
func strictWire(data []byte, value any) error {
	// Wire messages are bounded separately from the smaller stored document.
	if len(data) > 64*1024 {
		return errors.New("oversized local request")
	}
	return decodeWire(data, value)
}
