package main

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"testing"
)

func TestVersionEntryAdvertisesReleasePacketSupport(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"version", "--entry-json"}, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var capability struct {
		Version               int   `json:"version"`
		Update                bool  `json:"update"`
		ReleasePacketVersions []int `json:"releasePacketVersions"`
	}
	if err := json.Unmarshal(out.Bytes(), &capability); err != nil {
		t.Fatal(err)
	}
	if capability.Version != 1 || !capability.Update || !slices.Contains(capability.ReleasePacketVersions, 2) {
		t.Fatal("entry capability cannot distinguish an older packet reader", out.String())
	}
}
