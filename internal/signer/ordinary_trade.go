package signer

import "errors"

const ordinaryTradeFeature = "ordinary-typed-trade-fixture-v1"

// This dependency is supplied only by compiled disposable composition. Normal
// startup, an unlocked key and immutable provenance alone never enable trading.
func (v *Vault) ConfigureControlledOrdinarySwapObserver(observer SwapObserver) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.ordinary || v.closed || observer == nil || v.admissionContext.Profile != OwnerCustodyProfile || v.admissionContext.Provenance != ControlledCustodyProfile {
		return ErrUnavailable
	}
	v.swapObserver = observer
	v.ordinaryTradeEnabled = true
	v.ordinaryTradeGrants = nil
	return nil
}
func ordinaryTradeAdmission(a *CustodyAdmission) bool {
	return a != nil && a.Valid() && a.Profile == OwnerCustodyProfile && a.Provenance == ControlledCustodyProfile && a.Origin == "generated"
}
func (v *Vault) ordinaryTradeEligible() bool {
	return v.ordinary && v.ordinaryTradeEnabled && !v.closed && v.swapObserver != nil && v.record != nil && ordinaryEnvelope(*v.record) && ordinaryTradeAdmission(v.record.Admission) && v.record.RecoveredHistorySHA256 == "" && v.managedRef != nil && v.ledger != nil && v.ledger.ordinaryTradeFresh && v.record.Ref == *v.managedRef && v.record.Ref == v.ledger.ref && ordinaryTradeAdmission(v.ledger.ordinaryAdmission) && !v.store.failedState() && v.ledger.activeUpdate == ""
}
func ordinaryTradeAction(kind string) string {
	switch kind {
	case "ordinary-trade-approve":
		return "onchain-approve"
	case "ordinary-trade-sign":
		return "onchain-sign"
	case "ordinary-trade-settled":
		return "onchain-settled"
	case "ordinary-trade-cancel":
		return "onchain-cancel"
	}
	return kind
}
func (v *Vault) swapRecordKind(kind string) string {
	if v.ordinary {
		switch kind {
		case "onchain-approve":
			return "ordinary-trade-approve"
		case "onchain-sign":
			return "ordinary-trade-sign"
		case "onchain-settled":
			return "ordinary-trade-settled"
		case "onchain-cancel":
			return "ordinary-trade-cancel"
		}
	}
	return kind
}
func (l *ledger) ordinaryTradeRecord(r journalRecord) error {
	if l.ref.Version != 3 || !l.ordinaryTradeFresh || !ordinaryTradeAdmission(l.ordinaryAdmission) {
		return ErrUnavailable
	}
	if r.Kind == "ordinary-trade-approve" && (r.Swap == nil || r.Swap.Version != OrdinarySwapVersion) {
		return errors.New("ordinary typed approval required")
	}
	// The raw ordinary-domain record was strictly validated by ledger.apply.
	// Reuse only the typed swap transition validator; never persist this projection.
	r.Kind = ordinaryTradeAction(r.Kind)
	r.Admission = nil
	return l.applySwap(r)
}
