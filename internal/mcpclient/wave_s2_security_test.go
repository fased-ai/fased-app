package mcpclient

import "testing"

func TestWaveS2ReadOnlyGuardCamelCaseAndDefaultDeny(t *testing.T) {
	// H6: CamelCase, PascalCase, and common mutating verbs MUST be blocked under ReadOnly.
	blocked := []string{
		"sendTransaction",
		"placeOrder",
		"executeSwap",
		"deleteFile",
		"writeFile",
		"runCommand",
		"signMessage",
		"createUser",
		"updatePolicy",
		"callWebhook",
		"approveSpend",
		"transferFunds",
		"withdrawAll",
		"deployContract",
		"evalScript",
		"mysteryCustomTool", // default-deny unrecognized verb
	}
	for _, name := range blocked {
		if !isMutatingToolName(name) {
			t.Fatalf("expected %q blocked by default-deny read-only guard", name)
		}
	}

	// Legitimate read-only tool names in camelCase, snake_case, and kebab-case MUST be allowed.
	allowed := []string{
		"getBalance",
		"list_markets",
		"read-file",
		"fetchPrice",
		"searchDocs",
		"inspectAccount",
		"describeSchema",
		"quote_jupiter",
		"checkStatus",
	}
	for _, name := range allowed {
		if isMutatingToolName(name) {
			t.Fatalf("expected read-only tool %q allowed", name)
		}
	}
}
