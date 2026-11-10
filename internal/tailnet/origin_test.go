package tailnet

import "testing"

func TestPrivateHTTPSOriginAndIsolatedPort(t *testing.T) {
	for _, v := range []string{"https://node.owner.ts.net", "https://node.owner.ts.net:8443", "https://node.owner.ts.net:65535"} {
		if _, _, err := Origin(v); err != nil {
			t.Fatalf("valid %q: %v", v, err)
		}
	}
	for _, v := range []string{"http://node.owner.ts.net", "https://public.example", "https://node.owner.ts.net:22", "https://node.owner.ts.net:0443", "https://node.owner.ts.net:65536", "https://node.owner.ts.net:8443/path", "https://user@node.owner.ts.net:8443", "https://node.owner.ts.net.evil.example:8443"} {
		if _, _, err := Origin(v); err == nil {
			t.Fatalf("unsafe origin %q accepted", v)
		}
	}
}
