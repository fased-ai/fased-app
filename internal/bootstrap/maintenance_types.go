package bootstrap

import (
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/update"
)

type MaintenanceIdentity struct {
	Version         string
	Source          string
	InstallerSHA256 string
	Core            bundle.File
}

// PackageMaintenanceIdentity is public, independently verified release
// metadata for the fixed native package path. It grants no install authority.
type PackageMaintenanceIdentity struct {
	MaintenanceIdentity
	Sequence         uint64
	ReleaseSHA256    string
	ProvenanceSHA256 string
	Target           update.Target
	Deb, RPM         update.Artifact
}
