package maintenance

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestPackageWalletRequestIsBoundedAndIdentityOnly(t *testing.T) {
	w := update.WalletInitialization{Version: 1, OperationID: strings.Repeat("a", 32), InstanceID: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), Network: "devnet"}
	raw, err := json.Marshal(PackageRequest{Version: 1, Action: "wallet-initialize", Wallet: &w})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodePackageRequest(raw); err != nil || got.Wallet == nil || *got.Wallet != w {
		t.Fatal("the original wallet request was not accepted", err)
	}
	for _, bad := range []string{
		strings.Replace(string(raw), `"network":"devnet"`, `"network":"testnet"`, 1),
		strings.Replace(string(raw), w.InstanceID, "foreign", 1),
		strings.Replace(string(raw), w.OperationID, "other", 1),
		strings.Replace(string(raw), `"network":"devnet"`, `"network":"devnet","path":"/tmp/foreign"`, 1),
		strings.Replace(string(raw), `"action":"wallet-initialize"`, `"action":"status"`, 1),
	} {
		if _, err := DecodePackageRequest([]byte(bad)); err == nil {
			t.Fatal("foreign or extra wallet request input was accepted")
		}
	}
}
