// Package maintenance is the fixed local interface to Fased's protected OS
// helper. Requests contain product operations, never commands or file paths.
package maintenance

import (
	"encoding/base64"
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
	Version    int                            `json:"version"`
	Action     string                         `json:"action"`
	Serve      *ServeRequest                  `json:"serve,omitempty"`
	Remote     *RemoteInstallRequest          `json:"remote,omitempty"`
	Peer       *tailnet.Identity              `json:"peer,omitempty"`
	Custody    *CustodyUpdateRequest          `json:"custody,omitempty"`
	Enrollment *CustodyEnrollmentRequest      `json:"enrollment,omitempty"`
	Image      *CustodyEnrollmentImageRequest `json:"image,omitempty"`
}

const EnrollmentStreamPrefix = "FASED-ENROLLMENT-STREAM-1\n"

type CustodyEnrollmentImageRequest struct {
	OperationID string `json:"operationId"`
	Action      string `json:"action"`
	Slot        string `json:"slot"`
	InstanceID  string `json:"instanceId,omitempty"`
	Digest      string `json:"digest,omitempty"`
}

// CustodyEnrollmentRequest contains product choices and expected identities;
// the helper obtains executable bytes and owner/OS identities independently.
type CustodyEnrollmentRequest struct {
	OperationID       string `json:"operationId"`
	Step              string `json:"step,omitempty"`
	Network           string `json:"network"`
	Listen            string `json:"listen"`
	PublicOrigin      string `json:"publicOrigin,omitempty"`
	NativeOwnerAccess bool   `json:"nativeOwnerAccess,omitempty"`
	CoreSHA256        string `json:"coreSha256"`
	SignerSHA256      string `json:"signerSha256"`
	Sequence          uint64 `json:"sequence,string,omitempty"`
	ReleaseSHA256     string `json:"releaseSha256,omitempty"`
}

type CustodyUpdateRequest struct {
	OperationID   string `json:"operationId"`
	Sequence      uint64 `json:"sequence,string"`
	ReleaseSHA256 string `json:"releaseSha256"`
	Bootstrap     bool   `json:"bootstrap,omitempty"`
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
	Action      string                   `json:"action"`
	OperationID string                   `json:"operationId,omitempty"`
	ProbeID     string                   `json:"probeId,omitempty"`
	Pause       *update.CorePauseCommand `json:"pause,omitempty"`
}

