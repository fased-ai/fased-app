// Package maintenance is the fixed local interface to Fased's protected OS
// helper. Requests contain product operations, never commands or file paths.
package maintenance

import (
	"encoding/json"
	"errors"

	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/update"
)

const StateRoot = update.MaintenanceRoot

type Authority = update.MaintenanceAuthority

func OwnerRoot(owner int) string   { return update.MaintenanceOwnerRoot(owner) }
func ImagePath(hash string) string { return update.MaintenanceImagePath(hash) }

type ServeRequest struct {
	Configuration json.RawMessage `json:"configuration"`
	ETag          string          `json:"etag"`
}

type Request struct {
	Version int                   `json:"version"`
	Action  string                `json:"action"`
	Serve   *ServeRequest         `json:"serve,omitempty"`
	Remote  *RemoteInstallRequest `json:"remote,omitempty"`
	Peer    *tailnet.Identity     `json:"peer,omitempty"`
	Custody *CustodyUpdateRequest `json:"custody,omitempty"`
}

type CustodyUpdateRequest struct {
	OperationID string `json:"operationId"`
	Sequence uint64 `json:"sequence,string"`
	ReleaseSHA256 string `json:"releaseSha256"`
	Bootstrap bool `json:"bootstrap,omitempty"`
}

type RemoteInstallRequest struct {
	Listen        string `json:"listen"`
	Origin        string `json:"origin"`
	Sequence      uint64 `json:"sequence,string"`
	ReleaseSHA256 string `json:"releaseSha256"`
	Start         bool   `json:"start"`
}

type Response struct {
	Version  int             `json:"version"`
	Action   string          `json:"action"`
	Complete bool            `json:"complete"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type AppMaintenanceRequest struct {
	Action string `json:"action"`
	OperationID string `json:"operationId,omitempty"`
	ProbeID string `json:"probeId,omitempty"`
	Pause *update.CorePauseCommand `json:"pause,omitempty"`
}

func DecodeRequest(data []byte) (Request, error) {
	var r Request
	if len(data) > 32<<10 || update.StrictJSON(data, &r) != nil || r.Version != 1 {
		return r, errors.New("invalid maintenance request")
	}
	switch r.Action {
	case "custody-update":
		if r.Serve!=nil||r.Remote!=nil||r.Peer!=nil||r.Custody==nil||!update.IDOK(r.Custody.OperationID)||r.Custody.Sequence==0||len(r.Custody.ReleaseSHA256)!=64{return r,errors.New("one exact custody update required")}
	case "serve":
		if r.Custody!=nil || r.Peer != nil || r.Remote != nil || r.Serve == nil || len(r.Serve.Configuration) > 16<<10 || len(r.Serve.ETag) != 64 {
			return r, errors.New("invalid private-access maintenance request")
		}
	case "remote-install":
		if r.Custody!=nil || r.Peer != nil || r.Serve != nil || r.Remote == nil || r.Remote.Sequence == 0 || len(r.Remote.ReleaseSHA256) != 64 || len(r.Remote.Listen) > 64 || len(r.Remote.Origin) > 256 {
			return r, errors.New("invalid Remote installation request")
		}
	case "remote-start", "remote-stop", "remote-status":
		if r.Custody!=nil || r.Peer != nil || r.Serve != nil || r.Remote != nil {
			return r, errors.New("unexpected Remote lifecycle input")
		}
	case "remote-setup-link":
		if r.Custody!=nil || r.Serve != nil || r.Remote != nil || r.Peer != nil && (r.Peer.UserID <= 0 || r.Peer.NodeID == "" || len(r.Peer.NodeID) > 128 || r.Peer.Login == "" || len(r.Peer.Login) > 256 || len(r.Peer.Name) > 256) {
			return r, errors.New("invalid Remote setup owner")
		}
	default:
		return r, errors.New("unsupported maintenance operation")
	}
	return r, nil
}
