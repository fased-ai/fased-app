// Package maintenance is the fixed local interface to Fased's protected OS
// helper. Requests contain product operations, never commands or file paths.
package maintenance

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strconv"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/update"
)

const StateRoot = update.MaintenanceRoot

// Only this approved predecessor and retained candidate can use the one-time
// Local adoption bridge. This does not grant release or update authority.
const (
	LegacyLocalPredecessorPacket = "bc64b3c658c36e6e62ec667eb8cdb4936d1c74132dd06c2d448faf241c72eae5"
	LegacyLocalRetainedPacket    = "f009a6ad164403688e062e384f23c4ead7d994f1f94d339b8fdf4ee82ffb4725"
	LegacyLocalRetainedSource    = "21c7b865f4ee3ef0a00c1a7e067af6b48f0a6cd5"
	LegacyLocalRetainedBundle    = "93bd81aa86aa0df2838d00463f49bb3abf3749146bb46f48ffc2f1b607c9de8c"
	LegacyLocalRetainedInstaller = "b509071c4a640e8965e271b3b9c75fee9441f9c544d09a028229f778b021c2fa"
)

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
	Candidate  *CandidateRequest              `json:"candidate,omitempty"`
}

// PackageRequest is scoped by the fixed helper arguments, not by a path or
// identity supplied in the request body. Mutating package actions must be
// added only when their complete protected operation is implemented.
type PackageRequest struct {
	Version   int                          `json:"version"`
	Action    string                       `json:"action"`
	Candidate *CandidateRequest            `json:"candidate,omitempty"`
	Wallet    *update.WalletInitialization `json:"wallet,omitempty"`
	Listen    string                       `json:"listen,omitempty"`
}

func DecodePackageRequest(data []byte) (PackageRequest, error) {
	var r PackageRequest
	var fields map[string]json.RawMessage
	if len(data) > 4096 || update.StrictJSON(data, &fields) != nil || fields["version"] == nil || fields["action"] == nil || update.StrictJSON(data, &r) != nil || r.Version != 1 {
		return PackageRequest{}, errors.New("invalid package maintenance request")
	}
	switch r.Action {
	case "status":
		if len(fields) != 2 || r.Candidate != nil {
			return PackageRequest{}, errors.New("status accepts no package input")
		}
	case "initialize":
		if len(fields) != 2 || r.Candidate != nil {
			return PackageRequest{}, errors.New("initialization accepts no caller-selected package")
		}
	case "owner-channel":
		if len(fields) != 2 || r.Candidate != nil {
			return PackageRequest{}, errors.New("owner channel accepts no caller-selected identity or command")
		}
	case "candidate-stage":
		var candidateFields map[string]json.RawMessage
		if len(fields) != 3 || fields["candidate"] == nil || update.StrictJSON(fields["candidate"], &candidateFields) != nil ||
			(len(candidateFields) != 2 && len(candidateFields) != 3) ||
			candidateFields["packetSha256"] == nil || candidateFields["scope"] == nil ||
			(len(candidateFields) == 3 && candidateFields["allowAutomatic"] == nil) ||
			r.Candidate == nil || !canonicalSHA256(r.Candidate.PacketSHA256) || (r.Candidate.Scope != "fresh" && r.Candidate.Scope != "update") ||
			(r.Candidate.Scope == "fresh" && candidateFields["allowAutomatic"] != nil) {
			return PackageRequest{}, errors.New("one exact native candidate required")
		}
	case "provision":
		host, port, err := net.SplitHostPort(r.Listen)
		n, numberErr := strconv.Atoi(port)
		if len(fields) != 3 || fields["listen"] == nil || r.Candidate != nil || err != nil || host != "127.0.0.1" || numberErr != nil || n < 1024 || n > 65535 || strconv.Itoa(n) != port {
			return PackageRequest{}, errors.New("one fixed Local listener is required")
		}
	case "wallet-initialize":
		if len(fields) != 3 || fields["wallet"] == nil || r.Wallet == nil || !r.Wallet.Valid() {
			return PackageRequest{}, errors.New("one exact wallet initialization is required")
		}
	default:
		return PackageRequest{}, errors.New("invalid package maintenance request")
	}
	return r, nil
}

const EnrollmentStreamPrefix = "FASED-ENROLLMENT-STREAM-1\n"
const CandidateStreamPrefix = "FASED-CANDIDATE-STREAM-1\n"
const PackageOwnerStreamPrefix = "FASED-PACKAGE-OWNER-STREAM-1\n"

// CandidateRequest carries an exact public CI artifact identity, never a
// caller-selected root path, command, trust root, or network endpoint.
type CandidateRequest struct {
	PacketSHA256   string `json:"packetSha256"`
	Scope          string `json:"scope"`
	AllowAutomatic bool   `json:"allowAutomatic,omitempty"`
}

