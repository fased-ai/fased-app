package update

// Chunk size leaves room for base64 and all bindings inside the owner RPC cap.
const StageChunk = 24 << 10

type StageRequest struct {
	IntentID       string        `json:"intentId"`
	ManifestSHA256 string        `json:"manifestSha256"`
	Sequence       uint64        `json:"sequence,string"`
	File           string        `json:"file"`
	Offset         int64         `json:"offset"`
	Data           []byte        `json:"data,omitempty"`
	Core           *CoreSnapshot `json:"core,omitempty"`
}
type StageFile struct {
	Artifact Artifact `json:"artifact"`
	Bytes    int64    `json:"bytes"`
}
type StageStatus struct {
	IntentID           string      `json:"intentId"`
	Sequence           uint64      `json:"sequence,string"`
	ManifestSHA256     string      `json:"manifestSha256"`
	State              string      `json:"state"`
	Files              []StageFile `json:"files"`
	Verified           bool        `json:"verified"`
	InstallAllowed     bool        `json:"installAllowed"`
	ResumeAllowed      bool        `json:"resumeAllowed"`
	FinancialExecution bool        `json:"financialExecution"`
}

func Stageable(i Intent) bool {
	if len(i.Artifacts) != 2 || !lifecycleOK(i.Lifecycle) {
		return false
	}
	for _, a := range i.Artifacts {
		if !tokenOK(a.File) || a.Size <= 0 || a.Size > MaxArtifact || !hashOK(a.SHA256) {
			return false
		}
	}
	return i.Artifacts[0].File != i.Artifacts[1].File
}

// Immutable receipt identity; excludes mutable stage/abort state.
func StageDigest(i Intent) string {
	i.State = "prepared"
	i.StageSHA256 = ""
	return SnapshotDigest("artifact-stage:v1", i)
}
