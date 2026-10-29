// Package vpsinstall owns the app-only Ubuntu Remote system service. It adds no
// signer, wallet, provider credential, public listener or financial permission.
package vpsinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Options struct {
	Core, CoreSHA256, Listen, PublicOrigin string
	PrepareHost, Start                     bool
}

// Manifest is published once as root-owned configuration. Lifecycle progress
// belongs to a separate owner-private journal; application state never owns it.
type Manifest struct {
	Version      int    `json:"version"`
	ID           string `json:"id"`
	Phase        string `json:"phase"`
	OwnerUID     int    `json:"ownerUid"`
	OwnerGID     int    `json:"ownerGid"`
	AppUID       int    `json:"appUid"`
	AppGID       int    `json:"appGid"`
	AppUser      string `json:"appUser"`
	Root         string `json:"root"`
	Data         string `json:"data"`
	Core         string `json:"core"`
	Config       string `json:"config"`
	Unit         string `json:"unit"`
	UnitPath     string `json:"unitPath"`
	Listen       string `json:"listen"`
	PublicOrigin string `json:"publicOrigin"`
	CoreSHA256   string `json:"coreSha256"`
}

type ServiceStatus struct {
	Installed  bool   `json:"installed"`
	Active     bool   `json:"active"`
	Unit       string `json:"unit"`
	State      string `json:"state"`
	InstanceID string `json:"instanceId,omitempty"`
	URL        string `json:"url,omitempty"`
}

func Root(owner int) string        { return fmt.Sprintf("/var/lib/fased-app-vps-%d", owner) }
func ConfigPath(owner int) string  { return Root(owner) + "/install.json" }
func accountName(owner int) string { return fmt.Sprintf("fased-vps-%d", owner) }
func unitName(owner int) string    { return accountName(owner) + ".service" }
func hash(b []byte) string         { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func canonicalHex(s string, size int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == size && hex.EncodeToString(b) == s
}
func validateEndpoint(listen, origin string) error {
	h, p, e := net.SplitHostPort(listen)
	n, ne := strconv.Atoi(p)
	if e != nil || h != "127.0.0.1" || ne != nil || n < 1024 || n > 65535 || strconv.Itoa(n) != p {
		return errors.New("Remote requires 127.0.0.1 and an explicit unprivileged port")
	}
	if origin == "" {
		return nil
	}
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.String() != origin || strings.ContainsAny(origin, "\r\n\t %$\\\"") {
		return errors.New("exact private HTTPS origin required")
	}
	return nil
}
func (m Manifest) validate() error {
	if m.Version != 1 || m.Phase != "installed" || !canonicalHex(m.ID, 16) || !canonicalHex(m.CoreSHA256, 32) || m.OwnerUID <= 0 || m.OwnerGID <= 0 || m.AppUID <= 0 || m.AppGID <= 0 || m.AppUID == m.OwnerUID || m.AppGID == m.OwnerGID || m.AppUser != accountName(m.OwnerUID) || m.Root != Root(m.OwnerUID) || m.Data != m.Root+"/app-state" || m.Core != m.Root+"/fased-app" || m.Config != ConfigPath(m.OwnerUID) || m.Unit != unitName(m.OwnerUID) || m.UnitPath != "/etc/systemd/system/"+m.Unit {
		return errors.New("invalid independent Remote installation identity")
	}
	return validateEndpoint(m.Listen, m.PublicOrigin)
}

func unit(m Manifest) []byte {
	command := m.Core + " serve --data-dir " + m.Data + " --listen " + m.Listen + " --instance-location remote --require-sandbox --installation-root " + m.Root + " --remote-config " + m.Config
	if m.PublicOrigin != "" {
		command += " --public-origin " + m.PublicOrigin
	}
	return []byte(fmt.Sprintf("[Unit]\nDescription=Fased App Remote core\nAfter=network.target\n\n[Service]\nType=simple\nUser=%d\nGroup=%d\nSupplementaryGroups=\nExecStart=%s\nRestart=on-failure\nRestartSec=3\nTimeoutStopSec=15\nUMask=0077\nNoNewPrivileges=yes\nProtectSystem=strict\nProtectHome=yes\nPrivateTmp=yes\nReadOnlyPaths=%s\nReadWritePaths=%s\nProtectKernelTunables=yes\nProtectKernelModules=yes\nProtectControlGroups=yes\nCapabilityBoundingSet=\nAmbientCapabilities=\nRestrictSUIDSGID=yes\nLockPersonality=yes\nRestrictAddressFamilies=AF_UNIX AF_INET AF_INET6\nWorkingDirectory=/\nEnvironment=HOME=/nonexistent\n\n[Install]\nWantedBy=multi-user.target\n", m.AppUID, m.AppGID, command, m.Root, m.Data))
}

func helperArgs(m Manifest, action string) []string {
	return []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(m.AppUID), "--regid", strconv.Itoa(m.AppGID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", m.Core, action, "--data-dir", m.Data}
}