func DecodeRequest(data []byte) (Request, error) {
	var r Request
	if len(data) > 32<<10 || update.StrictJSON(data, &r) != nil || r.Version != 1 {
		return r, errors.New("invalid maintenance request")
	}
	switch r.Action {
	case "custody-enrollment-activation":
		q := r.Enrollment
		if r.Serve != nil || r.Remote != nil || r.Peer != nil || r.Custody != nil || r.Image != nil || q == nil || !update.IDOK(q.OperationID) ||
			(q.Step != "activate" && q.Step != "restart") || q.Network != "" || q.Listen != "" || q.PublicOrigin != "" || q.NativeOwnerAccess || q.CoreSHA256 != "" || q.SignerSHA256 != "" || q.Sequence != 0 || q.ReleaseSHA256 != "" {
			return r, errors.New("one fixed enrollment activation required")
		}
	case "custody-enrollment-cutover":
		q := r.Enrollment
		if r.Serve != nil || r.Remote != nil || r.Peer != nil || r.Custody != nil || r.Image != nil || q == nil || !update.IDOK(q.OperationID) ||
			(q.Step != "reserve" && q.Step != "fence" && q.Step != "check" && q.Step != "publish" && q.Step != "finish") ||
			q.Network != "" || q.Listen != "" || q.PublicOrigin != "" || q.NativeOwnerAccess || q.CoreSHA256 != "" || q.SignerSHA256 != "" || q.Sequence != 0 || q.ReleaseSHA256 != "" {
			return r, errors.New("one fixed enrollment cutover step required")
		}
	case "custody-enrollment-image":
		q := r.Image
		if r.Serve != nil || r.Remote != nil || r.Peer != nil || r.Custody != nil || r.Enrollment != nil || q == nil || !update.IDOK(q.OperationID) {
			return r, errors.New("one fixed enrollment image operation required")
		}
		switch q.Action {
		case "export":
			if q.Slot != "source" || !enrollmentInstance(q.InstanceID) || q.Digest != "" {
				return r, errors.New("invalid enrollment source")
			}
		case "import", "verify":
			if q.Action == "import" && q.Slot != "stage" || q.Action == "verify" && q.Slot != "stage" && q.Slot != "app" || !enrollmentInstance(q.InstanceID) || !canonicalSHA256(q.Digest) {
				return r, errors.New("invalid enrollment image binding")
			}
		case "empty":
			if (q.Slot != "stage" && q.Slot != "app") || q.InstanceID != "" || q.Digest != "" {
				return r, errors.New("invalid enrollment empty check")
			}
		default:
			return r, errors.New("unsupported enrollment image operation")
		}
	case "custody-enrollment-empty", "custody-enrollment-stage":
		q := r.Enrollment
		if r.Serve != nil || r.Remote != nil || r.Peer != nil || r.Custody != nil || r.Image != nil || q == nil || !update.IDOK(q.OperationID) ||
			q.Step != "" || q.Network != "" || q.Listen != "" || q.PublicOrigin != "" || q.NativeOwnerAccess || q.CoreSHA256 != "" || q.SignerSHA256 != "" || q.Sequence != 0 || q.ReleaseSHA256 != "" {
			return r, errors.New("one fixed custody enrollment required")
		}
	case "custody-enrollment-prepare":
		q := r.Enrollment
		if r.Serve != nil || r.Remote != nil || r.Peer != nil || r.Custody != nil || r.Image != nil || q == nil || !update.IDOK(q.OperationID) ||
			q.Step != "" || (q.Network != "devnet" && q.Network != "mainnet-beta") || len(q.Listen) > 64 || len(q.PublicOrigin) > 256 ||
			!canonicalSHA256(q.CoreSHA256) || !canonicalSHA256(q.SignerSHA256) || (q.Sequence == 0) != (q.ReleaseSHA256 == "") || q.ReleaseSHA256 != "" && !canonicalSHA256(q.ReleaseSHA256) {
			return r, errors.New("fixed custody enrollment choices required")
		}
	case "custody-initialize":
		if r.Serve != nil || r.Remote != nil || r.Peer != nil || r.Custody != nil || r.Enrollment != nil || r.Image != nil {
			return r, errors.New("custody initialization takes no caller-supplied inputs")
		}
	case "custody-update":
		if r.Serve != nil || r.Remote != nil || r.Peer != nil || r.Enrollment != nil || r.Image != nil || r.Custody == nil || !update.IDOK(r.Custody.OperationID) || r.Custody.Sequence == 0 || len(r.Custody.ReleaseSHA256) != 64 {
			return r, errors.New("one exact custody update required")
		}
	case "serve":
		if r.Custody != nil || r.Enrollment != nil || r.Image != nil || r.Peer != nil || r.Remote != nil || r.Serve == nil || len(r.Serve.Configuration) > 16<<10 || len(r.Serve.ETag) != 64 {
			return r, errors.New("invalid private-access maintenance request")
		}
	case "remote-install":
		if r.Custody != nil || r.Enrollment != nil || r.Image != nil || r.Peer != nil || r.Serve != nil || r.Remote == nil || r.Remote.Sequence == 0 || len(r.Remote.ReleaseSHA256) != 64 || len(r.Remote.Listen) > 64 || len(r.Remote.Origin) > 256 {
			return r, errors.New("invalid Remote installation request")
		}
	case "remote-start", "remote-stop", "remote-status", "remote-enrollment-validate", "remote-enrollment-status", "remote-enrollment-prepared-status", "remote-enrollment-suspend", "remote-enrollment-restore", "tailscale-login":
		if r.Custody != nil || r.Enrollment != nil || r.Image != nil || r.Peer != nil || r.Serve != nil || r.Remote != nil {
			return r, errors.New("unexpected Remote lifecycle input")
		}
	case "remote-setup-link":
		if r.Custody != nil || r.Enrollment != nil || r.Image != nil || r.Serve != nil || r.Remote != nil || r.Peer != nil && (r.Peer.UserID <= 0 || r.Peer.NodeID == "" || len(r.Peer.NodeID) > 128 || r.Peer.Login == "" || len(r.Peer.Login) > 256 || len(r.Peer.Name) > 256) {
			return r, errors.New("invalid Remote setup owner")
		}
	default:
		return r, errors.New("unsupported maintenance operation")
	}
	return r, nil
}

func enrollmentInstance(s string) bool {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}

func canonicalSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}
