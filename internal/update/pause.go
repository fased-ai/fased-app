package update

import "errors"

// These are app-owned metadata fences, not financial authorization. Only the
// immutable owner conductor obtains them from actual private core IPC.
type CorePauseRequest struct {
	Version        int          `json:"version"`
	IntentID       string       `json:"intentId"`
	Sequence       uint64       `json:"sequence,string"`
	ManifestSHA256 string       `json:"manifestSha256"`
	Expected       CoreSnapshot `json:"expected"`
}
type CorePauseReceipt struct {
	Version int               `json:"version"`
	State   string            `json:"state"`
	PauseID string            `json:"pauseId"`
	Request *CorePauseRequest `json:"request,omitempty"`
	Core    CoreSnapshot      `json:"core"`
}
type CorePauseCommand struct {
	Action    string            `json:"action"`
	ProbeID   string            `json:"probeId"`
	Request   *CorePauseRequest `json:"request,omitempty"`
	Completed *Installation     `json:"completed,omitempty"`
	Aborted   *Intent           `json:"aborted,omitempty"`
}
type PreflightRequest struct {
	IntentID       string           `json:"intentId"`
	Sequence       uint64           `json:"sequence,string"`
	ManifestSHA256 string           `json:"manifestSha256"`
	ProbeID        string           `json:"probeId"`
	Pause          CorePauseReceipt `json:"pause"`
	ExpectedSigner SignerSnapshot   `json:"expectedSigner"`
}

type Preflight struct {
	Version            int              `json:"version"`
	IntentID           string           `json:"intentId"`
	Sequence           uint64           `json:"sequence,string"`
	ManifestSHA256     string           `json:"manifestSha256"`
	Pause              CorePauseReceipt `json:"pause"`
	Signer             SignerSnapshot   `json:"signer"`
	Files              []StageFile      `json:"files"`
	BindingSHA256      string           `json:"bindingSha256"`
	Verified           bool             `json:"verified"`
	InstallAllowed     bool             `json:"installAllowed"`
	ResumeAllowed      bool             `json:"resumeAllowed"`
	FinancialExecution bool             `json:"financialExecution"`
}

func PauseFor(i Intent) CorePauseRequest {
	return CorePauseRequest{Version: 1, IntentID: i.ID, Sequence: i.Sequence, ManifestSHA256: i.ManifestSHA256, Expected: i.Core}
}
func ValidPauseRequest(r CorePauseRequest) bool {
	return r.Version == 1 && IDOK(r.IntentID) && r.Sequence > 0 && hashOK(r.ManifestSHA256) && r.Expected.Version == 1 && IDOK(r.Expected.ProbeID) && IDOK(r.Expected.BootID) && tokenOK(r.Expected.InstanceID) && r.Expected.State.Name == "core-db" && r.Expected.State.Version > 0 && stateFeaturesOK(r.Expected.State.Features) && hashOK(r.Expected.State.Anchor)
}
func SameCoreMetadata(a, b CoreSnapshot) bool {
	a.BootID = ""
	b.BootID = ""
	a.ProbeID = ""
	b.ProbeID = ""
	return SnapshotDigest("core", a) == SnapshotDigest("core", b)
}
func ValidatePaused(i Intent, p CorePauseReceipt, probe string) error {
	if !IDOK(probe) || p.Version != 1 || p.State != "paused" || !IDOK(p.PauseID) || p.Request == nil || !ValidPauseRequest(*p.Request) || SnapshotDigest("pause-request", *p.Request) != SnapshotDigest("pause-request", PauseFor(i)) || !IDOK(p.Core.BootID) || p.Core.ProbeID != probe || !SameCoreMetadata(i.Core, p.Core) {
		return errors.New("stale or unbound core pause receipt")
	}
	return nil
}
