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

// Request is a closed set of owner operations. It has no sign/submit method.
// Passwords are bytes so callers can clear their own buffers after use.
type Request struct {
	Action        string `json:"action"`
	Password      []byte `json:"password,omitempty"`
	NewPassword   []byte `json:"newPassword,omitempty"`
	Document      []byte `json:"document,omitempty"`
	UnlockSeconds int    `json:"unlockSeconds,omitempty"`
}
type Response struct {
	Status   Status `json:"status"`
	Document []byte `json:"document,omitempty"`
	Error    string `json:"error,omitempty"`
}

func execute(v *Vault, r Request, owner bool) Response {
	defer wipe(r.Password)
	defer wipe(r.NewPassword)
	var document []byte
	var err error
	if !owner && r.Action != "status" {
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
	case "restore":
		err = v.Restore(r.Document, r.Password, r.NewPassword)
	case "export-fixture-seed":
		document, err = v.ExportFixtureSeed()
	default:
		err = ErrUnavailable
	}
	response := Response{Status: v.Status(), Document: document}
	if err != nil {
		response.Error = err.Error()
	}
	return response
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
