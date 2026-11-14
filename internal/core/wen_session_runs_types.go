package core

import "github.com/fased-ai/fased-app/internal/wenstrategy"

type WENFixtureCapability struct {
	ID        string `json:"id"`
	Enabled   bool   `json:"enabled"`
	Financial bool   `json:"financial"`
}
type WENFixtureRunPrepare struct {
	Schema          string  `json:"schema"`
	Profile         string  `json:"profile"`
	Revision        int64   `json:"revision"`
	LibraryRevision int64   `json:"libraryRevision"`
	StrategyDigest  string  `json:"strategyDigest"`
	Name            string  `json:"name"`
	ScenarioID      string  `json:"scenarioId"`
	ScenarioDigest  string  `json:"scenarioDigest"`
	FixtureWalletID string  `json:"fixtureWalletId"`
	RunID           *string `json:"runId"`
}
type WENFixtureConsent struct {
	ID              string                  `json:"id"`
	ReviewDigest    string                  `json:"reviewDigest"`
	Profile         string                  `json:"profile"`
	SourceDigest    string                  `json:"sourceDigest"`
	ScenarioDigest  string                  `json:"scenarioDigest"`
	InventoryDigest string                  `json:"inventoryDigest"`
	PinnedIDs       []string                `json:"pinnedIds"`
	PoolID          string                  `json:"poolId"`
	PoolRevision    int64                   `json:"poolRevision"`
	BudgetBefore    wenstrategy.BudgetState `json:"budgetBefore"`
	StrategyLimits  wenstrategy.Limits      `json:"strategyLimits"`
	BootID          string                  `json:"bootId"`
	PreparedAt      int64                   `json:"preparedAt"`
	ConsentedAt     int64                   `json:"consentedAt"`
	ReviewExpiresAt int64                   `json:"reviewExpiresAt"`
	Deadline        int64                   `json:"deadline"`
	RevokedAt       *int64                  `json:"revokedAt"`
	WitnessDigest   string                  `json:"witnessDigest"`
}
type WENFixtureRun struct {
	Schema               string                    `json:"schema"`
	EvidenceClass        string                    `json:"evidenceClass"`
	Executable           bool                      `json:"executable"`
	Profile              string                    `json:"profile"`
	Name                 string                    `json:"name"`
	Mode                 string                    `json:"mode"`
	Record               wenstrategy.SessionRecord `json:"record"`
	Source               WENVersion                `json:"source"`
	ScenarioID           string                    `json:"scenarioId"`
	ScenarioDigest       string                    `json:"scenarioDigest"`
	InventoryDigest      string                    `json:"inventoryDigest"`
	PinnedIDs            []string                  `json:"pinnedIds"`
	Cursor               int                       `json:"cursor"`
	ClockAt              string                    `json:"clockAt"`
	LastWallAt           int64                     `json:"lastWallAt"`
	BootID               string                    `json:"bootId"`
	FirstStartedAt       int64                     `json:"firstStartedAt"`
	Deadline             int64                     `json:"deadline"`
	AdmissionOrder       int64                     `json:"admissionOrder"`
	ConsentHistory       []WENFixtureConsent       `json:"consentHistory"`
	DailyAccepted        map[string]int            `json:"dailyAccepted"`
	Operations           []WENSessionOperation     `json:"operations"`
	Decisions            []WENSessionDecision      `json:"decisions"`
	Receipts             []WENSessionReceipt       `json:"receipts"`
	Reason               string                    `json:"reason"`
	CompletionReason     string                    `json:"completionReason"`
	StopRequested        bool                      `json:"stopRequested"`
	AcknowledgedDecision int                       `json:"acknowledgedDecision"`
	CreateHash           string                    `json:"createHash"`
}
type WENFixtureRunReview struct {
	Schema          string               `json:"schema"`
	Input           WENFixtureRunPrepare `json:"input"`
	Source          WENVersion           `json:"source"`
	Scenario        WENSessionScenario   `json:"scenario"`
	InventoryDigest string               `json:"inventoryDigest"`
	PinnedIDs       []string             `json:"pinnedIds"`
	Pool            WENSessionPool       `json:"pool"`
	Run             *WENFixtureRun       `json:"run"`
	Capability      WENFixtureCapability `json:"capability"`
	InstanceID      string               `json:"instanceId"`
	BootID          string               `json:"bootId"`
	PreparedAt      int64                `json:"preparedAt"`
	ExpiresAt       int64                `json:"expiresAt"`
	RunDeadline     *int64               `json:"runDeadline"`
	MaxWallSeconds  int64                `json:"maxWallSeconds"`
	ReservedBytes   int                  `json:"reservedBytes"`
	Executable      bool                 `json:"executable"`
	Witness         string               `json:"witness"`
}
type WENFixtureRunView struct {
	Schema          string               `json:"schema"`
	Profile         string               `json:"profile"`
	Revision        int64                `json:"revision"`
	LibraryRevision int64                `json:"libraryRevision"`
	Capability      WENFixtureCapability `json:"capability"`
	EvidenceClass   string               `json:"evidenceClass"`
	Executable      bool                 `json:"executable"`
	Runs            []WENFixtureRun      `json:"runs"`
	Pools           []WENSessionPool     `json:"pools"`
	Receipt         *WENSessionReceipt   `json:"receipt"`
}
type WENFixtureRunControl struct {
	Schema       string  `json:"schema"`
	Profile      string  `json:"profile"`
	Revision     int64   `json:"revision"`
	PoolRevision int64   `json:"poolRevision"`
	RunID        string  `json:"runId"`
	Action       string  `json:"action"`
	DecisionID   *string `json:"decisionId"`
}
type WENFixtureRunReconcile struct {
	Schema       string `json:"schema"`
	Profile      string `json:"profile"`
	Revision     int64  `json:"revision"`
	PoolRevision int64  `json:"poolRevision"`
	RunID        string `json:"runId"`
	EventID      string `json:"eventId"`
}
