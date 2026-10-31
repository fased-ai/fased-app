package vpsinstall

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var ErrApplicationMaintenanceAuthority = errors.New("Remote maintenance authority is not prepared; use vps updates prepare in the administrator terminal")

type applicationMaintenanceGrant struct {
	Version                              int    `json:"version"`
	InstallationID                       string `json:"installationId"`
	OwnerUID, OwnerGID, AppUID, AppGID   int
	Root, Data, Config, Unit, AppUser    string
	OwnerControl, OwnerHome, ImageSHA256 string
}

func maintenanceRoot(owner int) string {
	return fmt.Sprintf("/var/lib/fased-app-vps-maintenance-%d", owner)
}
func maintenanceImagePath(owner int) string { return maintenanceRoot(owner) + "/fased-app-maintenance" }
func maintenanceTemplateName(owner int) string {
	return fmt.Sprintf("fased-vps-maintenance-%d@.service", owner)
}
func maintenanceUnit(owner int, id string) string {
	return strings.Replace(maintenanceTemplateName(owner), "@.", "@"+id+".", 1)
}
func maintenanceTemplatePath(owner int) string {
	return "/etc/systemd/system/" + maintenanceTemplateName(owner)
}
func maintenanceSudoPath(owner int) string {
	return fmt.Sprintf("/etc/sudoers.d/fased-vps-%d-updates", owner)
}
func maintenancePolkitPath(owner int) string {
	return fmt.Sprintf("/etc/polkit-1/rules.d/60-fased-vps-%d-updates.rules", owner)
}
func (g applicationMaintenanceGrant) matches(m Manifest) bool {
	return g.Version == 1 && g.InstallationID == m.ID && g.OwnerUID == m.OwnerUID && g.OwnerGID == m.OwnerGID && g.AppUID == m.AppUID && g.AppGID == m.AppGID && g.Root == m.Root && g.Data == m.Data && g.Config == m.Config && g.Unit == m.Unit && g.AppUser == m.AppUser && canonicalHex(g.ImageSHA256, 32)
}

type maintenanceRule struct{ tool, args string }

