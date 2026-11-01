//go:build linux

package localinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"os"
	"path/filepath"
)

// The new OS adapter never overwrites the previous helper or its request.
// An unfinished old operation must be recovered by its retained owner first.
func legacyMaintenanceIdle(m Manifest) error {
	b, e := core.ReadPrivateFile(filepath.Join(applicationUpdateDirectory(Paths{Root: m.Root, Data: m.Data}), "maintenance.json"))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var r maintenanceRequest
	old := bytes.ReplaceAll(maintenanceTemplate(m), []byte("fased-app-maintenance-system"), []byte("fased-app-maintenance"))
	old = bytes.ReplaceAll(old, []byte("maintenance-delegate --operation"), []byte("maintain-update --operation"))
	old = bytes.ReplaceAll(old, []byte("TimeoutStartSec=16min"), []byte("TimeoutStartSec=15min"))
	delivered := bytes.Replace(old, []byte("NoNewPrivileges=yes\n"), []byte("NoNewPrivileges=yes\nCapabilityBoundingSet=\n"), 1)
	if len(b) > 4096 || json.Unmarshal(b, &r) != nil || r.Version != 1 || !applicationUpdateID(r.ID) || r.Root != m.Root || r.Data != m.Data || r.TemplateSHA256 != bytesDigest(old) && r.TemplateSHA256 != bytesDigest(delivered) || !validExpectedDigest(r.PreviousManifestSHA256) || !oneOfMaintenancePhase(r.Finished) {
		return errors.New("previous maintenance operation is unfinished or unrecognized; preserve and recover it before preparing the new adapter")
	}
	return nil
}
