//go:build linux

package signer

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	// MaxRetainedSwaps is the soft active/recent swap threshold before automatic compaction runs.
	MaxRetainedSwaps = 64

	// CompactionRetainRecentSwaps is the number of most-recent terminal swaps preserved on auto-compaction.
	CompactionRetainRecentSwaps = 16

	// CompactionTriggerRecords triggers automatic compaction well before the 8,192-record read bound.
	CompactionTriggerRecords = 4096

	compactionPendingName = "operations.log.compact.pending"
)

func (s *storage) hasPendingCompaction() (bool, error) {
	fd, err := unix.Openat(s.dir, compactionPendingName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	_ = unix.Close(fd)
	return true, nil
}

func (s *storage) recoverCompaction() error {
	pending, err := s.hasPendingCompaction()
	if err != nil {
		return err
	}
	if !pending {
		return nil
	}
	if _, _, err := s.readJournal("operations.log"); err != nil {
		return errors.New("compaction recovery failed: operations journal unreadable")
	}
	if err := unix.Unlinkat(s.dir, compactionPendingName, 0); err != nil {
		return err
	}
	return unix.Fsync(s.dir)
}

func (s *storage) compactJournal(j *journal, records []journalRecord) (result error) {
	if s.readOnly {
		return errors.New("cold inspection cannot compact journal")
	}
	if s.failed {
		return errors.New("storage outcome uncertain; restart signer")
	}
	if j == nil || j.name != "operations.log" || len(records) == 0 || len(records) >= 8192 {
		return errors.New("invalid journal compaction input")
	}
	prev := ""
	var buf []byte
	for i := range records {
		records[i].Version = JournalFormat
		records[i].Sequence = i + 1
		records[i].Previous = prev
		records[i].Hash = recordHash(records[i])
		line, err := json.Marshal(records[i])
		if err != nil {
			return err
		}
		if len(line)+1 > 64*1024 {
			return errors.New("compacted journal record exceeds bound")
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
		prev = records[i].Hash
	}
	if len(buf) == 0 || len(buf) > maxJournal {
		return errors.New("compacted journal exceeds max size")
	}

	defer func() {
		if result != nil {
			s.failed = true
		}
	}()
	if err := s.checkpoint("journal:compact:before"); err != nil {
		return err
	}

	curFD, err := unix.Openat(s.dir, j.name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	var st unix.Stat_t
	statErr := unix.Fstat(curFD, &st)
	fdCheckErr := journalFD(curFD)
	_ = unix.Close(curFD)
	if statErr != nil || fdCheckErr != nil {
		return errors.New("operations journal cannot be verified before compaction")
	}
	if st.Size != int64(j.size) {
		return errors.New("fixture journal changed outside signer")
	}

	fd, err := unix.Openat(s.dir, compactionPendingName, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), compactionPendingName)
	_, writeErr := f.Write(buf)
	var syncErr error
	if writeErr == nil && len(buf) > 0 {
		syncErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := unix.Fsync(s.dir); err != nil {
		return err
	}
	if err := s.checkpoint("journal:compact:staged"); err != nil {
		return err
	}

	if err := unix.Renameat(s.dir, compactionPendingName, s.dir, j.name); err != nil {
		return err
	}
	if err := unix.Fsync(s.dir); err != nil {
		return err
	}
	if err := s.checkpoint("journal:compact:published"); err != nil {
		return err
	}
	j.seq = len(records)
	j.hash = prev
	j.size = len(buf)
	return nil
}

func isSwapRecordKind(kind string) bool {
	switch kind {
	case "onchain-approve", "onchain-sign", "onchain-settled", "onchain-cancel",
		"ordinary-trade-approve", "ordinary-trade-sign", "ordinary-trade-settled", "ordinary-trade-cancel":
		return true
	}
	return false
}

// CompactOperationsJournal prunes settled/cancelled/expired swap records older than
// the most recent retainRecentSwaps entries, re-chains operations.log with a
// cryptographic anchor hash, dry-run verifies replay, and publishes atomically.
func (v *Vault) CompactOperationsJournal(retainRecentSwaps int) (int, error) {
	if v == nil {
		return 0, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.compactOperationsJournalLocked(retainRecentSwaps)
}

func (v *Vault) compactOperationsJournalLocked(retainRecentSwaps int) (int, error) {
	if v.closed || v.store == nil || v.store.readOnly || v.store.failedState() || v.ledger == nil || v.ledger.main == nil {
		return 0, ErrUnavailable
	}
	if retainRecentSwaps < 0 || retainRecentSwaps >= MaxRetainedSwaps {
		return 0, errors.New("invalid swap retention count")
	}
	if v.ledger.activeUpdate != "" {
		return 0, errors.New("exclusive update intent blocks journal compaction")
	}
	nowUnix := v.now().Unix()
	for _, s := range v.ledger.swaps {
		if s.State == "signed" || (s.State == "approved" && s.Proposal.Quote.ExpiresAt > nowUnix) {
			return 0, ErrHeld
		}
	}
	for _, op := range v.ledger.ops {
		if op.State == "reserved" || op.State == "prepared" || op.State == "sending" {
			return 0, ErrHeld
		}
	}

	main, records, err := v.store.readJournal("operations.log")
	if err != nil {
		return 0, err
	}
	if len(records) == 0 || main.seq != v.ledger.main.seq || main.hash != v.ledger.main.hash {
		return 0, errors.New("operations journal diverged from memory ledger")
	}

	lastSeqBySwap := map[string]int{}
	for _, r := range records {
		if isSwapRecordKind(r.Kind) && r.ID != "" {
			lastSeqBySwap[r.ID] = r.Sequence
		}
	}
	type swapOrder struct {
		id  string
		seq int
	}
	var terminal []swapOrder
	for id, s := range v.ledger.swaps {
		if s.State == "confirmed" || s.State == "failed" || s.State == "cancelled" || (s.State == "approved" && s.Proposal.Quote.ExpiresAt <= nowUnix) {
			terminal = append(terminal, swapOrder{id: id, seq: lastSeqBySwap[id]})
		}
	}
	if len(terminal) <= retainRecentSwaps {
		return 0, nil
	}
	sort.Slice(terminal, func(i, j int) bool {
		if terminal[i].seq != terminal[j].seq {
			return terminal[i].seq < terminal[j].seq
		}
		return terminal[i].id < terminal[j].id
	})
	pruneCount := len(terminal) - retainRecentSwaps
	pruneIDs := make(map[string]bool, pruneCount)
	for i := 0; i < pruneCount; i++ {
		pruneIDs[terminal[i].id] = true
	}

	compacted := make([]journalRecord, 0, len(records)+1)
	var maxAt int64
	for i, r := range records {
		if r.At > maxAt {
			maxAt = r.At
		}
		if i > 0 && isSwapRecordKind(r.Kind) && pruneIDs[r.ID] {
			continue
		}
		compacted = append(compacted, r)
	}
	compactAt := nowUnix
	if compactAt < maxAt {
		compactAt = maxAt
	}
	anchor := journalRecord{
		At:           compactAt,
		Kind:         "journal-compact",
		OriginalHash: main.hash,
		Outcome:      fmt.Sprintf("compacted:%d:%d", main.seq, len(pruneIDs)),
	}
	if v.ledger.ref.Version == 3 && v.ledger.ordinaryAdmission != nil {
		anchor.Admission = clone(v.ledger.ordinaryAdmission)
	}
	compacted = append(compacted, anchor)

	// Re-chain in memory and dry-run replay to guarantee 100% state equivalence before disk write.
	dryRun := make([]journalRecord, len(compacted))
	copy(dryRun, compacted)
	prev := ""
	for i := range dryRun {
		dryRun[i].Version = JournalFormat
		dryRun[i].Sequence = i + 1
		dryRun[i].Previous = prev
		dryRun[i].Hash = recordHash(dryRun[i])
		prev = dryRun[i].Hash
	}

	replayed := newLedger()
	switch v.ledger.ref.Version {
	case 1:
		for _, r := range dryRun {
			if err := replayed.apply(r); err != nil {
				return 0, fmt.Errorf("compaction dry-run replay failed: %w", err)
			}
		}
	case 2:
		ref, err := managedHistory(dryRun)
		if err != nil || !sameWallet(*ref, v.ledger.ref) {
			return 0, errors.New("compaction dry-run managed verification failed")
		}
		replayed.ref = *ref
		replayed.managedFresh = dryRun[0].FreshManaged
		replayed.lastTime = dryRun[0].At
		for _, r := range dryRun[1:] {
			if err := replayed.apply(r); err != nil {
				return 0, err
			}
		}
	case 3:
		ref, adm, err := ordinaryHistory(dryRun)
		if err != nil || !sameWallet(*ref, v.ledger.ref) || adm == nil || v.ledger.ordinaryAdmission == nil || *adm != *v.ledger.ordinaryAdmission {
			return 0, errors.New("compaction dry-run ordinary verification failed")
		}
		replayed.ref = *ref
		replayed.ordinaryAdmission = adm
		replayed.lastTime = dryRun[0].At
		for _, r := range dryRun[1:] {
			if err := replayed.apply(r); err != nil {
				return 0, err
			}
		}
	default:
		return 0, errors.New("unsupported ledger version for compaction")
	}

	if replayed.ref != v.ledger.ref || replayed.installedSequence != v.ledger.installedSequence || replayed.updateFloor != v.ledger.updateFloor || replayed.activeUpdate != v.ledger.activeUpdate {
		return 0, errors.New("compaction altered non-swap signer state")
	}
	for id, want := range v.ledger.swaps {
		if pruneIDs[id] {
			continue
		}
		got, ok := replayed.swaps[id]
		if !ok || got.State != want.State || got.Proposal.Digest != want.Proposal.Digest {
			return 0, errors.New("compaction altered retained swap state")
		}
	}
	if (replayed.receiptRPC == nil) != (v.ledger.receiptRPC == nil) {
		return 0, errors.New("compaction altered receipt RPC state")
	}

	if err := v.store.compactJournal(v.ledger.main, dryRun); err != nil {
		return 0, err
	}
	v.ledger.swaps = replayed.swaps
	v.ledger.lastTime = replayed.lastTime
	return len(pruneIDs), nil
}

func validCompactionAnchor(r journalRecord) bool {
	if len(r.OriginalHash) != 64 || !strings.HasPrefix(r.Outcome, "compacted:") {
		return false
	}
	b, err := hex.DecodeString(r.OriginalHash)
	if err != nil || len(b) != 32 || hex.EncodeToString(b) != r.OriginalHash {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.Outcome, "compacted:"), ":")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}
