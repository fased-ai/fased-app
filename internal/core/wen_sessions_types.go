package core

import "github.com/fased-ai/fased-app/internal/wenstrategy"

type WENSessionPrepare struct {
	Schema          string `json:"schema"`
	Profile         string `json:"profile"`
	Revision        int64  `json:"revision"`
	LibraryRevision int64  `json:"libraryRevision"`
	StrategyDigest  string `json:"strategyDigest"`
	Name            string `json:"name"`
	Mode            string `json:"mode"`
	ScenarioID      string `json:"scenarioId"`
	ScenarioDigest  string `json:"scenarioDigest"`
	FixtureWalletID string `json:"fixtureWalletId"`
}
type WENSessionPool struct {
	ID       string                  `json:"id"`
	Revision int64                   `json:"revision"`
	Budget   wenstrategy.BudgetState `json:"budget"`
	Daily    map[string]string       `json:"daily"`
}
type WENSessionReceipt struct {
	ID           string `json:"id"`
	CommandHash  string `json:"commandHash"`
	Action       string `json:"action"`
	DecisionID   string `json:"decisionId"`
	OperationID  string `json:"operationId"`
	PoolRevision int64  `json:"poolRevision"`
}
type WENSessionDecision struct {
	Record           wenstrategy.DecisionRecord `json:"record"`
	Phase            string                     `json:"phase"`
	EvaluationReason string                     `json:"evaluationReason"`
	InputDigest      string                     `json:"inputDigest"`
}
type WENSessionOffer struct {
	ID          string                  `json:"id"`
	DecisionID  string                  `json:"decisionId"`
	RoundID     string                  `json:"roundId"`
	At          string                  `json:"at"`
	Cutoff      string                  `json:"cutoff"`
	OfferedAt   int64                   `json:"offeredAt"`
	ExpiresAt   int64                   `json:"expiresAt"`
	Reservation wenstrategy.Reservation `json:"reservation"`
}
type WENSessionOperation struct {
	ID                 string                  `json:"id"`
	RoundID            string                  `json:"roundId"`
	Day                string                  `json:"day"`
	Status             string                  `json:"status"`
	Reservation        wenstrategy.Reservation `json:"reservation"`
	ConfirmedDeduction string                  `json:"confirmedDeduction"`
	ConfirmedFee       string                  `json:"confirmedFee"`
}
type WENSession struct {
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
	NextSlot             int                       `json:"nextSlot"`
	ClockAt              string                    `json:"clockAt"`
	LastWallAt           int64                     `json:"lastWallAt"`
	BootID               string                    `json:"bootId"`
	Schedule             wenstrategy.ScheduleState `json:"schedule"`
	DailyAccepted        map[string]int            `json:"dailyAccepted"`
	Pending              *WENSessionOffer          `json:"pending"`
	Operations           []WENSessionOperation     `json:"operations"`
	Decisions            []WENSessionDecision      `json:"decisions"`
	Receipts             []WENSessionReceipt       `json:"receipts"`
	Reason               string                    `json:"reason"`
	CompletionReason     string                    `json:"completionReason"`
	StopRequested        bool                      `json:"stopRequested"`
	AcknowledgedDecision int                       `json:"acknowledgedDecision"`
	CreateHash           string                    `json:"createHash"`
}
type wenSessionWorkspace struct {
	Sessions []WENSession     `json:"sessions"`
	Pools    []WENSessionPool `json:"pools"`
}
type WENSessionView struct {
	Schema          string               `json:"schema"`
	Profile         string               `json:"profile"`
	Revision        int64                `json:"revision"`
	LibraryRevision int64                `json:"libraryRevision"`
	EvidenceClass   string               `json:"evidenceClass"`
	Executable      bool                 `json:"executable"`
	Sessions        []WENSession         `json:"sessions"`
	Pools           []WENSessionPool     `json:"pools"`
	PoolOptions     []string             `json:"poolOptions"`
	Scenarios       []WENSessionScenario `json:"scenarios"`
	NextEvents      map[string]string    `json:"nextEvents"`
	Receipt         *WENSessionReceipt   `json:"receipt,omitempty"`
}
type WENSessionReview struct {
	Schema          string             `json:"schema"`
	Input           WENSessionPrepare  `json:"input"`
	Source          WENVersion         `json:"source"`
	Scenario        WENSessionScenario `json:"scenario"`
	InventoryDigest string             `json:"inventoryDigest"`
	PinnedIDs       []string           `json:"pinnedIds"`
	Pool            WENSessionPool     `json:"pool"`
	PreparedAt      int64              `json:"preparedAt"`
	ExpiresAt       int64              `json:"expiresAt"`
	ReservedBytes   int                `json:"reservedBytes"`
	Executable      bool               `json:"executable"`
	Witness         string             `json:"witness"`
}
type WENSessionStep struct {
	Schema       string `json:"schema"`
	Profile      string `json:"profile"`
	Revision     int64  `json:"revision"`
	PoolRevision int64  `json:"poolRevision"`
	SessionID    string `json:"sessionId"`
	EventID      string `json:"eventId"`
}
type WENSessionControl struct {
	Schema       string  `json:"schema"`
	Profile      string  `json:"profile"`
	Revision     int64   `json:"revision"`
	PoolRevision int64   `json:"poolRevision"`
	SessionID    string  `json:"sessionId"`
	Action       string  `json:"action"`
	DecisionID   *string `json:"decisionId"`
	ReviewID     *string `json:"reviewId"`
	Witness      *string `json:"witness"`
}
type WENSessionActionInput struct {
	Schema       string `json:"schema"`
	Profile      string `json:"profile"`
	Revision     int64  `json:"revision"`
	PoolRevision int64  `json:"poolRevision"`
	SessionID    string `json:"sessionId"`
	DecisionID   string `json:"decisionId"`
}
type WENSessionActionReview struct {
	Schema     string                  `json:"schema"`
	Input      WENSessionActionInput   `json:"input"`
	ReviewID   string                  `json:"reviewId"`
	Offer      WENSessionOffer         `json:"offer"`
	Decision   WENSessionDecision      `json:"decision"`
	Source     WENVersion              `json:"source"`
	Scenario   WENSessionScenario      `json:"scenario"`
	Metrics    []wenstrategy.Metric    `json:"metrics"`
	Budget     wenstrategy.BudgetState `json:"budget"`
	Executable bool                    `json:"executable"`
	Witness    string                  `json:"witness"`
}
