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

	"github.com/fased-ai/fased-app/internal/update"
)

type Options struct {
	NativeOwnerAccess             bool
	Core, Signer, Listen, Network string
	PrepareHost, Disposable       bool
	Mode, Profile, Provenance     string
	StageOnly                     bool // Enrollment only; never an ordinary CLI option.
	Location, PublicOrigin        string
}
type Identity struct {
	Name string `json:"name"`
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
}

const PackageLayout = "linux-package-v1"

type Manifest struct {
	Layout            string   `json:"layout,omitempty"`
	NativeOwnerAccess bool     `json:"nativeOwnerAccess,omitempty"`
	Profile           string   `json:"profile,omitempty"`
	Provenance        string   `json:"provenance,omitempty"`
	Enrollment        bool     `json:"enrollment,omitempty"`
	Location          string   `json:"location,omitempty"`
	PublicOrigin      string   `json:"publicOrigin,omitempty"`
	Network           string   `json:"network,omitempty"`
	InspectionBridge  string   `json:"inspectionBridge,omitempty"`
	Version           int      `json:"version"`
	Mode              string   `json:"mode"`
	ID                string   `json:"id"`
	Owner             int      `json:"owner"`
	Root              string   `json:"root"`
	Listen            string   `json:"listen"`
	CoreHash          string   `json:"coreHash"`
	SignerHash        string   `json:"signerHash"`
	App               Identity `json:"app"`
	Signer            Identity `json:"signer"`
}
type journal struct {
	NativeOwnerAccess bool   `json:"nativeOwnerAccess,omitempty"`
	Mode              string `json:"mode,omitempty"`
	Profile           string `json:"profile,omitempty"`
	Provenance        string `json:"provenance,omitempty"`
	Enrollment        bool   `json:"enrollment,omitempty"`
	Location          string `json:"location,omitempty"`
	PublicOrigin      string `json:"publicOrigin,omitempty"`
	Network           string `json:"network,omitempty"`
	Version           int    `json:"version"`
	ID                string `json:"id"`
	Owner             int    `json:"owner"`
	CoreHash          string `json:"coreHash"`
	SignerHash        string `json:"signerHash"`
	Listen            string `json:"listen"`
}

func Root(uid int) string                     { return fmt.Sprintf("/var/lib/fased-app-custody-%d", uid) }
func Unit(uid int, role string) string        { return fmt.Sprintf("fased-custody-%d-%s.service", uid, role) }
func accountName(uid int, role string) string { return fmt.Sprintf("fased-fx-%d-%s", uid, role) }

// PackageRoot is disjoint from the retained owner-wide custody installation.
// The random installation identity prevents a recreated profile adopting state.
func PackageRoot(uid int, location, id string) (string, error) {
	return update.PackageInstanceRoot(uid, location, id)
}

func packageAccountName(uid int, location, id, role string) string {
	short := "a"
	if role == "signer" {
		short = "s"
	}
	// Linux account names are limited to 32 bytes. A collision is refused by
	// the exact installation identity in the protected account comment.
	identity := sum([]byte(fmt.Sprintf("%d/%s/%s", uid, location, id)))[:24]
	return "fs-" + identity + "-" + short
}