// CandidateReceipt is public release metadata written only after the fixed
// helper verifies the exact CI artifact. It grants no signer or trade access.
type CandidateReceipt struct {
	Version           int           `json:"version"`
	Owner             int           `json:"owner"`
	Scope             string        `json:"scope"`
	AllowAutomatic    bool          `json:"allowAutomatic,omitempty"`
	PacketSHA256      string        `json:"packetSha256"`
	InstallerSHA256   string        `json:"installerSha256"`
	EngineSHA256      string        `json:"engineSha256"`
	ProductVersion    string        `json:"productVersion"`
	Sequence          uint64        `json:"sequence,string"`
	ReleaseSHA256     string        `json:"releaseSha256"`
	Target            update.Target `json:"target"`
	BaseSequence      uint64        `json:"baseSequence,string,omitempty"`
	BaseReleaseSHA256 string        `json:"baseReleaseSha256,omitempty"`
	BaseInstallation  string        `json:"baseInstallation,omitempty"`
	BaseManifestSHA   string        `json:"baseManifestSha256,omitempty"`
	ExpiresAt         int64         `json:"expiresAt,omitempty"`
}

// CandidateOperation binds an admitted candidate to the first authorized
// operation. It remains valid for that same recovery after admission expires.
type CandidateOperation struct {
	Version       int    `json:"version"`
	Owner         int    `json:"owner"`
	Scope         string `json:"scope"`
	PacketSHA256  string `json:"packetSha256"`
	OperationID   string `json:"operationId"`
	RequestSHA256 string `json:"requestSha256"`
}

func (o CandidateOperation) Validate() error {
	if o.Version != 1 || o.Owner <= 0 || (o.Scope != "fresh" && o.Scope != "update") || !canonicalSHA256(o.PacketSHA256) || !update.IDOK(o.OperationID) || !canonicalSHA256(o.RequestSHA256) {
		return errors.New("invalid protected candidate operation")
	}
	return nil
}

func (r CandidateReceipt) Validate() error {
	if r.Version != 1 || r.Owner <= 0 || !canonicalSHA256(r.PacketSHA256) || !canonicalSHA256(r.InstallerSHA256) || !canonicalSHA256(r.EngineSHA256) || !canonicalSHA256(r.ReleaseSHA256) || r.ProductVersion == "" || len(r.ProductVersion) > 64 || r.Sequence == 0 || r.Target != (update.Target{OS: "linux", Arch: "amd64"}) {
		return errors.New("invalid protected candidate identity")
	}
	switch r.Scope {
	case "fresh":
		if r.BaseSequence != 0 || r.BaseReleaseSHA256 != "" || r.BaseInstallation != "" || r.BaseManifestSHA != "" || r.ExpiresAt != 0 || r.AllowAutomatic {
			return errors.New("fresh candidate cannot authorize an update")
		}
	case "update":
		if r.BaseSequence == 0 || !canonicalSHA256(r.BaseReleaseSHA256) || !update.IDOK(r.BaseInstallation) || !canonicalSHA256(r.BaseManifestSHA) || r.Sequence <= r.BaseSequence || r.ExpiresAt <= 0 {
			return errors.New("candidate update must bind its predecessor")
		}
	default:
		return errors.New("invalid protected candidate scope")
	}
	return nil
}

// MatchesBase is shared by the visible check and the protected worker. An
// identical released version on another installation is not the same base.
func (r CandidateReceipt) MatchesBase(owner int, sequence uint64, release, installation, manifest string) bool {
	return r.Validate() == nil && r.Owner == owner && r.Scope == "update" && r.BaseSequence == sequence && r.BaseReleaseSHA256 == release && r.BaseInstallation == installation && r.BaseManifestSHA == manifest
}

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
	Action       string                          `json:"action"`
	OperationID  string                          `json:"operationId,omitempty"`
	ProbeID      string                          `json:"probeId,omitempty"`
	Pause        *update.CorePauseCommand        `json:"pause,omitempty"`
	PrivateProof *core.PrivateAccessProofRequest `json:"privateProof,omitempty"`
	RemotePeer   *tailnet.Identity               `json:"remotePeer,omitempty"`
}

func DecodeRequest(data []byte) (Request, error) {
	var r Request
	if len(data) > 32<<10 || update.StrictJSON(data, &r) != nil || r.Version != 1 {
		return r, errors.New("invalid maintenance request")
	}
	if r.Candidate != nil && r.Action != "candidate-stage" {
		return r, errors.New("candidate input is confined to candidate staging")
	}
	switch r.Action {
	case "candidate-stage":
		q := r.Candidate
		if q == nil || !canonicalSHA256(q.PacketSHA256) || (q.Scope != "fresh" && q.Scope != "update") || q.AllowAutomatic || r.Serve != nil || r.Remote != nil || r.Peer != nil || r.Custody != nil || r.Enrollment != nil || r.Image != nil {
			return r, errors.New("one fixed candidate identity and scope required")
		}
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
	case "remote-start", "remote-stop", "remote-status", "remote-ingress-ready", "custody-ingress-ready", "remote-enrollment-validate", "remote-enrollment-status", "remote-enrollment-prepared-status", "remote-enrollment-suspend", "remote-enrollment-restore", "remote-preflight-origin", "tailscale-login":
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
