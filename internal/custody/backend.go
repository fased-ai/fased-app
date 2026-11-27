package custody

import (
	"context"
	"time"
)

// Backend defines the typed boundary implemented both by the in-process custody *Service
// and the out-of-process Unix socket *SocketClient. Neither interface ever exposes raw keys.
type Backend interface {
	Close() error
	VerifyAuditChain(ctx context.Context) (int, error)
	SecurityStatus(ctx context.Context) (SecurityStatus, error)
	HasOwnerAuth(ctx context.Context) (bool, error)
	ConfigureOwnerAuth(ctx context.Context, password string) error
	AuthorizeOwnerAction(ctx context.Context, password, action, walletID string) (string, time.Time, error)
	ListOwnerPasskeys(ctx context.Context) ([]OwnerPasskey, error)
	DeleteOwnerPasskey(ctx context.Context, credentialID string) (bool, error)
	BeginPasskeyRegistration(ctx context.Context, rpID, origin, label string) (PasskeyRegisterOptions, error)
	FinishPasskeyRegistration(ctx context.Context, req PasskeyRegisterFinishRequest) (OwnerPasskey, error)
	BeginPasskeyAssertion(ctx context.Context, rpID, origin, action, walletID, bindingDigest string) (PasskeyAssertOptions, error)
	FinishPasskeyAssertion(ctx context.Context, req PasskeyAssertFinishRequest) (string, time.Time, string, error)
	ListWallets(ctx context.Context) ([]WalletPublic, error)
	GetWallet(ctx context.Context, idOrHandle string) (WalletPublic, error)
	ResolveWallet(ctx context.Context, sel WalletSelector) (WalletPublic, error)
	CreateNativeWallet(ctx context.Context, name, role, primaryRPCURL string) (WalletPublic, error)
	AttachExternalWallet(ctx context.Context, custodyMode, name, role, publicKeyB58, primaryRPCURL string) (WalletPublic, error)
	UpdatePolicy(ctx context.Context, walletID string, next WalletPolicy, confirmLoosen bool, expectedPolicyHash, actor string) (WalletPublic, error)
	SetFreeze(ctx context.Context, walletID string, frozen bool, reason, actor string) (WalletPublic, error)
	UpdateConnections(ctx context.Context, walletID string, next WalletConnections, actor string) (WalletPublic, error)
	SetWalletRPC(ctx context.Context, walletID, primaryURL, fallbackURL, actor string) (WalletPublic, error)
	DeleteWallet(ctx context.Context, walletID string) (bool, error)
	FetchBalance(ctx context.Context, walletID string) (BalanceResult, error)
	PrepareReview(ctx context.Context, req PrepareReviewRequest) (WalletReview, error)
	ExecuteNativeReview(ctx context.Context, walletID, reviewID, actor string) (LedgerEntry, error)
	SubmitHardwareSignedReview(ctx context.Context, walletID, reviewID, signedTxB64, actor string) (LedgerEntry, error)
	ReconcileLedgerEntry(ctx context.Context, walletID, entryID string) (LedgerEntry, error)
	ListActivity(ctx context.Context, walletID string) (reviews []WalletReview, ledger []LedgerEntry, audit []AuditEntry, err error)
	ExportEncryptedRecovery(ctx context.Context, walletID, passphrase, actor string) ([]byte, string, error)
	ImportEncryptedRecovery(ctx context.Context, archiveRaw []byte, passphrase, name, role, primaryRPCURL string) (WalletPublic, error)
}

var (
	_ Backend = (*Service)(nil)
	_ Backend = (*SocketClient)(nil)
)
