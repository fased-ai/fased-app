//go:build linux

package custodyinstall

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// BoundAtomicUnitWriteArgs creates only the exact transaction unit and embeds
// its bytes in the allowed OS command. The no-clobber operation never rewrites
// an already published unit. Unit-start permission must be granted separately,
// only after provisioning has finished and the loaded units are verified.
func BoundAtomicUnitWriteArgs(owner int, target string, data []byte) ([]string, error) {
	if owner <= 0 || filepath.Clean(target) != target || len(data) == 0 || len(data) > 32768 {
		return nil, fmt.Errorf("invalid atomic service request")
	}
	pattern := `^/etc/systemd/system/fased-custody-` + strconv.Itoa(owner) + `-atomic-[a-f0-9]{32}-(forward|rollback|completion|cleanup-rollback|cleanup-complete)\.service$`
	reusablePattern := `^/etc/systemd/system/fased-custody-` + strconv.Itoa(owner) + `-(atomic-(launcher|provision|fence|compat(-(runner|next-core|previous-signer|next-signer))?|setup)|stage-(image|meta|unit)@)\.service$`
	reusablePending := `^/etc/systemd/system/[.]fased-custody-` + strconv.Itoa(owner) + `-(atomic-(launcher|provision|fence|compat(-(runner|next-core|previous-signer|next-signer))?|setup)|stage-(image|meta|unit)@)\.service-[a-f0-9]{32}[.]pending$`
	guardPattern := `^/etc/systemd/system/fased-custody-` + strconv.Itoa(owner) + `-(app|signer)\.service\.d/20-fased-update-guard\.conf$`
	guardPendingPattern := `^/etc/systemd/system/fased-custody-` + strconv.Itoa(owner) + `-(app|signer)\.service\.d/[.]20-fased-update-guard\.conf-[a-f0-9]{32}[.]pending$`
	guardPending := regexp.MustCompile(guardPendingPattern).MatchString(target)
	guard := regexp.MustCompile(guardPattern).MatchString(target) || guardPending
	if guard && string(data) != "[Unit]\nAssertPathExists=!"+Root(owner)+"/update-start-blocked.json\n" {
		return nil, fmt.Errorf("restart assertion content changed")
	}
	if !guard && !regexp.MustCompile(pattern).MatchString(target) && !regexp.MustCompile(reusablePattern).MatchString(target) && !regexp.MustCompile(reusablePending).MatchString(target) {
		return nil, fmt.Errorf("foreign atomic service destination")
	}
	if guardPending || regexp.MustCompile(reusablePending).MatchString(target) {
		unit, err := reusableSetupSlotUnit(owner, target)
		if err != nil {
			return nil, err
		}
		return boundNoClobberWriteArgsWithUnit(target, data, "0022", unit), nil
	}
	return boundNoClobberWriteArgs(owner, target, data, "0022"), nil
}

// Static reusable metadata is published once before its start-only policy.
// Exact bytes remain in the one-time authorized OS command, never stdin.
func BoundReusableMetadataWriteArgs(owner int, target string, data []byte) ([]string, error) {
	if owner <= 0 || filepath.Clean(target) != target || len(data) == 0 || len(data) > 16384 {
		return nil, fmt.Errorf("invalid reusable metadata")
	}
	pattern := `^` + regexp.QuoteMeta(Root(owner)+"/maintenance/") + `[.](authority[.]json|reusable-v1[.]json|guard[.]sha256|preparation-fence[.](json|sha256)|policy(-slot-[01])?[.]sha256|(setup|worker|provision)[.]env|compat-(runner|next-core|previous-signer|next-signer)[.]out|copy-(runner|guard)[.]json|stage-(image|meta|unit)[.]allow)-[a-f0-9]{32}[.]pending$`
	if !regexp.MustCompile(pattern).MatchString(target) {
		return nil, fmt.Errorf("foreign reusable metadata destination")
	}
	unit, err := reusableSetupSlotUnit(owner, target)
	if err != nil {
		return nil, err
	}
	return boundNoClobberWriteArgsWithUnit(target, data, "0022", unit), nil
}

func AllowedBoundReusableMetadataWrite(owner int, data []byte, args []string) bool {
	if len(args) != 17 {
		return false
	}
	want, err := BoundReusableMetadataWriteArgs(owner, args[len(args)-1], data)
	return err == nil && slices.Equal(args, want)
}

