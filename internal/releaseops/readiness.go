package releaseops

// Readiness is a static capability report, not a receipt/approval importer.
// There is intentionally no API that fills production bindings from fixtures.
type Readiness struct {
	Version             int      `json:"version"`
	FixtureOnly         bool     `json:"fixtureOnly"`
	ProductionAdmission bool     `json:"productionAdmission"`
	PublicActivation    bool     `json:"publicActivation"`
	RealWallets         bool     `json:"realWallets"`
	AutomaticUpdates    bool     `json:"automaticUpdates"`
	Result              string   `json:"result"`
	Blockers            []string `json:"blockers"`
}

func Status() Readiness {
	return Readiness{Version: 1, FixtureOnly: true, Result: "BLOCKED", Blockers: []string{
		"independently authenticated production installer/root/checkpoint",
		"named independent custodians, backup restore and monitoring rota",
		"approved immutable repository, metadata branch and exact API/CDN policy",
		"authorized publisher identities, protected workflow and sequence ledger",
		"production attestation mechanism and pinned builder verification material",
		"production trusted time/state and independent compatible owner/signer admission",
		"named incident contacts and independently authenticated rescue acceptance",
		"native signing, real private VPS, authoritative recovery and retail acceptance",
	}}
}