func instanceUnit(m Manifest, role string) string {
	if m.Layout == "" {
		return Unit(m.Owner, role)
	}
	if m.Layout != PackageLayout {
		return ""
	}
	unit, err := update.PackageInstanceUnit(m.Owner, m.Location, m.ID, role)
	if err != nil {
		return ""
	}
	return unit
}
func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
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
	if m.Layout != "" {
		return errors.New("package instance requires its own controller")
	}
	if m.NativeOwnerAccess && (m.Version != 3 || !m.Enrollment || m.Location != "local") {
		return errors.New("native owner access requires Local enrolled ordinary custody")
	}
	if !validLocation(m.Location, m.PublicOrigin) || m.Enrollment && m.Location == "" {
		return errors.New("invalid enrolled instance profile")
	}
	if m.InspectionBridge != "" && (m.Version != 1 || !validHex(m.InspectionBridge, 16)) {
		return errors.New("invalid fixture reader bridge")
	}
	if !custodyContext(m.Version, m.Profile, m.Provenance) || m.Version == 3 && !m.Enrollment || !custodyMode(m.Version, m.Mode, m.Network) || m.Owner <= 0 || m.Root != Root(m.Owner) || !validHex(m.ID, 16) || !validHex(m.CoreHash, 32) || !validHex(m.SignerHash, 32) || !validListen(m.Listen) {
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

// validatePackage accepts only the new isolated topology. Legacy maintenance
// callers use validate instead and cannot operate on this layout.
func (m Manifest) validatePackage() error {
	root, err := PackageRoot(m.Owner, m.Location, m.ID)
	if err != nil || m.Layout != PackageLayout || m.Root != root || m.Version != 3 ||
		m.Mode != "ordinary-custody:v1" || m.Profile != "owner-authorized:v1" || m.Provenance != "owner-input:v1" ||
		!m.Enrollment || m.NativeOwnerAccess || m.InspectionBridge != "" ||
		!validLocation(m.Location, m.PublicOrigin) || m.Location == "remote" && m.PublicOrigin == "" ||
		(m.Network != "" && m.Network != "devnet" && m.Network != "mainnet-beta") ||
		!validHex(m.CoreHash, 32) || !validHex(m.SignerHash, 32) || !validListen(m.Listen) {
		return errors.New("invalid isolated package instance")
	}
	for _, pair := range []struct {
		i    Identity
		role string
	}{{m.App, "app"}, {m.Signer, "signer"}} {
		if pair.i.Name != packageAccountName(m.Owner, m.Location, m.ID, pair.role) || pair.i.UID <= 0 || pair.i.GID <= 0 || pair.i.UID == m.Owner {
			return errors.New("invalid isolated package identity")
		}
	}
	if m.App.UID == m.Signer.UID || m.App.GID == m.Signer.GID {
		return errors.New("app and signer identities must be separate")
	}
	return nil
}
func (j journal) validate(uid int) error {
	if j.NativeOwnerAccess && (j.Version != 3 || !j.Enrollment || j.Location != "local") {
		return errors.New("invalid native owner enrollment")
	}
	if !custodyContext(j.Version, j.Profile, j.Provenance) || j.Version == 3 && !j.Enrollment || !custodyMode(j.Version, j.mode(), j.Network) || j.Owner != uid || !validHex(j.ID, 16) || !validHex(j.CoreHash, 32) || !validHex(j.SignerHash, 32) || !validListen(j.Listen) || !validLocation(j.Location, j.PublicOrigin) || j.Enrollment && j.Location == "" {
		return errors.New("invalid installer intent; preserve and investigate it")
	}
	return nil
}
func marker(id, role string) string { return "Fased disposable " + id + " " + role }
func instanceMarker(m Manifest, role string) string {
	if m.Layout == PackageLayout {
		return "Fased " + m.ID + " " + role
	}
	return marker(m.ID, role)
}
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
	unit := func(role string, i Identity, exec, write, addresses string) []byte {
		description := "Fased disposable custody fixture"
		service := shared
		install := "\n[Install]\nWantedBy=multi-user.target\n"
		if m.Layout == PackageLayout {
			description = "Fased " + role
			if role == "signer" && m.Network == "" {
				// A fresh installation has no wallet/network yet. Never make its
				// deliberately unavailable signer restart repeatedly at boot.
				service = strings.Replace(service, "Restart=on-failure", "Restart=no", 1)
				install = ""
			}
		}
		return []byte(fmt.Sprintf("[Unit]\nDescription=%s\n\n[Service]\nType=simple\nUser=%d\nGroup=%d\nExecStart=%s%sReadWritePaths=%s\nRestrictAddressFamilies=%s\n%s", description, i.UID, i.GID, exec, service, write, addresses, install))
	}
	profile := ""
	if m.Location != "" {
		profile = " --instance-location " + m.Location
	}
	if m.PublicOrigin != "" {
		profile += " --public-origin " + m.PublicOrigin
	}
	return map[string][]byte{
		"fased-app.service":    unit("application", m.App, m.Root+"/fased-app serve --data-dir "+m.Root+"/app-state --listen "+m.Listen+" --require-sandbox --installation-root "+m.Root+" --custody-config "+m.Root+"/identity.json"+profile, m.Root+"/app-state", "AF_UNIX AF_INET AF_INET6"),
		"fased-signer.service": unit("signer", m.Signer, m.Root+"/fased-signer serve --config "+m.Root+"/identity.json "+signerModeFlag(m), m.Root+"/signer-state "+m.Root+"/signer-ipc", "AF_UNIX"),
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
	return version == 1 && mode == "disposable-fixture:v1" && network == "" || (version == 2 && mode == "disposable-custody:v2" || version == 3 && mode == "ordinary-custody:v1") && (network == "devnet" || network == "mainnet-beta")
}

func custodyContext(version int, profile, provenance string) bool {
	return version != 3 && profile == "" && provenance == "" || version == 3 && profile == "owner-authorized:v1" && (provenance == "controlled-disposable:v1" || provenance == "owner-input:v1")
}
func (j journal) mode() string {
	if j.Mode != "" {
		return j.Mode
	}
	return modeFor(j.Network)
}
func signerModeFlag(m Manifest) string {
	if m.Version == 3 && m.Provenance == "owner-input:v1" {
		return "--ordinary-custody"
	}
	return "--disposable-fixture"
}