// The privileged operation compares its root-owned input to bytes bound into
// the exact sudo command before linking it. find does not follow symlinks.
func boundReusableSetupLinkArgs(owner int, id, source, target string, data []byte) ([]string, error) {
	if owner <= 0 || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(id) || len(data) == 0 || len(data) > 32768 || filepath.Clean(source) != source || filepath.Clean(target) != target {
		return nil, fmt.Errorf("invalid reusable setup publication")
	}
	pending := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-"+id+".pending")
	if source == pending {
		_, metaErr := BoundReusableMetadataWriteArgs(owner, source, data)
		_, unitErr := BoundAtomicUnitWriteArgs(owner, source, data)
		if metaErr != nil && unitErr != nil {
			return nil, fmt.Errorf("foreign reusable setup publication")
		}
	} else {
		pattern := `^` + regexp.QuoteMeta(Root(owner)+"/maintenance/") + `((setup|worker|provision)[.]env|compat-(runner|next-core|previous-signer|next-signer)[.]out)$`
		if target != source+".owner" || !regexp.MustCompile(pattern).MatchString(source) || string(data) != "FASED_MAINTENANCE\n" {
			return nil, fmt.Errorf("foreign reusable setup marker")
		}
		pending = filepath.Join(filepath.Dir(source), "."+filepath.Base(source)+"-"+id+".pending")
	}
	unit, err := reusableSetupSlotUnit(owner, pending)
	if err != nil {
		return nil, err
	}
	check := []byte(sum(data) + "  " + source + "\n")
	return []string{"/usr/bin/systemd-run", "--quiet", "--wait", "--collect", "--unit=" + unit,
		"--property=Type=oneshot", "--property=StandardInputData=" + base64.StdEncoding.EncodeToString(check),
		"--property=StandardOutput=null", "--property=StandardError=null", "--", "/usr/bin/find", source,
		"-maxdepth", "0", "-type", "f", "-uid", "0", "-gid", "0", "-perm", "0644", "-links", "1",
		"-exec", "/usr/bin/sha256sum", "--check", "--status", "--", "/dev/stdin", ";",
		"-exec", "/usr/bin/ln", "--no-target-directory", "--", "{}", target, ";"}, nil
}

func allowedBoundReusableSetupLink(owner int, data []byte, args []string) bool {
	if len(args) != 38 {
		return false
	}
	pattern := `^--unit=fased-setup-slot-` + strconv.Itoa(owner) + `-([a-f0-9]{32})-[a-f0-9]{16}$`
	match := regexp.MustCompile(pattern).FindStringSubmatch(args[4])
	if len(match) != 2 {
		return false
	}
	want, err := boundReusableSetupLinkArgs(owner, match[1], args[11], args[len(args)-2], data)
	return err == nil && slices.Equal(args, want)
}

// A published staging link may be removed only while it is still the same
// root-owned inode as the exact published file. The shared unit serializes it
// with this slot's writer and publisher.
func boundReusableSetupCleanupArgs(owner int, id, pending, target string, data []byte) ([]string, error) {
	if _, err := boundReusableSetupLinkArgs(owner, id, pending, target, data); err != nil {
		return nil, err
	}
	unit, err := reusableSetupSlotUnit(owner, pending)
	if err != nil {
		return nil, err
	}
	check := []byte(sum(data) + "  " + pending + "\n")
	return []string{"/usr/bin/systemd-run", "--quiet", "--wait", "--collect", "--unit=" + unit,
		"--property=Type=oneshot", "--property=StandardInputData=" + base64.StdEncoding.EncodeToString(check),
		"--property=StandardOutput=null", "--property=StandardError=null", "--property=ExecStartPre=/usr/bin/test ! -L " + target, "--", "/usr/bin/find", filepath.Dir(pending),
		"-maxdepth", "1", "-name", filepath.Base(pending), "-type", "f", "-uid", "0", "-gid", "0", "-perm", "0644", "-links", "2", "-samefile", target,
		"-exec", "/usr/bin/sha256sum", "--check", "--status", "--", "/dev/stdin", ";", "-delete"}, nil
}

