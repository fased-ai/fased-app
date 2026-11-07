//go:build linux

package custodyinstall

import (
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/core"
)

func OwnsEnrollmentIntent(m Manifest) bool {
	config, err := os.UserConfigDir()
	if err != nil || m.Owner != os.Getuid() {
		return false
	}
	b, err := core.ReadPrivateFile(filepath.Join(config, "fased-app-custody-installer", "intent.json"))
	var j journal
	return err == nil && strict(b, &j) == nil && j.validate(m.Owner) == nil && j.ID == m.ID && j.CoreHash == m.CoreHash && j.SignerHash == m.SignerHash && j.Network == m.Network && j.mode() == m.Mode && j.Profile == m.Profile && j.Provenance == m.Provenance && j.Listen == m.Listen && j.Enrollment == m.Enrollment && j.Location == m.Location && j.PublicOrigin == m.PublicOrigin && j.NativeOwnerAccess == m.NativeOwnerAccess
}
