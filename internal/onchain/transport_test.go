package onchain

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestDialPublicAddressFilteringAndFailover(t *testing.T) {
	t.Parallel()

	// Reject any DNS answer containing a private, loopback, or link-local IP BEFORE dialing.
	badSets := [][]netip.Addr{
		{netip.MustParseAddr("127.0.0.1")},
		{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("10.0.0.1")},
		{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("169.254.169.254")},
		{netip.MustParseAddr("2606:4700::1111"), netip.MustParseAddr("fc00::1")},
	}
	for _, ips := range badSets {
		dialed := 0
		_, err := dialPublicAddress(
			t.Context(),
			"tcp",
			"rpc.example.com:443",
			func(context.Context, string, string) ([]netip.Addr, error) { return ips, nil },
			func(context.Context, string, string) (net.Conn, error) {
				dialed++
				return nil, errors.New("unexpected dial")
			},
		)
		if err == nil || dialed != 0 {
			t.Fatalf("expected rejection without dialing for %v, got err=%v dialed=%d", ips, err, dialed)
		}
	}

	// Interleave IPv4 + IPv6 (capped at 6 targets) and fail over to healthy second IP.
	pubIPs := []netip.Addr{
		netip.MustParseAddr("1.1.1.1"),
		netip.MustParseAddr("8.8.8.8"),
		netip.MustParseAddr("9.9.9.9"),
		netip.MustParseAddr("4.2.2.1"),
		netip.MustParseAddr("2606:4700::1111"),
		netip.MustParseAddr("2001:4860:4860::8888"),
		netip.MustParseAddr("2620:fe::fe"),
	}
	var attempts []string
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	conn, err := dialPublicAddress(
		t.Context(),
		"tcp",
		"rpc.example.com:443",
		func(context.Context, string, string) ([]netip.Addr, error) { return pubIPs, nil },
		func(ctx context.Context, _ string, addr string) (net.Conn, error) {
			attempts = append(attempts, addr)
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 2500*time.Millisecond {
				t.Fatalf("expected <=2s per-attempt timeout, ok=%v rem=%v", ok, time.Until(deadline))
			}
			if len(attempts) == 1 {
				return nil, errors.New("first IP unreachable")
			}
			return c1, nil
		},
	)
	if err != nil || conn != c1 {
		t.Fatalf("expected failover to second IP, got conn=%v err=%v", conn, err)
	}
	if len(attempts) != 2 || attempts[0] != "1.1.1.1:443" || attempts[1] != "[2606:4700::1111]:443" {
		t.Fatalf("expected interleaved v4/v6 dial order, got %v", attempts)
	}

	interleaved, err := interleavePublicIPs(pubIPs)
	if err != nil || len(interleaved) != 6 {
		t.Fatalf("expected 6 interleaved targets, got %v err=%v", interleaved, err)
	}
}
