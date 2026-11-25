//go:build linux

package vpsnetwork

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/tailnet"
	"golang.org/x/sys/unix"
)

func activeSSHDaemonPID(ctx context.Context) (int, error) {
	ops := liveOps(nil)
	units := []string{"ssh.service", "sshd.service"}
	if p, err := hostplatform.Current(); err == nil && sshServiceForPlatform(p) == "sshd.service" {
		units = []string{"sshd.service", "ssh.service"}
	}
	for _, unit := range units {
		main, err := ops.command(ctx, "/usr/bin/systemctl", "show", "--property=MainPID", "--value", unit)
		if err != nil {
			continue
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(main)))
		if parseErr == nil && pid > 1 {
			return pid, nil
		}
	}
	return 0, errors.New("no active OpenSSH daemon MainPID found")
}

// networkProofWithSystem verifies that the current invocation runs inside an active
// direct OpenSSH session over tailscale0 matching the Format-4 instance's private HTTPS endpoint.
func networkProofWithSystem(ctx context.Context, m Manifest, system *custodyinstall.System) (networkAccess, error) {
	var empty networkAccess
	if m.PublicOrigin == "" {
		return empty, errors.New("prepare Tailscale HTTPS and owner sign-in before network lockdown; SSH-only access is preserved")
	}
	sshPID, err := activeSSHDaemonPID(ctx)
	if err != nil || !hasSSHAncestor(os.Getpid(), sshPID, procParent) {
		return empty, errors.New("open a direct OpenSSH connection through Tailscale; detached shells and Tailscale SSH cannot verify sshd policy")
	}
	iface, err := net.InterfaceByName("tailscale0")
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return empty, errors.New("network lockdown requires the active tailscale0 interface")
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return empty, err
	}
	if err = trustedTool(fileOps(), "/usr/bin/tailscale"); err != nil {
		return empty, errors.New("install and enroll the official Tailscale client before network lockdown")
	}
	data, err := exec.CommandContext(ctx, "/usr/bin/tailscale", "status", "--json").Output()
	if err != nil || len(data) > 1024*1024 {
		return empty, errors.New("cannot verify the current Tailscale device")
	}
	sid, err := unix.Getsid(0)
	if err != nil {
		return empty, err
	}
	p, err := checkNetworkSession(os.Getenv("SSH_CONNECTION"), sid, addrs, data, m.PublicOrigin)
	if err != nil {
		return empty, err
	}

	transport := &http.Transport{
		Proxy:                 nil,
		TLSHandshakeTimeout:   3 * time.Second,
		ResponseHeaderTimeout: 3 * time.Second,
		DisableCompression:    true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("private instance redirects are refused")
		},
	}

	pm, err := verifyPackageInstallation(m)
	if err != nil {
		return empty, err
	}
	commit, err := custodyinstall.ReadEnrollmentCommit(pm)
	if err != nil {
		return empty, errors.New("completed same-instance package enrollment is required")
	}
	identity, err := checkPackagePrivateLocalApplication(ctx, m, commit.InstanceID, client)
	if err != nil {
		return empty, err
	}
	socket, err := tailnet.ProtectedSocket("package-"+m.ID, m.OwnerUID)
	if err != nil {
		return empty, err
	}
	access := privateaccess.System()
	access.Client = client
	if system != nil {
		peer, wErr := tailnet.WhoIs(ctx, p.PeerIP)
		if wErr != nil {
			return empty, wErr
		}
		access.ExpectedPeer = &peer
		access.ProofOut = system.Out
		access.Proof = func(ctx context.Context, request core.PrivateAccessProofRequest) (core.PrivateAccessProof, error) {
			return system.PrivateAccessProof(ctx, pm, request)
		}
	}
	if err = access.Verify(ctx, privateaccess.Plan{Mode: "tailscale", Origin: m.PublicOrigin, Target: "unix:" + socket}, identity); err != nil {
		return empty, err
	}
	return p, nil
}

