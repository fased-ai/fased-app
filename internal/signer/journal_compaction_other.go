//go:build !linux

package signer

func (*storage) hasPendingCompaction() (bool, error) { return false, nil }
func (*storage) recoverCompaction() error            { return nil }
func (*storage) compactJournal(*journal, []journalRecord) error {
	return ErrUnavailable
}

// CompactOperationsJournal is unavailable outside Linux hardened storage.
func (*Vault) CompactOperationsJournal(int) (int, error) {
	return 0, ErrUnavailable
}

func (*Vault) compactOperationsJournalLocked(int) (int, error) {
	return 0, ErrUnavailable
}

func validCompactionAnchor(journalRecord) bool { return false }
