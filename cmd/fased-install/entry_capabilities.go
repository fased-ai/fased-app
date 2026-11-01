package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"
)

var errEntryUpgradeRequired = errors.New("verified newer update entry required")

type entryCapabilityOutput struct{ data []byte }

func (b *entryCapabilityOutput) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > 8192 {
		return 0, errors.New("oversized entry capability response")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
func installedUpdateEntrySupported(ctx context.Context, corePath string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, corePath, "version", "--entry-json")
	var out entryCapabilityOutput
	c.Stdout = &out
	c.Stderr = io.Discard
	if c.Run() != nil {
		return false
	}
	var v struct {
		Version int  `json:"version"`
		Update  bool `json:"update"`
	}
	return json.Unmarshal(out.data, &v) == nil && v.Version == 1 && v.Update
}
