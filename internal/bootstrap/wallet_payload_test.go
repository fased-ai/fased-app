package bootstrap

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestNativeReleaseBindingAcceptsBoundHeadlessSigner(t *testing.T) {
	source := strings.Repeat("a", 40)
	signer := strings.Repeat("b", 64)
	desktop := strings.Repeat("c", 64)
	for _, tc := range []struct {
		name    string
		binding NativeReleaseBinding
		valid   bool
	}{
		{"headless", NativeReleaseBinding{Version: 2, ProductVersion: "0.1.12", Source: source, SignerSHA256: signer}, true},
		{"desktop", NativeReleaseBinding{Version: 1, ProductVersion: "0.1.12", Source: source, SignerSHA256: signer, DesktopSHA256: desktop}, true},
		{"legacy needs desktop", NativeReleaseBinding{Version: 1, ProductVersion: "0.1.12", Source: source, SignerSHA256: signer}, false},
		{"headless cannot claim desktop", NativeReleaseBinding{Version: 2, ProductVersion: "0.1.12", Source: source, SignerSHA256: signer, DesktopSHA256: desktop}, false},
		{"missing signer", NativeReleaseBinding{Version: 2, ProductVersion: "0.1.12", Source: source}, false},
		{"wrong source", NativeReleaseBinding{Version: 2, ProductVersion: "0.1.12", Source: strings.Repeat("d", 40), SignerSHA256: signer}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.binding.Valid("0.1.12", source); got != tc.valid {
				t.Fatalf("valid = %v, want %v", got, tc.valid)
			}
		})
	}
}

func TestEmbeddedSignerStreamBindsExactVersionSourceAndBytes(t *testing.T) {
	p := &WalletPayload{Data: []byte("disposable signer executable"), Version: "0.1.11", Source: strings.Repeat("a", 40)}
	p.SHA256 = digest(p.Data)
	var wire bytes.Buffer
	if err := WriteWalletPayload(&wire, p); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"complete", "changed-byte", "truncated", "trailing", "version", "source", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			data := append([]byte(nil), wire.Bytes()...)
			version, source := p.Version, p.Source
			switch kind {
			case "changed-byte":
				data[len(data)-1] ^= 1
			case "truncated":
				data = data[:len(data)-1]
			case "trailing":
				data = append(data, 'x')
			case "version":
				version = "0.1.12"
			case "source":
				source = strings.Repeat("b", 40)
			case "oversized":
				id := WalletPayloadIdentity{1, p.Version, p.Source, p.SHA256, MaxWalletPayload + 1}
				data, _ = json.Marshal(id)
				data = append(data, '\n')
			}
			var output bytes.Buffer
			id, err := ReadWalletPayload(bytes.NewReader(data), &output, version, source)
			if kind == "complete" {
				if err != nil || !bytes.Equal(output.Bytes(), p.Data) || id.SHA256 != p.SHA256 {
					t.Fatalf("bound signer stream failed: %v", err)
				}
			} else if err == nil {
				t.Fatal("unbound signer stream admitted")
			}
		})
	}
	for _, p := range []*WalletPayload{nil, {Data: []byte("changed"), SHA256: p.SHA256, Version: p.Version, Source: p.Source}} {
		if err := WriteWalletPayload(io.Discard, p); err == nil {
			t.Fatal("missing or changed compiled signer emitted")
		}
	}
}
