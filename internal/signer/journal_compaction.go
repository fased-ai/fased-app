package signer

const (
	// MaxRetainedSwaps is the soft active/recent swap threshold before automatic compaction runs.
	MaxRetainedSwaps = 64

	// CompactionRetainRecentSwaps is the number of most-recent terminal swaps preserved on auto-compaction.
	CompactionRetainRecentSwaps = 16

	// CompactionTriggerRecords triggers automatic compaction well before the 8,192-record read bound.
	CompactionTriggerRecords = 4096
)
