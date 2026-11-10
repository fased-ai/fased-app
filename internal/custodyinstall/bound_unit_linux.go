//go:build linux

package custodyinstall

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// BoundUnitWriteArgs binds a single root-owned service staging file and its
// exact bytes into the privileged OS command. sudoers may then authorize that
// precise command instead of granting an unrestricted stdin write.
func BoundUnitWriteArgs(owner int, stage string, data []byte) ([]string, error) {
	if owner <= 0 || filepath.Clean(stage) != stage || len(data) == 0 || len(data) > 8192 {
		return nil, fmt.Errorf("invalid service staging request")
	}
	u := strconv.Itoa(owner)
	patterns := []string{
		`^/etc/systemd/system/\.fased-vps-` + u + `\.service-([a-f0-9]{32})\.pending$`,
		`^/etc/systemd/system/\.fased-custody-` + u + `-(app|signer)\.service-([a-f0-9]{32})\.pending$`,
		`^/var/lib/\.fased-app-vps-` + u + `-([a-f0-9]{32})\.pending/\.fased-vps-` + u + `\.service-([a-f0-9]{32})\.pending$`,
		`^/var/lib/\.fased-custody-` + u + `-([a-f0-9]{32})\.pending/\.fased-(app|signer)\.service-([a-f0-9]{32})\.pending$`,
		`^/etc/systemd/system/fased-vps-` + u + `\.service\.d/\.30-fased-provider-credentials\.conf-([a-f0-9]{32})\.pending$`,
	}
	matched := false
	for i, pattern := range patterns {
		m := regexp.MustCompile(pattern).FindStringSubmatch(stage)
		if len(m) == 0 {
			continue
		}
		if i == 2 && m[1] != m[2] || i == 3 && m[1] != m[3] {
			return nil, fmt.Errorf("service staging identity changed")
		}
		matched = true
		break
	}
	if !matched {
		return nil, fmt.Errorf("foreign service staging destination %q", stage)
	}
	return boundRootWriteArgs(owner, stage, data, "0022"), nil
}

// BoundPolicyWriteArgs is limited to this owner's generated custody sudo
// policies. The exact approved bytes, destination and restrictive mode are part
// of the command that the administrator may separately authorize.
func BoundPolicyWriteArgs(owner int, stage string, data []byte) ([]string, error) {
	if owner <= 0 || filepath.Clean(stage) != stage || len(data) == 0 || len(data) > 8192 {
		return nil, fmt.Errorf("invalid policy staging request")
	}
	u := strconv.Itoa(owner)
	pattern := `^/etc/sudoers[.]d/(fased-native-` + u + `|[.]fased-remote-proof-` + u + `)-[a-f0-9]{32}[.]pending$`
	if !regexp.MustCompile(pattern).MatchString(stage) {
		return nil, fmt.Errorf("foreign policy staging destination")
	}
	return boundRootWriteArgs(owner, stage, data, "0227"), nil
}

func boundRootWriteArgs(owner int, stage string, data []byte, umask string) []string {
	var encoded strings.Builder
	encoded.Grow(len(data) * 5)
	for _, b := range data {
		fmt.Fprintf(&encoded, `\0%03o`, b)
	}
	sum := sha256.Sum256([]byte(stage))
	unit := fmt.Sprintf("fased-bound-%d-%x", owner, sum[:8])
	return []string{"/usr/bin/systemd-run", "--quiet", "--wait", "--collect", "--unit=" + unit,
		"--property=Type=oneshot", "--property=UMask=" + umask, "--property=StandardOutput=truncate:" + stage,
		"--property=StandardError=null", "--", "/usr/bin/printf", "%b", encoded.String()}
}

func AllowedBoundPolicyWrite(owner int, data []byte, args []string) bool {
	if len(args) != 13 || !strings.HasPrefix(args[7], "--property=StandardOutput=truncate:") {
		return false
	}
	stage := strings.TrimPrefix(args[7], "--property=StandardOutput=truncate:")
	want, err := BoundPolicyWriteArgs(owner, stage, data)
	return err == nil && slices.Equal(args, want)
}

func AllowedBoundUnitWrite(owner int, data []byte, args []string) bool {
	if len(args) != 13 || !strings.HasPrefix(args[7], "--property=StandardOutput=truncate:") {
		return false
	}
	stage := strings.TrimPrefix(args[7], "--property=StandardOutput=truncate:")
	want, err := BoundUnitWriteArgs(owner, stage, data)
	return err == nil && slices.Equal(args, want)
}
