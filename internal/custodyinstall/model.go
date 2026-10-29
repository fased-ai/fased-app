// Package custodyinstall manages the explicit disposable Linux custody topology.
// It never runs project code as root or imports/migrates an existing wallet.
package custodyinstall

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
	Core, Signer, Listen, Network string
	PrepareHost, Disposable       bool
	StageOnly                     bool // Enrollment only; never an ordinary CLI option.
	Location, PublicOrigin        string
}
type Identity struct {
	Name string `json:"name"`
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
}
type Manifest struct {
	Enrollment       bool     `json:"enrollment,omitempty"`
	Location         string   `json:"location,omitempty"`
	PublicOrigin     string   `json:"publicOrigin,omitempty"`
	Network          string   `json:"network,omitempty"`
	InspectionBridge string   `json:"inspectionBridge,omitempty"`
	Version          int      `json:"version"`
	Mode             string   `json:"mode"`
	ID               string   `json:"id"`
	Owner            int      `json:"owner"`
	Root             string   `json:"root"`
	Listen           string   `json:"listen"`
	CoreHash         string   `json:"coreHash"`
	SignerHash       string   `json:"signerHash"`
	App              Identity `json:"app"`
	Signer           Identity `json:"signer"`
}
type journal struct {
	Enrollment   bool   `json:"enrollment,omitempty"`
	Location     string `json:"location,omitempty"`
	PublicOrigin string `json:"publicOrigin,omitempty"`
	Network      string `json:"network,omitempty"`
	Version      int    `json:"version"`
	ID           string `json:"id"`
	Owner        int    `json:"owner"`
	CoreHash     string `json:"coreHash"`
	SignerHash   string `json:"signerHash"`
	Listen       string `json:"listen"`
}

func Root(uid int) string                     { return fmt.Sprintf("/var/lib/fased-app-custody-%d", uid) }
func Unit(uid int, role string) string        { return fmt.Sprintf("fased-custody-%d-%s.service", uid, role) }
func accountName(uid int, role string) string { return fmt.Sprintf("fased-fx-%d-%s", uid, role) }
func sum(b []byte) string                     { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func validHex(s string, n int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == n && hex.EncodeToString(b) == s
}
func validListen(s string) bool {
	h, p, e := net.SplitHostPort(s)
	if e != nil || h != "127.0.0.1" {
		return false
	}
	n, e := strconv.Atoi(p)
	return e == nil && n >= 1024 && n <= 65535 && strconv.Itoa(n) == p
}
func (m Manifest) validate() error {
	if !validLocation(m.Location, m.PublicOrigin) || m.Enrollment && m.Location == "" {
		return errors.New("invalid enrolled instance profile")
	}
	if m.InspectionBridge != "" && (m.Version != 1 || !validHex(m.InspectionBridge, 16)) {
		return errors.New("invalid fixture reader bridge")
	}
	if !custodyMode(m.Version, m.Mode, m.Network) || m.Owner <= 0 || m.Root != Root(m.Owner) || !validHex(m.ID, 16) || !validHex(m.CoreHash, 32) || !validHex(m.SignerHash, 32) || !validListen(m.Listen) {
		return errors.New("invalid managed fixture manifest")
	}
	for _, pair := range []struct {
		i    Identity
		role string
	}{{m.App, "app"}, {m.Signer, "signer"}} {
		if pair.i.Name != accountName(m.Owner, pair.role) || pair.i.UID <= 0 || pair.i.GID <= 0 || pair.i.UID == m.Owner {
			return errors.New("invalid independent fixture identity")
		}
	}
	if m.App.UID == m.Signer.UID || m.App.GID == m.Signer.GID {
		return errors.New("app and signer must have independent identities/groups")
	}
	return nil
}
func (j journal) validate(uid int) error {
	if !custodyMode(j.Version, modeFor(j.Network), j.Network) || j.Owner != uid || !validHex(j.ID, 16) || !validHex(j.CoreHash, 32) || !validHex(j.SignerHash, 32) || !validListen(j.Listen) || !validLocation(j.Location, j.PublicOrigin) || j.Enrollment && j.Location == "" {
		return errors.New("invalid installer intent; preserve and investigate it")
	}
	return nil
}
func marker(id, role string) string { return "Fased disposable " + id + " " + role }
func parseAccount(line, name, comment string) (Identity, error) {
	f := strings.Split(strings.TrimSpace(line), ":")
	if len(f) != 7 || f[0] != name || f[4] != comment || f[5] != "/nonexistent" || f[6] != "/usr/sbin/nologin" {
		return Identity{}, errors.New("foreign account identity; preserving it")
	}
	uid, e := strconv.Atoi(f[2])
	if e != nil || uid <= 0 {
		return Identity{}, errors.New("invalid account UID")
	}
	gid, e := strconv.Atoi(f[3])
	if e != nil || gid <= 0 {
		return Identity{}, errors.New("invalid account GID")
	}
	return Identity{Name: name, UID: uid, GID: gid}, nil
}

// Numeric identities and fixed canonical paths avoid shell/unit interpolation.
func definitions(m Manifest) map[string][]byte {
	if m.InspectionBridge != "" {
		return fixtureUnits(m, false)
	}
	shared := "\nRestart=on-failure\nRestartSec=2\nTimeoutStopSec=15\nUMask=0077\nNoNewPrivileges=yes\nProtectSystem=strict\nProtectHome=yes\nPrivateTmp=yes\nProtectKernelTunables=yes\nProtectKernelModules=yes\nProtectControlGroups=yes\nRestrictSUIDSGID=yes\nCapabilityBoundingSet=\nAmbientCapabilities=\nWorkingDirectory=/\nEnvironment=HOME=/nonexistent\n"
	unit := func(i Identity, exec, write, addresses string) []byte {
		return []byte(fmt.Sprintf("[Unit]\nDescription=Fased disposable custody fixture\n\n[Service]\nType=simple\nUser=%d\nGroup=%d\nExecStart=%s%sReadWritePaths=%s\nRestrictAddressFamilies=%s\n\n[Install]\nWantedBy=multi-user.target\n", i.UID, i.GID, exec, shared, write, addresses))
	}
	profile := ""
	if m.Location != "" {
		profile = " --instance-location " + m.Location
	}
	if m.PublicOrigin != "" {
		profile += " --public-origin " + m.PublicOrigin
	}
	return map[string][]byte{
		"fased-app.service":    unit(m.App, m.Root+"/fased-app serve --data-dir "+m.Root+"/app-state --listen "+m.Listen+" --require-sandbox --installation-root "+m.Root+" --custody-config "+m.Root+"/identity.json"+profile, m.Root+"/app-state", "AF_UNIX AF_INET AF_INET6"),
		"fased-signer.service": unit(m.Signer, m.Root+"/fased-signer serve --config "+m.Root+"/identity.json --disposable-fixture", m.Root+"/signer-state "+m.Root+"/signer-ipc", "AF_UNIX"),
	}
}

func validLocation(location, origin string) bool {
	if location == "" || location == "local" {
		return origin == ""
	}
	if location != "remote" {
		return false
	}
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && !u.ForceQuery && u.String() == origin && !strings.ContainsAny(origin, "\r\n\t %$\\\"")
}

func modeFor(network string) string {
	if network == "" {
		return "disposable-fixture:v1"
	}
	return "disposable-custody:v2"
}
func custodyMode(version int, mode, network string) bool {
	return version == 1 && mode == "disposable-fixture:v1" && network == "" || version == 2 && mode == "disposable-custody:v2" && (network == "devnet" || network == "mainnet-beta")
}
