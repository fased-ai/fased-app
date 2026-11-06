package bootstrap

import (
	"encoding/json"
	"testing"
)

func TestCurrentEntryCapabilitiesPreservesVersion1(t *testing.T) {
	value := CurrentEntryCapabilities()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"version":1,"update":true,"releasePacketVersions":[1,2],"maintenanceDescriptor":true}` {
		t.Fatalf("entry wire contract changed: %s", raw)
	}
	value.ReleasePacketVersions[0] = 99
	next := CurrentEntryCapabilities()
	if next.ReleasePacketVersions[0] != 1 {
		t.Fatal("caller mutation changed the advertised parser capabilities")
	}
}
