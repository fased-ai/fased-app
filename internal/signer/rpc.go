package signer

import (
	"context"
	"errors"
	"time"
)

type Config struct {
	Version   int    `json:"version"`
	Mode      string `json:"mode"`
	SignerID  string `json:"signerId"`
	OwnerUID  int    `json:"ownerUid"`
	AppUID    int    `json:"appUid"`
	SignerUID int    `json:"signerUid"`
	StateDir  string `json:"stateDir"`
	SocketDir string `json:"socketDir"`
}

func (c Config) Validate() error {
	if c.Version != 1 || c.Mode != FixtureNetwork || !validID(c.SignerID) || c.OwnerUID <= 0 || c.AppUID <= 0 || c.SignerUID <= 0 ||
		c.OwnerUID == c.AppUID || c.OwnerUID == c.SignerUID || c.AppUID == c.SignerUID || c.StateDir == c.SocketDir {
		return errors.New("fixture requires a versioned identity and three distinct non-root UIDs")
	}
	return nil
}

// Request is a closed set of owner and fixture operations. No real sign/submit.
// Passwords are bytes so callers can clear their own buffers after use.
type Request struct {
	Action        string `json:"action"`
	Password      []byte `json:"password,omitempty"`
	NewPassword   []byte `json:"newPassword,omitempty"`
	Document      []byte `json:"document,omitempty"`
	UnlockSeconds int    `json:"unlockSeconds,omitempty"`
	PublicInput
}
type Response struct {
	Approvals []ApprovalReceipt `json:"approvals,omitempty"`
	Status    Status            `json:"status"`
	Document  []byte            `json:"document,omitempty"`
	Error     string            `json:"error,omitempty"`
	Fixture   *FixtureSummary   `json:"fixture,omitempty"`
	Operation *FixtureOperation `json:"operation,omitempty"`
}

func execute(v *Vault, r Request, owner bool) Response {
	defer wipe(r.Password)
	defer wipe(r.NewPassword)
	var document []byte
	var err error
	if !owner && !AppActionAllowed(r.Action) {
		return Response{Error: "owner administration denied"}
	}
	switch r.Action {
	case "status":
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
	case "status", "fixture-approvals", "fixture-state", "fixture-reserve", "fixture-execute", "fixture-reconcile", "fixture-operation":
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
