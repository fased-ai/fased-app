package releaseops

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestReleaseTransportPinnedAddressFallback(t *testing.T) {
	for _, mode := range []string{"refused", "timeout", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			lookups, dials := 0, 0
			peer, connection := net.Pipe()
			defer peer.Close()
			defer connection.Close()
			w, err := newTransport(nil, func(context.Context, string) ([]netip.Addr, error) {
				lookups++
				return []netip.Addr{netip.MustParseAddr("2606:50c0:8000::154"), netip.MustParseAddr("185.199.108.133")}, nil
			}, func(attempt context.Context, network, address string) (net.Conn, error) {
				dials++
				if network != "tcp" {
					t.Fatal("changed dial network")
				}
				if dials == 1 {
					if address != "[2606:50c0:8000::154]:443" {
						t.Fatal("first dial was not pinned", address)
					}
					switch mode {
					case "timeout":
						if deadline, ok := attempt.Deadline(); !ok || time.Until(deadline) > 750*time.Millisecond {
							t.Fatal("first address can exhaust the request deadline")
						}
						<-attempt.Done()
					case "cancelled":
						cancel()
					}
					return nil, errors.New("first address unavailable")
				}
				if dials != 2 || address != "185.199.108.133:443" {
					t.Fatal("fallback escaped pinned DNS", address)
				}
				return connection, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer w.wire.CloseIdleConnections()
			got, err := w.wire.DialContext(ctx, "tcp", apiHost+":443")
			if mode == "cancelled" {
				if err == nil || got != nil || dials != 1 {
					t.Fatal("cancelled request tried another address", dials, err)
				}
			} else if err != nil || got != connection || dials != 2 {
				t.Fatal("unavailable first address prevented fallback", dials, err)
			}
			if lookups != 1 {
				t.Fatal("fallback repeated DNS resolution", lookups)
			}
		})
	}
}
