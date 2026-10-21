package update

// Exported fixture schemas allow the trusted OS publisher to seal inputs. They
// are data, never installed authority: only the Linux root-verified factory opens
// an installed role. Existing in-process constructors remain private.
type FixtureInspectionCatalog = inspectionCatalog
type FixtureInspectionRoleInput = inspectionRoleInput
type FixtureInspectionClock = inspectionClock
type FixtureInspectionReader = inspectionReader

func FixtureInspectionContract() FixtureInspectionReader { return inspectionReaderV1() }

const FixtureInspectionProfile = inspectionProfile

type FixtureInspectionFence struct {
	Version    int    `json:"version"`
	Profile    string `json:"profile"`
	Provision  string `json:"provision"`
	CatalogSHA string `json:"catalogSha256"`
	BridgeSHA  string `json:"bridgeSha256"`
}
type InstalledInspectionStatus struct {
	Head          string             `json:"head"`
	Held          bool               `json:"held"`
	CatalogSHA    string             `json:"catalogSha256"`
	Sources       [2]string          `json:"sources"`
	Core          CoreSnapshot       `json:"core"`
	Signer        SignerSnapshot     `json:"signer"`
	Unit          FixtureReleaseUnit `json:"unit"`
	ProvenanceSHA string             `json:"provenanceSha256"`

	FixtureOnly        bool                   `json:"fixtureOnly"`
	Historical         bool                   `json:"historical"`
	Profile            string                 `json:"profile"`
	Proposal           string                 `json:"proposal"`
	Review             string                 `json:"review"`
	Exchange           string                 `json:"exchange"`
	Admission          ReleaseAdmissionReport `json:"admission"`
	InstallAllowed     bool                   `json:"installAllowed"`
	ResumeAllowed      bool                   `json:"resumeAllowed"`
	WalletSigner       bool                   `json:"walletSigner"`
	FinancialExecution bool                   `json:"financialExecution"`
	UpdaterEnabled     bool                   `json:"updaterEnabled"`
}

// FixtureInspectionInputName is the closed publisher grammar, not a path grant.
func FixtureInspectionInputName(n string) bool {
	if n == "catalog.json" {
		return true
	}
	for _, role := range []string{"owner", "signer"} {
		for _, suffix := range []string{".json", "-seed.json", "-policy.json", "-clock.json"} {
			if n == role+suffix {
				return true
			}
		}
	}
	const prefix = "bundle/"
	if len(n) <= len(prefix) || n[:len(prefix)] != prefix {
		return false
	}
	name := n[len(prefix):]
	return name == "index.json" || inspectionNameLimit(name) > 0
}

// InspectionFixtureHeld compares an unchanged financial reader pair. Unlike the
// older 3->4 bridge it must retain every completion feature on both sides.
func InspectionFixtureHeld(before, now SignerSnapshot, aborted bool) bool {
	before.BootID, before.ProbeID, now.BootID, now.ProbeID = "", "", "", ""
	if aborted {
		before.ActiveIntentID = ""
		before.States = append([]State(nil), before.States...)
		now.States = append([]State(nil), now.States...)
		for i := range before.States {
			if before.States[i].Name == "operations" {
				before.States[i].Anchor = ""
			}
		}
		for i := range now.States {
			if now.States[i].Name == "operations" {
				now.States[i].Anchor = ""
			}
		}
	}
	return SnapshotDigest("inspection-fixture-held:v1", before) == SnapshotDigest("inspection-fixture-held:v1", now)
}
