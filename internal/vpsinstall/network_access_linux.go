//go:build linux

package vpsinstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"golang.org/x/sys/unix"
)

// networkProof is a lockout guard, not a grant of administrative authority.
// SSH/Tailscale authenticate the administrator; sudo authorizes OS changes.
// There are no environment switches that substitute a fixture for this proof.
func networkProof(ctx context.Context, m Manifest) (networkAccess, error) {
	var empty networkAccess
	if m.PublicOrigin == "" {
		return empty, errors.New("prepare Tailscale HTTPS and owner sign-in before network lockdown; SSH-only access is preserved")
	}
	// Tailscale SSH uses its own authorization rather than sshd policy. This
	// operation supports a direct OpenSSH session; do not silently accept another
	// login implementation or a detached tmux service as that proof.
	main, err := liveOps(nil).command(ctx, "/usr/bin/systemctl", "show", "--property=MainPID", "--value", "ssh.service")
	sshPID, parseErr := strconv.Atoi(strings.TrimSpace(string(main)))
	if err != nil || parseErr != nil || !hasSSHAncestor(os.Getpid(), sshPID, procParent) {
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
	if err = trustedTool(liveOps(nil), "/usr/bin/tailscale"); err != nil {
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
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 3 * time.Second, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("private instance redirects are refused")
	}}
	identity, err := checkPrivateApplication(ctx, m, client)
	if err != nil {
		return empty, err
	}
	// Reuse the private access inventory guard; HTTPS alone does not exclude Funnel.
	access := privateaccess.System()
	access.Client = client
	if err = access.Verify(ctx, privateaccess.Plan{Mode: "tailscale", Origin: m.PublicOrigin, Target: "http://" + m.Listen}, identity); err != nil {
		return empty, err
	}
	return p, nil
}

func procParent(pid int) (int, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/stat", pid))
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
	if len(data) > 1024*1024 || json.Unmarshal(data, &status) != nil || status.BackendState != "Running" || !status.Self.Online || "https://"+strings.TrimSuffix(status.Self.DNSName, ".") != origin {
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

func checkPrivateApplication(ctx context.Context, m Manifest, client *http.Client) (string, error) {
	expected := ""
	if m.Data != "" && m.OwnerUID > 0 {
		o := liveOps(nil)
		if checkDirectory(o, m.Data, m.AppUID, m.AppGID, 0700) != nil {
			f, err := committedEnrollment(o, m)
			if err != nil {
				return "", errors.New("completed same-instance enrollment is required for private application proof")
			}
			expected = f.Source.ExpectedInstanceID
		}
	}
	return checkPrivateApplicationBound(ctx, m, client, expected)
}
func checkPrivateApplicationBound(ctx context.Context, m Manifest, client *http.Client, expectedInstance string) (string, error) {
	health := func(origin, expected string) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/v1/health", nil)
		if err != nil {
			return "", err
		}
		response, err := client.Do(req)
		if err != nil {
			return "", errors.New("private Fased health/TLS could not be verified; retain SSH and console access")
		}
		defer response.Body.Close()
		h, err := connection.ReadHealth(response, expected)
		if err != nil || h.Connection == nil || h.Connection.Location != "remote" || h.Connection.OwnerUID != m.OwnerUID {
			return "", errors.New("private HTTPS must reach this installed Remote instance")
		}
		return h.InstanceID, nil
	}
	identity, err := health("http://"+m.Listen, expectedInstance)
	if err != nil {
		return "", err
	}
	if _, err = health(m.PublicOrigin, identity); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.PublicOrigin+"/api/v1/auth/state", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("cannot verify private owner setup")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	var state struct {
		Claimed bool `json:"claimed"`
	}
	if err != nil || len(body) > 16384 || resp.StatusCode != http.StatusOK || json.Unmarshal(body, &state) != nil || !state.Claimed {
		return "", fmt.Errorf("finish owner setup and sign in at %s before lockdown", m.PublicOrigin)
	}
	return identity, nil
}
