package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fased-ai/fased-app/internal/signer"
)

// App-owned public records are preferences, never signer authority. No balance,
// private credential or asserted approval is accepted in this schema.
type Account struct {
	RPC       *AccountRPC        `json:"rpc,omitempty"`
	Version   int                `json:"version"`
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Mode      string             `json:"mode"`
	Network   string             `json:"network"`
	PublicKey string             `json:"publicKey"`
	Signer    *signer.AccountRef `json:"signer,omitempty"`
}
type ProposalDraft struct {
	AccountID string                 `json:"accountId"`
	Proposal  signer.FixtureProposal `json:"proposal"`
}
type AccountsRecord struct {
	Version   int             `json:"version"`
	Revision  int64           `json:"revision"`
	Accounts  []Account       `json:"accounts"`
	Proposals []ProposalDraft `json:"proposals"`
}
type FixtureView struct {
	Available bool                 `json:"available"`
	Status    *signer.PublicStatus `json:"status,omitempty"`
}
type ProposalView struct {
	ID      string                 `json:"id"`
	Digest  string                 `json:"digest"`
	Receipt signer.ApprovalReceipt `json:"receipt"`
}
type AccountsView struct {
	Setup         WalletSetup       `json:"setup"`
	Addresses     map[string]string `json:"addresses"`
	Record        AccountsRecord    `json:"record"`
	Fixture       FixtureView       `json:"fixture"`
	AccountStates map[string]string `json:"accountStates"`
	Proposals     []ProposalView    `json:"proposals"`
}

func managedNetworkAccount(a Account) bool {
	return a.Version == 3 && a.Mode == "fixture-managed" && a.Signer != nil && signer.ValidManagedAccountRef(*a.Signer) && a.Signer.Network == a.Network && a.Signer.PublicKey == a.PublicKey
}

func ordinaryContext(s *signer.PublicStatus) bool {
	if s == nil || s.Custody == nil || !s.Custody.ValidContext() || !s.AdmissionHeld || s.WalletSigner || s.FinancialExecution {
		return false
	}
	return s.Custody.Provenance == signer.ControlledCustodyProfile && s.FixtureOnly || s.Custody.Provenance == signer.OwnerKeyProvenance && !s.FixtureOnly && signer.OrdinaryOwnerAdmissionEnabled()
}
func ordinaryStatus(s *signer.PublicStatus) bool {
	return ordinaryContext(s) && s.Custody.Valid() && s.Account != nil && signer.ValidOrdinaryAccountRef(*s.Account)
}

