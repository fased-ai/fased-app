package netguard

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
)

func TestIsPublicUnicastIPBlocksSpecialAndTransitionRanges(t *testing.T) {
	t.Parallel()
	blocked := []string{
		"0.0.0.0",
		"0.1.2.3",
		"10.0.0.1",
		"100.64.0.1",
		"100.100.100.200", // Alibaba IMDS
		"127.0.0.1",
		"169.254.169.254", // AWS/GCP/Azure IMDS
		"172.16.0.1",
		"192.0.0.1",
		"192.0.2.1",
		"192.168.1.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"224.0.0.1",
		"240.0.0.1",
		"255.255.255.255",
		"::",
		"::1",
		"::ffff:127.0.0.1",
		"::ffff:169.254.169.254",
		"::ffff:100.100.100.200",
		"64:ff9b::169.254.169.254", // NAT64 well-known
		"64:ff9b:1::10.0.0.1",      // NAT64 local-use
		"100::1",                   // Discard-only
		"2001::1",                  // Teredo
		"2001:db8::1",              // Documentation
		"2002:a9fe:a9fe::1",        // 6to4
		"fc00::1",                  // ULA
		"fd7a:115c:a1e0::1",        // Tailscale ULA
		"fe80::1",                  // Link-local
		"ff02::1",                  // Multicast
		"4000::1",                  // Non-2000::/3 global
	}
	for _, s := range blocked {
		ip := netip.MustParseAddr(s)
		if IsPublicUnicastIP(ip) {
			t.Fatalf("expected %s to be blocked as non-public", s)
		}
	}
	allowed := []string{
		"1.1.1.1",
		"8.8.8.8",
		"93.184.216.34",
		"2606:4700:4700::1111",
		"2001:4860:4860::8888",
	}
	for _, s := range allowed {
		ip := netip.MustParseAddr(s)
		if !IsPublicUnicastIP(ip) {
			t.Fatalf("expected %s to be allowed as public unicast", s)
		}
	}
}

func TestWholeSetDNSRejectionAndTailscaleRules(t *testing.T) {
	t.Parallel()
	dialCalled := false
	stubDial := func(_ context.Context, _, _ string) (net.Conn, error) {
		dialCalled = true
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	}

	// Poisoned DNS set with 1 public + 1 private IP MUST reject the entire dial!
	poisonDial := SafeDialContext(Options{
		Lookup: func(_ context.Context, _, _ string) ([]netip.Addr, error) {
			return []netip.Addr{
				netip.MustParseAddr("1.1.1.1"),
				netip.MustParseAddr("169.254.169.254"),
			}, nil
		},
		Dial: stubDial,
	})
	if _, err := poisonDial(t.Context(), "tcp", "mixed.example.com:443"); err == nil {
		t.Fatal("expected poisoned mixed DNS set to be rejected")
	}
	if dialCalled {
		t.Fatal("dial should never be invoked when DNS set contains non-public IP")
	}

	// DNS rebinding: non-loopback hostname resolving to 127.0.0.1 rejected even with AllowLoopback=true.
	rebindDial := SafeDialContext(Options{
		AllowLoopback: true,
		Lookup: func(_ context.Context, _, _ string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		Dial: stubDial,
	})
	if _, err := rebindDial(t.Context(), "tcp", "evil.example.com:443"); err == nil {
		t.Fatal("expected non-loopback hostname resolving to 127.0.0.1 to be rejected")
	}

	// Tailscale mode permits *.ts.net -> 100.64.x.y, rejects Alibaba IMDS 100.100.100.200, and rejects non-ts.net CGNAT.
	tsAlibaba := SafeDialContext(Options{
		AllowTailscaleHosts: true,
		Lookup: func(_ context.Context, _, _ string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("100.100.100.200")}, nil
		},
		Dial: stubDial,
	})
	if _, err := tsAlibaba(t.Context(), "tcp", "node.tailnet.ts.net:443"); err == nil || !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("expected Alibaba IMDS blocked even on *.ts.net, got %v", err)
	}

	tsNonTS := SafeDialContext(Options{
		AllowTailscaleHosts: true,
		Lookup: func(_ context.Context, _, _ string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("100.64.0.5")}, nil
		},
		Dial: stubDial,
	})
	if _, err := tsNonTS(t.Context(), "tcp", "evil.example.com:443"); err == nil {
		t.Fatal("expected CGNAT blocked on non-*.ts.net host")
	}

	tsOK := SafeDialContext(Options{
		AllowTailscaleHosts: true,
		Lookup: func(_ context.Context, _, _ string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("100.64.0.5")}, nil
		},
		Dial: stubDial,
	})
	conn, err := tsOK(t.Context(), "tcp", "node.tailnet.ts.net:443")
	if err != nil {
		t.Fatalf("expected valid *.ts.net Tailscale peer to succeed, got %v", err)
	}
	_ = conn.Close()

	// LoopbackOnly rejects public IPs.
	loopOnly := SafeDialContext(Options{
		LoopbackOnly: true,
		Dial:         stubDial,
	})
	if _, err := loopOnly(t.Context(), "tcp", "1.1.1.1:5000"); err == nil {
		t.Fatal("expected LoopbackOnly to reject 1.1.1.1")
	}
	c, err := loopOnly(t.Context(), "tcp", "127.0.0.1:5000")
	if err != nil {
		t.Fatalf("expected LoopbackOnly to allow 127.0.0.1, got %v", err)
	}
	_ = c.Close()
}