func allowedBoundReusableSetupCleanup(owner int, data []byte, args []string) bool {
	if len(args) != 37 {
		return false
	}
	pattern := `^--unit=fased-setup-slot-` + strconv.Itoa(owner) + `-([a-f0-9]{32})-[a-f0-9]{16}$`
	match := regexp.MustCompile(pattern).FindStringSubmatch(args[4])
	if len(match) != 2 {
		return false
	}
	want, err := boundReusableSetupCleanupArgs(owner, match[1], filepath.Join(args[12], args[16]), args[28], data)
	return err == nil && slices.Equal(args, want)
}

// The hidden sudoers staging name is not loaded by sudo. Exact content is
// embedded in the one-time OS command and an existing file is never truncated.
func BoundReusablePolicyWriteArgs(owner int, stage string, data []byte) ([]string, error) {
	if owner <= 0 || filepath.Clean(stage) != stage || len(data) == 0 || len(data) > 8192 || !regexp.MustCompile(`^/etc/sudoers[.]d/[.]fased-custody-`+strconv.Itoa(owner)+`-reusable-[a-f0-9]{32}[.]pending$`).MatchString(stage) {
		return nil, fmt.Errorf("invalid reusable policy staging request")
	}
	unit, err := reusableSetupSlotUnit(owner, stage)
	if err != nil {
		return nil, err
	}
	return boundNoClobberWriteArgsWithUnit(stage, data, "0227", unit), nil
}

func AllowedBoundReusablePolicyWrite(owner int, data []byte, args []string) bool {
	if len(args) != 17 {
		return false
	}
	want, err := BoundReusablePolicyWriteArgs(owner, args[len(args)-1], data)
	return err == nil && slices.Equal(args, want)
}

// These one-time copy/create operations are conditional at the OS boundary:
// calling them directly cannot replace an existing staging path or inbox.
func boundReusableInboxCreateArgs(owner, signerGID int, id string) ([]string, error) {
	if owner <= 0 || signerGID <= 0 || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(id) {
		return nil, fmt.Errorf("invalid reusable inbox identity")
	}
	path := Root(owner) + "/maintenance/inbox"
	unit := fmt.Sprintf("fased-custody-%d-setup-inbox-%s", owner, id)
	return []string{"/usr/bin/systemd-run", "--quiet", "--wait", "--collect", "--unit=" + unit, "--property=Type=oneshot", "--property=ConditionPathExists=!" + path, "--", "/usr/bin/install", "-d", "-m", "2710", "-o", strconv.Itoa(owner), "-g", strconv.Itoa(signerGID), "--", path}, nil
}

func allowedBoundReusableInboxCreate(owner int, args []string) bool {
	if len(args) != 18 {
		return false
	}
	pattern := `^--unit=fased-custody-` + strconv.Itoa(owner) + `-setup-inbox-([a-f0-9]{32})$`
	match := regexp.MustCompile(pattern).FindStringSubmatch(args[4])
	gid, err := strconv.Atoi(args[15])
	if len(match) != 2 || err != nil {
		return false
	}
	want, err := boundReusableInboxCreateArgs(owner, gid, match[1])
	return err == nil && slices.Equal(args, want)
}

func boundReusableImageCopyArgs(owner int, id, component, digest string) ([]string, error) {
	if owner <= 0 || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(id) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(digest) || (component != "runner" && component != "guard") {
		return nil, fmt.Errorf("invalid reusable image copy identity")
	}
	name := "runner"
	if component == "guard" {
		name = "atomic-guard"
	}
	source := fmt.Sprintf("/var/lib/fased-app-approved-%d/%s", owner, digest)
	pending := Root(owner) + "/maintenance/." + name + "-" + id + ".pending"
	marker := pending + ".source"
	unit, err := reusableSetupSlotUnit(owner, pending)
	if err != nil {
		return nil, err
	}
	return []string{"/usr/bin/systemd-run", "--quiet", "--wait", "--collect", "--unit=" + unit,
		"--property=Type=oneshot", "--property=ConditionPathExists=!" + pending, "--property=ConditionPathExists=!" + marker,
		"--property=StandardInput=file:" + source, "--property=StandardOutput=null", "--property=StandardError=null",
		"--property=ExecStartPre=/usr/bin/install -m 0755 -o root -g root -- /dev/null " + pending,
		"--property=ExecStartPre=/usr/bin/ln --no-target-directory -- " + pending + " " + marker,
		"--", "/usr/bin/tee", "--append", "--", pending}, nil
}