func checkPackagePrivateLocalApplication(ctx context.Context, m Manifest, expected string, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+m.Listen+"/api/v1/health", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	h, err := connection.ReadHealth(response, expected)
	if err != nil || h.Connection == nil || h.Connection.Location != "remote" || h.Connection.OwnerUID != m.OwnerUID || !h.Connection.RemoteReady {
		return "", errors.New("finish private owner/device setup before lockdown")
	}
	return h.InstanceID, nil
}

func procParent(pid int) (int, error) {
	f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	end := strings.LastIndexByte(string(b), ')')
	if err != nil || len(b) > 4096 || end < 0 {
		return 0, errors.New("cannot verify SSH process ancestry")
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) < 2 {
		return 0, errors.New("invalid SSH process ancestry")
	}
	return strconv.Atoi(fields[1])
}

func hasSSHAncestor(pid, main int, parent func(int) (int, error)) bool {
	if pid <= 1 || main <= 1 {
		return false
	}
	seen := map[int]bool{}
	for count := 0; count < 64 && pid > 1; count++ {
		if pid == main {
			return true
		}
		if seen[pid] {
			return false
		}
		seen[pid] = true
		next, err := parent(pid)
		if err != nil || next <= 0 {
			return false
		}
		pid = next
	}
	return false
}

func tailnetIP(ip netip.Addr) bool {
	return netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(ip)
}

func checkNetworkSession(value string, sid int, addrs []net.Addr, data []byte, origin string) (networkAccess, error) {
	var empty networkAccess
	parts := strings.Fields(value)
	if len(parts) != 4 || sid <= 0 {
		return empty, errors.New("open an ordinary SSH session to this VPS over Tailscale first")
	}
	peer, e1 := netip.ParseAddr(parts[0])
	server, e2 := netip.ParseAddr(parts[2])
	clientPort, e3 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || e3 != nil || clientPort < 1 || clientPort > 65535 || parts[3] != "22" || !tailnetIP(peer) || !tailnetIP(server) || peer == server {
		return empty, errors.New("use OpenSSH on port 22 through the VPS Tailscale address; public SSH cannot approve lockdown")
	}
	var status struct {
		BackendState string
		Self         struct {
			Online       bool
			DNSName      string
			TailscaleIPs []string
		}
	}
	originHost, _, originErr := tailnet.Origin(origin)
	if len(data) > 1024*1024 || json.Unmarshal(data, &status) != nil || status.BackendState != "Running" || !status.Self.Online || originErr != nil || strings.TrimSuffix(status.Self.DNSName, ".") != originHost {
		return empty, errors.New("the connected Tailscale device must match the installed private HTTPS address")
	}
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	if !strings.HasSuffix(dns, ".ts.net") || strings.ContainsAny(dns, "/?#@ :%\r\n\t") {
		return empty, errors.New("invalid Tailscale HTTPS identity")
	}
	inStatus, onInterface := false, false
	for _, raw := range status.Self.TailscaleIPs {
		ip, err := netip.ParseAddr(raw)
		inStatus = inStatus || (err == nil && ip == server)
	}
	for _, addr := range addrs {
		prefix, err := netip.ParsePrefix(addr.String())
		onInterface = onInterface || (err == nil && prefix.Addr() == server)
	}
	if !inStatus || !onInterface {
		return empty, errors.New("the SSH destination must be this device's active Tailscale address")
	}
	// A new process/session (setsid, tmux) is not another SSH connection. Bind the
	// entire TCP tuple, including source port; multiplexed SSH channels share it.
	session := net.JoinHostPort(peer.String(), strconv.Itoa(clientPort)) + "->" + net.JoinHostPort(server.String(), "22")
	return networkAccess{SessionID: session, PeerIP: peer.String(), ServerIP: server.String(), SSHPort: 22}, nil
}