// Only the ordinary installation maintainer receives these OS-tool grants.
// Imports read stdin, never caller-selected root files; fixed destinations cannot
// be units, sudo policy, keys or another installation. No project code runs root.
// POSIX ERE argument matching requires sudo>=1.9.10 (Ubuntu24.04 supports it).
func maintenanceRules(m Manifest) []maintenanceRule {
	q := regexp.QuoteMeta
	id := `[0-9a-f]{32}`
	dir := q(m.Root) + `-update-` + id
	names := `(next-core|previous-core|previous-install[.]json|next-install[.]json|previous-intent[.]json|next-intent[.]json|owner-helper[.]json|replacement-fased-app|replacement-install[.]json|replacement-intent[.]json)`
	pending := dir + `/[.]` + names + `-` + id + `[.]pending`
	helperImage := `(` + q(m.Core) + `|` + dir + `/(previous-core|next-core))`
	helper := `--reuid ` + strconv.Itoa(m.AppUID) + ` --regid ` + strconv.Itoa(m.AppGID) + ` --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- /usr/bin/env --ignore-environment --chdir=/ HOME=/nonexistent PATH=/usr/bin:/bin `
	actor := q(strings.Split(maintenanceTemplateName(m.OwnerUID), "@")[0]) + `@` + id + `[.]service`
	policies := `(` + q(maintenanceSudoPath(m.OwnerUID)) + `|` + q(maintenancePolkitPath(m.OwnerUID)) + `)`
	rules := []maintenanceRule{
		{"/usr/bin/install", `-d -m 755 -o 0 -g 0 -- ` + dir},
		{"/usr/bin/install", `-m (644|755) -o root -g root -- /dev/stdin ` + pending},
		{"/usr/bin/ln", `--no-target-directory -- ` + pending + ` ` + dir + `/` + names},
		{"/usr/bin/rm", `-- ` + pending},
		{"/usr/bin/sync", `-f (` + q(m.Root) + `|` + dir + `|` + pending + `|` + dir + `/` + names + `)`},
		{"/usr/bin/passwd", `-S ` + q(m.AppUser)},
		{"/usr/bin/setpriv", `--version`},
		{"/usr/bin/setpriv", helper + helperImage + ` application-owner-client --config ` + q(m.Config)},
		{"/usr/bin/setpriv", helper + q(m.Core) + ` update-inventory --data-dir ` + q(m.Data) + ` --probe-id ` + id},
		{"/usr/bin/setpriv", helper + q(m.Core) + ` login --data-dir ` + q(m.Data)},
		{"/usr/bin/setpriv", helper + q(m.Core) + ` status --data-dir ` + q(m.Data)},
		// The existing managed Remote Start preserves its autostart contract by
		// enabling this already installed immutable unit before starting it.
		{"/usr/bin/systemctl", `(start|stop|enable) -- ` + q(m.Unit)},
		{"/usr/bin/systemctl", `(enable|disable|reset-failed) -- ` + actor},
		{"/usr/bin/cat", `-- ` + policies},
	}
	for _, p := range []string{maintenanceSudoPath(m.OwnerUID), maintenancePolkitPath(m.OwnerUID)} {
		rules = append(rules, maintenanceRule{"/usr/bin/find", q(p[:strings.LastIndex(p, "/")]) + ` -maxdepth 1 -name ` + q(p[strings.LastIndex(p, "/")+1:]) + ` -printf %U:%G:%m:%y`})
	}
	for _, name := range []string{"fased-app", "install.json", "intent.json"} {
		rules = append(rules, maintenanceRule{"/usr/bin/mv", `--force --no-target-directory -- ` + dir + `/replacement-` + q(name) + ` ` + q(m.Root+"/"+name)})
	}
	for i := range rules {
		rules[i].args = "^" + rules[i].args + "$"
	}
	return rules
}
func maintenanceSudoers(m Manifest) []byte {
	var b strings.Builder
	b.WriteString("# Fased: fixed app-maintenance authority; no runtime/signer sudo grant.\n")
	for _, r := range maintenanceRules(m) {
		fmt.Fprintf(&b, "#%d ALL=(root) NOPASSWD: %s %s\n", m.OwnerUID, r.tool, r.args)
	}
	return []byte(b.String())
}
func maintenancePolkit(m Manifest) []byte {
	// Transient creation and unit-file management receive no grant. Ubuntu255 sends
	// both unit and verb for manage-units; unknown details fail closed.
	return []byte(fmt.Sprintf("polkit.addRule(function(action, subject) {\n  if (subject.user === %q && action.id === 'org.freedesktop.systemd1.manage-units' && action.lookup('verb') === 'start' && /^fased-vps-maintenance-%d@[0-9a-f]{32}\\.service$/.test(action.lookup('unit') || '')) return polkit.Result.YES;\n});\n", m.AppUser, m.OwnerUID))
}
func maintenanceTemplate(g applicationMaintenanceGrant) []byte {
	// The ordinary owner starts with no capabilities. Only sudo's fixed OS tools
	// may gain these; setpriv drops every capability before project code executes.
	return []byte(fmt.Sprintf("[Unit]\nDescription=Fased Remote update maintenance\nAfter=network-online.target polkit.service\n\n[Service]\nType=oneshot\nUser=%d\nGroup=%d\nExecStart=%s maintain-update --remote --unattended --operation %%i\nTimeoutStartSec=15min\nRestart=no\nRemainAfterExit=yes\nUMask=0077\nWorkingDirectory=/\nEnvironment=HOME=%s\nEnvironment=XDG_CONFIG_HOME=%s\nNoNewPrivileges=no\nCapabilityBoundingSet=CAP_CHOWN CAP_DAC_OVERRIDE CAP_FOWNER CAP_SETGID CAP_SETUID CAP_SETPCAP CAP_AUDIT_WRITE\nAmbientCapabilities=\nPrivateTmp=yes\nProtectSystem=strict\nProtectHome=read-only\nReadWritePaths=/var/lib /run /etc/systemd/system/multi-user.target.wants %s\nProtectKernelTunables=yes\nProtectKernelModules=yes\nProtectControlGroups=yes\nLockPersonality=yes\n\n[Install]\nWantedBy=multi-user.target\n", g.OwnerUID, g.OwnerGID, maintenanceImagePath(g.OwnerUID), g.OwnerHome, strings.TrimSuffix(g.OwnerControl, "/fased-app-vps-installer"), g.OwnerControl))
}
