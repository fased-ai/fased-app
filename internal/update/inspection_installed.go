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
