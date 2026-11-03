package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"
)

// EntryCapabilities describes the installed command parser, not instance state.
// Entry ABI version1 remains compatible with readers that ignore additive fields.
type EntryCapabilities struct {
	Version               int   `json:"version"`
	Update                bool  `json:"update"`
	ReleasePacketVersions []int `json:"releasePacketVersions"`
}

func CurrentEntryCapabilities() EntryCapabilities {
	return EntryCapabilities{Version: 1, Update: true, ReleasePacketVersions: []int{1, 2}}
}

type entryCapabilityOutput struct{ data []byte }

func (b *entryCapabilityOutput) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > 8192 {
		return 0, errors.New("oversized entry capability response")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

// InstalledUpdateEntrySupported probes only the existing installed executable.
// Generic update support cannot establish that an older command parses packet2.
func InstalledUpdateEntrySupported(ctx context.Context, corePath string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, corePath, "version", "--entry-json")
	var out entryCapabilityOutput
	c.Stdout = &out
	c.Stderr = io.Discard
	if c.Run() != nil {
		return false
	}
	var capability EntryCapabilities
	if json.Unmarshal(out.data, &capability) != nil || capability.Version != 1 || !capability.Update {
		return false
	}
	supported := false
	for _, version := range capability.ReleasePacketVersions {
		if version <= 0 {
			return false
		}
		if version == 2 {
			supported = true
		}
	}
	return supported
}