func defaultAccounts() AccountsRecord {
	return AccountsRecord{Version: 1, Accounts: []Account{}, Proposals: []ProposalDraft{}}
}
func hexValue(s string, n int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == n && hex.EncodeToString(b) == s
}
func validName(s string) bool {
	if !utf8.ValidString(s) || len(s) < 1 || len(s) > 80 {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func (s *Store) configureSigner(path string) error {
	if path == "" {
		return nil
	}
	c, e := signer.LoadConfig(path)
	if e != nil {
		return e
	}
	if os.Geteuid() != c.AppUID {
		return errors.New("fixture view requires the configured app identity")
	}
	s.fixture = func(ctx context.Context, r signer.Request) (signer.Response, error) {
		// This bridge is read-only even if a future caller constructs another action.
		if r.Action != "status" && r.Action != "fixture-approvals" {
			return signer.Response{}, errors.New("owner administration is unavailable through the core")
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return signer.Call(ctx, c, false, r)
	}
	if c.Version == 2 || c.Version == 3 {
		s.custodyNetwork = c.Network
		s.custodyOwnerSupported = c.Version == 3 && c.Profile == signer.OwnerCustodyProfile && c.Provenance == signer.ControlledCustodyProfile
	}
	return nil
}
func (s *Store) fixtureView(ctx context.Context) FixtureView {
	if s.fixture == nil {
		return FixtureView{}
	}
	r, e := s.fixture(ctx, signer.Request{Action: "status"})
	if e != nil {
		return FixtureView{}
	}
	public := r.Status.PublicView()
	return FixtureView{Available: true, Status: &public}
}
func (s *Store) accountsRecord(ctx context.Context) (AccountsRecord, error) {
	var r AccountsRecord
	var b string
	e := s.db.QueryRowContext(ctx, "SELECT document,revision FROM accounts WHERE id=1").Scan(&b, &r.Revision)
	if e != nil {
		return r, e
	}
	rev := r.Revision
	e = json.Unmarshal([]byte(b), &r)
	r.Revision = rev
	return r, e
}
func (s *Store) Accounts(ctx context.Context) (AccountsView, error) {
	r, e := s.accountsRecord(ctx)
	if e != nil {
		return AccountsView{}, e
	}
	v := AccountsView{Record: r, Fixture: s.fixtureView(ctx), AccountStates: map[string]string{}, Proposals: []ProposalView{}}
	v.Setup = s.walletSetup(v.Fixture)
	v.Addresses = map[string]string{}
	for _, a := range r.Accounts {
		if (a.Version == 2 || a.Version == 3 || a.Version == 4) && solanaGenesis(a.Network) != "" {
			if address, err := signer.FormatPublicKey(a.PublicKey); err == nil {
				v.Addresses[a.ID] = address
			}
		}
		state := "public-record-only"
		if a.Mode == "fixture-managed" || a.Mode == "managed" {
			state = "unverified"
			if v.Fixture.Status != nil && v.Fixture.Status.Account != nil {
				state = "stale"
				if a.Signer != nil && *a.Signer == *v.Fixture.Status.Account {
					state = "current-fixture"
					if ordinaryNetworkAccount(a) && ordinaryStatus(v.Fixture.Status) {
						state = "current-custody-held"
					}
				}
			}
		}
		v.AccountStates[a.ID] = state
	}
	ids := []string{}
	for _, p := range r.Proposals {
		ids = append(ids, p.Proposal.ID)
	}
	receipts := map[string]signer.ApprovalReceipt{}
	if v.Fixture.Available && len(ids) > 0 {
		response, err := s.fixture(ctx, signer.Request{Action: "fixture-approvals", PublicInput: signer.PublicInput{ProposalIDs: ids}})
		if err == nil {
			for _, r := range response.Approvals {
				receipts[r.ProposalID] = r
			}
		}
	}
	for _, p := range r.Proposals {
		digest := signer.ProposalDigest(p.Proposal)
		receipt, ok := receipts[p.Proposal.ID]
		if !ok {
			receipt = signer.ApprovalReceipt{ProposalID: p.Proposal.ID, State: "unverified"}
		} else if receipt.Digest != "" && receipt.Digest != digest {
			receipt.State = "mismatched"
		}
		v.Proposals = append(v.Proposals, ProposalView{ID: p.Proposal.ID, Digest: digest, Receipt: receipt})
	}
	return v, nil
}
func validateAccounts(r AccountsRecord, old AccountsRecord, live FixtureView, now int64) error {
	if r.Version != 1 || r.Revision < 0 || len(r.Accounts) > 8 || len(r.Proposals) > 8 {
		return errors.New("invalid account record version/revision or capacity")
	}
	accounts := map[string]Account{}
	oldAccounts := map[string]Account{}
	oldProposals := map[string]ProposalDraft{}
	for _, a := range old.Accounts {
		oldAccounts[a.ID] = a
	}
	for _, p := range old.Proposals {
		oldProposals[p.Proposal.ID] = p
	}
	for _, a := range r.Accounts {
		if !((a.Version == 1 && a.Network == signer.FixtureNetwork) || (a.Version == 2 && a.Mode == "watch" && a.Signer == nil && solanaGenesis(a.Network) != "") || managedNetworkAccount(a) || ordinaryNetworkAccount(a)) || !hexValue(a.ID, 16) || !validName(a.Name) || !hexValue(a.PublicKey, 32) {
			return errors.New("use a versioned disposable public account; real wallets are disabled")
		}
		if _, ok := accounts[a.ID]; ok {
			return errors.New("duplicate account identity")
		}
		switch a.Mode {
		case "watch", "external":
			if a.Signer != nil {
				return errors.New("public/manual records have no signer authority")
			}
		case "managed":
			if !ordinaryNetworkAccount(a) {
				return errors.New("invalid ordinary custody reference")
			}
			prior, ok := oldAccounts[a.ID]
			if !ok || prior.Signer == nil || *prior.Signer != *a.Signer {
				if !ordinaryStatus(live.Status) || *live.Status.Account != *a.Signer || live.Status.UpdateHeld {
					return errors.New("stale or unavailable ordinary custody reference")
				}
			}
		case "fixture-managed":
			if a.Signer == nil || !(a.Version == 1 && signer.ValidAccountRef(*a.Signer) || managedNetworkAccount(a)) || a.Signer.PublicKey != a.PublicKey || a.Signer.Network != a.Network {
				return errors.New("invalid fixture account reference")
			}
			prior, ok := oldAccounts[a.ID]
			if !ok || prior.Signer == nil || *prior.Signer != *a.Signer {
				if live.Status == nil || live.Status.Account == nil || *live.Status.Account != *a.Signer || !live.Status.FixtureOnly || live.Status.WalletSigner || live.Status.FinancialExecution || live.Status.UpdateHeld {
					return errors.New("stale or unavailable signer reference; refresh before recording it")
				}
			}
		default:
			return errors.New("invalid account capability mode")
		}
		if a.RPC != nil && !validProviderRPC(a) && (a.RPC.Version != 1 || a.RPC.Endpoint != FixtureRPCURL || a.RPC.Network != a.Network || a.Network != signer.FixtureNetwork) {
			return errors.New("only disposable RPC settings are available")
		}
		accounts[a.ID] = a
	}
	seen := map[string]bool{}
	for _, d := range r.Proposals {
		p := d.Proposal
		a, ok := accounts[d.AccountID]
		if !ok || a.Version != 1 || a.Mode != "fixture-managed" || a.Signer == nil || p.Policy.Account != *a.Signer || p.Grant.Account != *a.Signer || seen[p.ID] {
			return errors.New("proposal must bind one recorded fixture account")
		}
		seen[p.ID] = true
		if prior, ok := oldProposals[p.ID]; ok {
			if prior.AccountID != d.AccountID || signer.ProposalDigest(prior.Proposal) != signer.ProposalDigest(p) {
				return errors.New("proposal identity is immutable; create a new proposal")
			}
		} else {
			if e := p.Validate(now); e != nil {
				return e
			}
			if live.Status == nil || live.Status.Account == nil || *live.Status.Account != *a.Signer || live.Status.RecoveryRequired {
				return errors.New("stale or held account cannot propose new authority")
			}
		}
	}
	return nil
}
func (s *Store) UpdateAccounts(ctx context.Context, r AccountsRecord) (AccountsView, error) {
	return s.updateAccounts(ctx, r, false)
}
func (s *Store) updateAccounts(ctx context.Context, r AccountsRecord, rpcChange bool) (AccountsView, error) {
	s.lockVenueCredentials()
	old, e := s.accountsRecord(ctx)
	if e != nil {
		return AccountsView{}, e
	}
	if old.Revision != r.Revision {
		return AccountsView{}, ErrConflict
	}
	if !rpcChange {
		prior := map[string]Account{}
		for _, a := range old.Accounts {
			prior[a.ID] = a
		}
		for i, a := range r.Accounts {
			before := prior[a.ID]
			if a.RPC != nil && (before.RPC == nil || *a.RPC != *before.RPC) {
				return AccountsView{}, errors.New("use the RPC settings command")
			}
			// Older clients may omit RPC fields. Preserve them on ordinary edits, but
			// never carry a configuration across a changed key, mode or signer epoch.
			if sameAccountIdentity(a, before) {
				r.Accounts[i].RPC = before.RPC
			} else {
				r.Accounts[i].RPC = nil
			}
		}
	}
	if !s.rpcAdmission {
		prior := map[string]Account{}
		for _, a := range old.Accounts {
			prior[a.ID] = a
		}
		for _, a := range r.Accounts {
			if a.Version == 2 {
				b, ok := prior[a.ID]
				if !ok || !sameAccountIdentity(a, b) || a.Version != b.Version || (a.RPC != nil && (b.RPC == nil || *a.RPC != *b.RPC)) {
					return AccountsView{}, errors.New("live wallet configuration disabled")
				}
			}
		}
	}
	if e = validateAccounts(r, old, s.fixtureView(ctx), s.now().Unix()); e != nil {
		return AccountsView{}, e
	}
	if r.Accounts == nil {
		r.Accounts = []Account{}
	}
	if r.Proposals == nil {
		r.Proposals = []ProposalDraft{}
	}
	b, e := json.Marshal(r)
	if e != nil {
		return AccountsView{}, e
	}
	if len(b) > 15000 {
		return AccountsView{}, errors.New("account record is too large")
	}
	result, e := s.db.ExecContext(ctx, "UPDATE accounts SET document=?,revision=revision+1 WHERE id=1 AND revision=?", string(b), r.Revision)
	if e != nil {
		return AccountsView{}, metadataWriteError(e)
	}
	n, e := result.RowsAffected()
	if e != nil {
		return AccountsView{}, e
	}
	if n != 1 {
		return AccountsView{}, ErrConflict
	}
	return s.Accounts(ctx)
}

func (s *Store) controlledOrdinaryAccount(ctx context.Context, a Account) bool {
	if !s.custodyOwnerSupported || !ordinaryNetworkAccount(a) {
		return false
	}
	status := s.fixtureView(ctx).Status
	return ordinaryStatus(status) && status.Custody.Profile == signer.OwnerCustodyProfile && status.Custody.Provenance == signer.ControlledCustodyProfile && *status.Account == *a.Signer
}
