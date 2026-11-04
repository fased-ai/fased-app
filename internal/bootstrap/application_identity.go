package bootstrap

import (
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/releaseops"
)

// ApplicationDescriptorIdentity excludes delivery IDs only for the attested
// main-dispatch format. The signed content is identical before and after
// publication; v1 and other trust profiles retain their original identity.
func ApplicationDescriptorIdentity(d Descriptor) string {
	if d.Version == 4 && d.Release.BuildRef == "refs/heads/main" && d.Release.TagObject == "" {
		d.GitHub = releaseops.GitHubAsset{}
		b, _ := json.Marshal(d)
		return digest(append([]byte("fased-app:attested-content:v2\x00"), b...))
	}
	b, _ := json.Marshal(d)
	return digest(b)
}

// ApplicationDescriptorMatches recognizes the exact independently verified
// legacy v2 serialization. It never compares only version or executable hash,
// and never rewrites a recorded operation, lease, journal or installed floor.
func ApplicationDescriptorMatches(d Descriptor, identity string) bool {
	if d.Version != 4 || d.Release.BuildRef != "refs/heads/main" || d.Release.TagObject != "" {
		return false
	}
	if identity == ApplicationDescriptorIdentity(d) {
		return true
	}
	if d.GitHub.ReleaseID == "" {
		return false
	}
	b, _ := json.Marshal(d)
	return identity == digest(b)
}

func applicationIdentity(d Descriptor, legacy string) string {
	if d.Version == 4 && d.Release.BuildRef == "refs/heads/main" && d.Release.TagObject == "" {
		return ApplicationDescriptorIdentity(d)
	}
	return legacy
}

func (p *Prepared) matchesApplicationIdentity(identity string) bool {
	return identity == p.report.DescriptorSHA256 || ApplicationDescriptorMatches(p.descriptor, identity)
}
