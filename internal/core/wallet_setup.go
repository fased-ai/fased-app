package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"runtime"

	"github.com/fased-ai/fased-app/internal/signer"
)

// WalletSetup is public, non-persistent onboarding state. Owner key operations
// stay exclusively in the existing immutable owner helper, not this API.
type WalletSetup struct {
	CustodyClass        string `json:"custodyClass,omitempty"`
	AdmissionHeld       bool   `json:"admissionHeld,omitempty"`
	InstanceID          string `json:"instanceId"`
	Source              string `json:"source,omitempty"`
	Version             int    `json:"version"`
	Network             string `json:"network"`
	State               string `json:"state"`
	FixtureOnly         bool   `json:"fixtureOnly"`
	NetworkWatchEnabled bool   `json:"networkWatchEnabled"`
	OwnerSupported      bool   `json:"ownerSupported"`
}

type AccountSetupCommand struct {
	Revision  int64              `json:"revision"`
	Action    string             `json:"action"`
	Name      string             `json:"name"`
	Network   string             `json:"network"`
	Address   string             `json:"address,omitempty"`
	Reference *signer.AccountRef `json:"reference,omitempty"`
}

func (s *Store) walletSetup(live FixtureView) WalletSetup {
	v := WalletSetup{Version: 1, Network: s.custodyNetwork, State: "not-installed", FixtureOnly: true, NetworkWatchEnabled: s.rpcAdmission, OwnerSupported: runtime.GOOS == "linux"}
	v.InstanceID = s.instanceID
	switch s.installationSource.Load() {
	case 1:
		v.Source = "local"
	case 2:
		v.Source = "remote"
	}
	if s.fixture == nil && s.custodyNetwork == "" {
		return v
	}
	v.State = "unavailable"
	if !live.Available || live.Status == nil {
		return v
	}
	status := live.Status
	if (!status.FixtureOnly && !ordinaryContext(status)) || status.WalletSigner || status.FinancialExecution {
		return v
	}
	if status.Custody != nil {
		if !ordinaryContext(status) {
			return v
		}
		v.FixtureOnly = status.FixtureOnly
		v.CustodyClass = status.Custody.Class
		v.AdmissionHeld = true
		v.OwnerSupported = v.OwnerSupported && s.custodyOwnerSupported && status.Custody.Profile == signer.OwnerCustodyProfile && status.Custody.Provenance == signer.ControlledCustodyProfile
		if status.Account != nil && !ordinaryStatus(status) {
			return v
		}
	}
	if status.Account != nil {
		if !signer.ValidManagedAccountRef(*status.Account) && !ordinaryStatus(status) {
			v.State = "legacy"
			return v
		}
		if v.Network == "" {
			v.Network = status.Account.Network
		} else if v.Network != status.Account.Network {
			return v
		}
	}
	if ordinaryStatus(status) {
		v.CustodyClass = status.Custody.Class
		v.AdmissionHeld = true
	}
	if solanaGenesis(v.Network) == "" {
		v.State = "legacy"
		return v
	}
	switch {
	case status.UpdateHeld:
		v.State = "update-held"
	case status.Account == nil:
		v.State = "empty"
	case status.RecoveryRequired && status.Custody == nil:
		v.State = "recovery"
	case status.Locked:
		v.State = "locked"
	default:
		v.State = "unlocked"
	}
	return v
}

func (s *Store) SetupAccount(ctx context.Context, c AccountSetupCommand) (AccountsView, error) {
	fail := errors.New("choose a network, name and public account")
	if !validName(c.Name) || solanaGenesis(c.Network) == "" || c.Action != "watch" && c.Action != "attach" {
		return AccountsView{}, fail
	}
	r, err := s.accountsRecord(ctx)
	if err != nil {
		return AccountsView{}, err
	}
	if c.Revision != r.Revision {
		return AccountsView{}, ErrConflict
	}
	a := Account{Name: c.Name, Network: c.Network}
	switch c.Action {
	case "watch":
		if c.Reference != nil {
			return AccountsView{}, fail
		}
		if !s.rpcAdmission {
			return AccountsView{}, errors.New("network account setup is disabled in this build")
		}
		a.PublicKey, err = signer.ParsePublicAddress(c.Address)
		if err != nil {
			return AccountsView{}, err
		}
		a.Version, a.Mode = 2, "watch"
	case "attach":
		if c.Address != "" || c.Reference == nil || (!signer.ValidManagedAccountRef(*c.Reference) && !signer.ValidOrdinaryAccountRef(*c.Reference)) || c.Reference.Network != c.Network {
			return AccountsView{}, fail
		}
		live := s.fixtureView(ctx)
		setup := s.walletSetup(live)
		if setup.State == "update-held" {
			return AccountsView{}, ErrCorePaused
		}
		if setup.State != "locked" && setup.State != "unlocked" && setup.State != "recovery" || live.Status.Account == nil || *live.Status.Account != *c.Reference || setup.Network != c.Network {
			return AccountsView{}, errors.New("refresh the current signer identity before adding this wallet")
		}
		a.Version, a.Mode, a.PublicKey = 3, "fixture-managed", c.Reference.PublicKey
		if signer.ValidOrdinaryAccountRef(*c.Reference) {
			if !ordinaryStatus(live.Status) {
				return AccountsView{}, errors.New("ordinary custody admission unavailable")
			}
			a.Version, a.Mode = 4, "managed"
		}
		ref := *c.Reference
		a.Signer = &ref
	}
	for _, old := range r.Accounts {
		if old.Mode == a.Mode && old.Network == a.Network && old.PublicKey == a.PublicKey {
			if (a.Mode == "fixture-managed" || a.Mode == "managed") && old.Signer != nil && a.Signer != nil && *old.Signer != *a.Signer {
				// Recovery creates a distinct authority epoch. Keep the old public
				// record/credential binding intact; explicitly attach the new one.
				continue
			}
			return AccountsView{}, errors.New("wallet already added")
		}
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return AccountsView{}, err
	}
	a.ID = hex.EncodeToString(id)
	r.Accounts = append(r.Accounts, a)
	// Existing validation rechecks signer identity and revision at commit, and
	// preserves every prior record, credential binding, proposal and hold.
	return s.UpdateAccounts(ctx, r)
}