func allowedBoundReusableImageCopy(owner int, args []string) bool {
	if len(args) != 18 {
		return false
	}
	pattern := `^` + regexp.QuoteMeta(Root(owner)+"/maintenance/.") + `(runner|atomic-guard)-([a-f0-9]{32})[.]pending$`
	match := regexp.MustCompile(pattern).FindStringSubmatch(args[len(args)-1])
	if len(match) != 3 {
		return false
	}
	const input = "--property=StandardInput=file:"
	if !strings.HasPrefix(args[8], input) {
		return false
	}
	digest := filepath.Base(strings.TrimPrefix(args[8], input))
	component := match[1]
	if component == "atomic-guard" {
		component = "guard"
	}
	want, err := boundReusableImageCopyArgs(owner, match[2], component, digest)
	return err == nil && slices.Equal(args, want)
}

// Verify the root-approved source and its staged copy within the privileged
// operation itself; a controller-side check alone cannot protect this link.
func boundReusableImageLinkArgs(owner int, id, component, digest string) ([]string, error) {
	if owner <= 0 || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(id) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(digest) || (component != "runner" && component != "guard") {
		return nil, fmt.Errorf("invalid reusable image publication")
	}
	name := "runner"
	if component == "guard" {
		name = "atomic-guard"
	}
	source := fmt.Sprintf("/var/lib/fased-app-approved-%d/%s", owner, digest)
	pending := Root(owner) + "/maintenance/." + name + "-" + id + ".pending"
	marker := pending + ".source"
	target := Root(owner) + "/maintenance/" + name
	check := []byte(digest + "  " + source + "\n")
	unit, err := reusableSetupSlotUnit(owner, pending)
	if err != nil {
		return nil, err
	}
	return []string{"/usr/bin/systemd-run", "--quiet", "--wait", "--collect", "--unit=" + unit,
		"--property=Type=oneshot", "--property=StandardInputData=" + base64.StdEncoding.EncodeToString(check),
		"--property=StandardOutput=null", "--property=StandardError=null", "--property=ExecStartPre=/usr/bin/test ! -L " + marker, "--", "/usr/bin/find", pending,
		"-maxdepth", "0", "-type", "f", "-uid", "0", "-gid", "0", "-perm", "0755", "-links", "2", "-samefile", marker,
		"-exec", "/usr/bin/sha256sum", "--check", "--status", "--", "/dev/stdin", ";",
		"-exec", "/usr/bin/cmp", "--silent", "--", "{}", source, ";",
		"-exec", "/usr/bin/ln", "--no-target-directory", "--", "{}", target, ";"}, nil
}

func allowedBoundReusableImageLink(owner int, args []string) bool {
	if len(args) != 48 {
		return false
	}
	pattern := `^` + regexp.QuoteMeta(Root(owner)+"/maintenance/.") + `(runner|atomic-guard)-([a-f0-9]{32})[.]pending$`
	match := regexp.MustCompile(pattern).FindStringSubmatch(args[12])
	if len(match) != 3 {
		return false
	}
	digest := filepath.Base(args[39])
	component := match[1]
	if component == "atomic-guard" {
		component = "guard"
	}
	want, err := boundReusableImageLinkArgs(owner, match[2], component, digest)
	return err == nil && slices.Equal(args, want)
}

func boundReusableImageCleanupArgs(owner int, id, component, digest string) ([]string, error) {
	if _, err := boundReusableImageLinkArgs(owner, id, component, digest); err != nil {
		return nil, err
	}
	name := "runner"
	if component == "guard" {
		name = "atomic-guard"
	}
	source := fmt.Sprintf("/var/lib/fased-app-approved-%d/%s", owner, digest)
	pending := Root(owner) + "/maintenance/." + name + "-" + id + ".pending"
	marker := pending + ".source"
	target := Root(owner) + "/maintenance/" + name
	unit, err := reusableSetupSlotUnit(owner, pending)
	if err != nil {
		return nil, err
	}
	check := []byte(digest + "  " + source + "\n")
	return []string{"/usr/bin/systemd-run", "--quiet", "--wait", "--collect", "--unit=" + unit,
		"--property=Type=oneshot", "--property=StandardInputData=" + base64.StdEncoding.EncodeToString(check),
		"--property=StandardOutput=null", "--property=StandardError=null", "--property=ExecStartPre=/usr/bin/test ! -L " + target,
		"--property=ExecStartPre=/usr/bin/test ! -L " + source, "--property=ExecStartPre=/usr/bin/sha256sum --check --status -- /dev/stdin", "--", "/usr/bin/find", filepath.Dir(pending),
		"-maxdepth", "1", "(", "-name", filepath.Base(pending), "-o", "-name", filepath.Base(marker), ")", "-type", "f", "-uid", "0", "-gid", "0", "-perm", "0755", "-links", "+1", "-samefile", target,
		"-exec", "/usr/bin/cmp", "--silent", "--", "{}", source, ";", "-delete"}, nil
}

