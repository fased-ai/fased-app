package wenstrategy

import "encoding/json"

// Record types mirror the shared TypeScript transport contract. They are inert
// library shapes, not API/storage admission or a replacement receipt validator.
type EvidenceClass string
type Finality string
type NativeAmount struct {
	Asset    string  `json:"asset"`
	Mint     *string `json:"mint"`
	Units    string  `json:"units"`
	Decimals int     `json:"decimals"`
}
type Availability[T any] struct {
	Available bool   `json:"available"`
	Value     *T     `json:"value"`
	Reason    string `json:"reason,omitempty"`
}
type Provenance struct {
	RecordID      string `json:"recordId"`
	MarketBinding Market `json:"marketBinding"`
	Source        string `json:"source"`
	EventAt       string `json:"eventAt"`
	ReceivedAt    string `json:"receivedAt"`
	Sequence      string `json:"sequence"`
	InputDigest   string `json:"inputDigest"`
	MethodVersion string `json:"methodVersion"`
	Quality       string `json:"quality"`
}
type Observation struct {
	Provenance
	Units          string               `json:"units"`
	Value          Availability[string] `json:"value"`
	ObservationIDs []string             `json:"observationIds"`
}
type ResearchSnapshot struct {
	SnapshotID         string        `json:"snapshotId"`
	MarketBinding      Market        `json:"marketBinding"`
	Cutoff             string        `json:"cutoff"`
	GeneratedAt        string        `json:"generatedAt"`
	EvidenceClass      EvidenceClass `json:"evidenceClass"`
	Metrics            []Metric      `json:"metrics"`
	ObservationIDs     []string      `json:"observationIds"`
	InputDigest        string        `json:"inputDigest"`
	DisclosedRecordIDs []string      `json:"disclosedRecordIds"`
}
type Competition struct {
	Kind      string   `json:"kind"`
	SourceIDs []string `json:"sourceIds"`
}
type PreviewAssumptions struct {
	UserFeeLamports  string  `json:"userFeeLamports"`
	ConversionSource *string `json:"conversionSource"`
	ConversionAt     *string `json:"conversionAt"`
}
type PreviewEnvelope struct {
	Schema                    string                     `json:"schema"`
	StrategyDigest            string                     `json:"strategyDigest"`
	MarketBinding             Market                     `json:"marketBinding"`
	RoundID                   string                     `json:"roundId"`
	AmountsLamports           [5]string                  `json:"amountsLamports"`
	At                        string                     `json:"at"`
	CanonicalRevision         string                     `json:"canonicalRevision"`
	InputDigest               string                     `json:"inputDigest"`
	OutcomeRange              int                        `json:"outcomeRange"`
	CreditUnits               []string                   `json:"creditUnits"`
	Competition               Competition                `json:"competition"`
	Assumptions               PreviewAssumptions         `json:"assumptions"`
	PrincipalReturnedLamports string                     `json:"principalReturnedLamports"`
	UserCostsLamports         string                     `json:"userCostsLamports"`
	FreshMinerBudgetLamports  string                     `json:"freshMinerBudgetLamports"`
	AcquiredBTC               Availability[NativeAmount] `json:"acquiredBTC"`
	CarryEligibility          Availability[bool]         `json:"carryEligibility"`
	SATAward                  NativeAmount               `json:"satAward"`
	CanonicalReceiptHexes     []string                   `json:"canonicalReceiptHexes"`
	Executable                bool                       `json:"executable"`
}
type StrategyProposal struct {
	ProposalID      string   `json:"proposalId"`
	ProfileID       string   `json:"profileId"`
	ConversationID  string   `json:"conversationId"`
	SourceRecordIDs []string `json:"sourceRecordIds"`
	SnapshotIDs     []string `json:"snapshotIds"`
	StrategyDigest  string   `json:"strategyDigest"`
	Validation      string   `json:"validation"`
	ReviewState     string   `json:"reviewState"`
	MandateID       *string  `json:"mandateId"`
}
type SessionRecord struct {
	SessionID      string      `json:"sessionId"`
	StrategyDigest string      `json:"strategyDigest"`
	Runner         string      `json:"runner"`
	InstanceID     string      `json:"instanceId"`
	MandateID      *string     `json:"mandateId"`
	WalletID       string      `json:"walletId"`
	Status         string      `json:"status"`
	OperationIDs   []string    `json:"operationIds"`
	Budget         BudgetState `json:"budget"`
}
type DecisionRecord struct {
	DecisionID      string      `json:"decisionId"`
	SessionID       string      `json:"sessionId"`
	StrategyDigest  string      `json:"strategyDigest"`
	MandateID       *string     `json:"mandateId"`
	RoundID         string      `json:"roundId"`
	At              string      `json:"at"`
	Cutoff          string      `json:"cutoff"`
	InputIDs        []string    `json:"inputIds"`
	Branch          int         `json:"branch"`
	WeightsBps      [5]int      `json:"weightsBps"`
	AmountsLamports []string    `json:"amountsLamports"`
	Action          string      `json:"action"`
	Reason          string      `json:"reason"`
	Before          BudgetState `json:"before"`
	After           BudgetState `json:"after"`
	OperationIDs    []string    `json:"operationIds"`
	Pending         bool        `json:"pending"`
}
type FinancialHistory struct {
	RecordID             string         `json:"recordId"`
	MarketBinding        Market         `json:"marketBinding"`
	Owner                string         `json:"owner"`
	RoundID              string         `json:"roundId"`
	EntryID              string         `json:"entryId"`
	TransactionID        string         `json:"transactionId"`
	InstructionIndex     string         `json:"instructionIndex"`
	EventIndex           string         `json:"eventIndex"`
	SourceCursor         string         `json:"sourceCursor"`
	Finality             Finality       `json:"finality"`
	ReceiptID            string         `json:"receiptId"`
	EvidenceClass        EvidenceClass  `json:"evidenceClass"`
	SessionID            string         `json:"sessionId"`
	StrategyDigest       string         `json:"strategyDigest"`
	DecisionID           string         `json:"decisionId"`
	Deposited            []NativeAmount `json:"deposited"`
	Withdrawn            []NativeAmount `json:"withdrawn"`
	CommittedLamports    string         `json:"committedLamports"`
	ReturnedLamports     string         `json:"returnedLamports"`
	FeeLamports          string         `json:"feeLamports"`
	ForfeitureLamports   string         `json:"forfeitureLamports"`
	UserCostsLamports    string         `json:"userCostsLamports"`
	BTCInputLamports     string         `json:"btcInputLamports"`
	BTCPending           []NativeAmount `json:"btcPending"`
	BTCAcquired          []NativeAmount `json:"btcAcquired"`
	BTCClaimed           []NativeAmount `json:"btcClaimed"`
	SATReserved          string         `json:"satReserved"`
	SATEarned            string         `json:"satEarned"`
	SATClaimed           string         `json:"satClaimed"`
	SATSold              string         `json:"satSold"`
	Category             string         `json:"category"`
	RealizedSaleProceeds []NativeAmount `json:"realizedSaleProceeds"`
	ExternalCashFlow     bool           `json:"externalCashFlow"`
}
type ValuationOverlay struct {
	RecordID         string               `json:"recordId"`
	Source           string               `json:"source"`
	At               string               `json:"at"`
	Currency         string               `json:"currency"`
	Price            Availability[string] `json:"price"`
	FeeAssumption    string               `json:"feeAssumption"`
	ImpactAssumption string               `json:"impactAssumption"`
	Depth            Availability[string] `json:"depth"`
	Stale            bool                 `json:"stale"`
}
type APIEnvelope[T any] struct {
	Schema       string   `json:"schema"`
	GeneratedAt  string   `json:"generatedAt"`
	SourceCursor string   `json:"sourceCursor"`
	Finality     Finality `json:"finality"`
	Data         T        `json:"data"`
	NextCursor   *string  `json:"nextCursor"`
}
type APIError struct {
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
	Message   string `json:"message"`
}

func HistoryIdentity(r FinancialHistory) string {
	b, _ := json.Marshal([]string{r.MarketBinding.NetworkGenesisHash, r.MarketBinding.ProgramID, r.TransactionID, r.InstructionIndex, r.EventIndex})
	return string(b)
}
