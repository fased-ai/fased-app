package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

// Operator-owned initial-setup handoff survives a stop between deployment and
// first owner claim. It contains public identity only, never a password/key/code.
// It is used only for an unclaimed instance, never to replace an existing owner.
type remoteSetupOwner struct {
	Version        int              `json:"version"`
	Origin         string           `json:"origin"`
	Listen         string           `json:"listen"`
	EngineSHA256   string           `json:"engineSha256"`
	InstallationID string           `json:"installationId,omitempty"`
	InstanceID     string           `json:"instanceId,omitempty"`
	Owner          tailnet.Identity `json:"owner"`
}

func loadRemoteSetupOwner(dir string, m vpsinstall.Manifest, instance string) (tailnet.Identity, bool, error) {
	file := filepath.Join(dir, "owner-handoff.json")
	data, err := core.ReadPrivateFile(file)
	if os.IsNotExist(err) {
		return tailnet.Identity{}, false, nil
	}
	if err != nil {
		return tailnet.Identity{}, false, err
	}
	var record remoteSetupOwner
	if json.Unmarshal(data, &record) != nil || record.Version != 1 || record.Origin != m.PublicOrigin || record.Listen != m.Listen || record.EngineSHA256 != m.CoreSHA256 || record.Owner.UserID <= 0 || record.Owner.NodeID == "" || record.InstallationID != "" && record.InstallationID != m.ID || record.InstanceID != "" && record.InstanceID != instance {
		return tailnet.Identity{}, false, errors.New("initial Remote handoff differs; preserve it and inspect setup")
	}
	if record.InstallationID == "" || record.InstanceID == "" {
		record.InstallationID = m.ID
		record.InstanceID = instance
		if err = core.WritePrivateJSON(file, record); err != nil {
			return tailnet.Identity{}, false, err
		}
	}
	return record.Owner, true, nil
}