func allowedBoundReusableImageCleanup(owner int, args []string) bool {
	if len(args) != 44 {
		return false
	}
	pending := filepath.Join(args[14], args[19])
	pattern := `^` + regexp.QuoteMeta(Root(owner)+"/maintenance/.") + `(runner|atomic-guard)-([a-f0-9]{32})[.]pending$`
	match := regexp.MustCompile(pattern).FindStringSubmatch(pending)
	if len(match) != 3 {
		return false
	}
	component := match[1]
	if component == "atomic-guard" {
		component = "guard"
	}
	want, err := boundReusableImageCleanupArgs(owner, match[2], component, filepath.Base(args[41]))
	return err == nil && slices.Equal(args, want)
}

// BoundApprovedInputWriteArgs admits a small, digest-named root source. The
// exact bytes are in the OS command and an existing source is never rewritten.
func BoundApprovedInputWriteArgs(owner int, data []byte) ([]string, error) {
	if owner <= 0 || len(data) == 0 || len(data) > 16384 {
		return nil, fmt.Errorf("invalid approved input")
	}
	return boundNoClobberWriteArgs(owner, ApprovedExecutablePath(owner, data), data, "0222"), nil
}

func boundNoClobberWriteArgs(owner int, target string, data []byte, umask string) []string {
	sum := sha256.Sum256(append([]byte(target+"\x00"), data...))
	unit := fmt.Sprintf("fased-atomic-write-%d-%x", owner, sum[:8])
	return boundNoClobberWriteArgsWithUnit(target, data, umask, unit)
}

func reusableSetupSlotUnit(owner int, pending string) (string, error) {
	match := regexp.MustCompile(`-([a-f0-9]{32})[.]pending$`).FindStringSubmatch(pending)
	if owner <= 0 || filepath.Clean(pending) != pending || len(match) != 2 {
		return "", fmt.Errorf("invalid reusable setup slot")
	}
	sum := sha256.Sum256([]byte(pending))
	return fmt.Sprintf("fased-setup-slot-%d-%s-%x", owner, match[1], sum[:8]), nil
}

func boundNoClobberWriteArgsWithUnit(target string, data []byte, umask, unit string) []string {
	return []string{"/usr/bin/systemd-run", "--quiet", "--wait", "--collect", "--unit=" + unit,
		"--property=Type=oneshot", "--property=UMask=" + umask, "--property=StandardInputData=" + base64.StdEncoding.EncodeToString(data),
		"--property=StandardOutput=null", "--property=StandardError=null", "--", "/usr/bin/cp", "--no-clobber", "--no-preserve=mode", "--", "/dev/stdin", target}
}

func AllowedBoundAtomicUnitWrite(owner int, data []byte, args []string) bool {
	if len(args) != 17 {
		return false
	}
	want, err := BoundAtomicUnitWriteArgs(owner, args[len(args)-1], data)
	return err == nil && slices.Equal(args, want)
}

func AllowedBoundApprovedInputWrite(owner int, data []byte, args []string) bool {
	want, err := BoundApprovedInputWriteArgs(owner, data)
	return err == nil && slices.Equal(args, want)
}

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
		`^/etc/systemd/system/fased-custody-` + u + `-(app|signer)\.service\.d/\.20-fased-update-guard\.conf-([a-f0-9]{32})\.pending$`,
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
		if i == 5 && string(data) != "[Unit]\nAssertPathExists=!"+Root(owner)+"/update-start-blocked.json\n" {
			return nil, fmt.Errorf("restart assertion content changed")
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
	pattern := `^/etc/sudoers[.]d/(fased-native-` + u + `|[.]fased-remote-proof-` + u + `|[.]fased-custody-` + u + `-reusable)-[a-f0-9]{32}[.]pending$`
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
