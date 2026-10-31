package custodyinstall

import "github.com/fased-ai/fased-app/internal/signer"

func signerStatusReady(m Manifest, s signer.Status) bool {
	if s.WalletSigner || s.FinancialExecution {
		return false
	}
	if m.Version != 3 {
		return s.FixtureOnly
	}
	if m.Provenance == signer.OwnerKeyProvenance && !signer.OrdinaryOwnerAdmissionEnabled() {
		return false
	}
	if s.Custody == nil || !s.Custody.ValidContext() || s.Custody.Class != m.Mode || s.Custody.Profile != m.Profile || s.Custody.Provenance != m.Provenance || !s.AdmissionHeld || s.FixtureOnly != (m.Provenance == signer.ControlledCustodyProfile) {
		return false
	}
	if s.Account == nil {
		return s.Custody.Origin == ""
	}
	return s.Custody.Valid() && signer.ValidOrdinaryAccountRef(*s.Account) && s.Account.SignerID == m.ID && s.Account.Network == m.Network && s.RecoveryRequired
}
