package maintenance

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestMaintenanceRequestsNeverAdmitCommandOrPathOverrides(t *testing.T) {
	good := `{"version":1,"action":"serve","serve":{"configuration":{},"etag":"` + strings.Repeat("a", 64) + `"}}`
	if _, err := DecodeRequest([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		strings.Replace(good, `"version":1`, `"version":1,"command":["/bin/sh"]`, 1),
		strings.Replace(good, `"version":1`, `"version":1,"owner":0`, 1),
		strings.Replace(good, `"version":1`, `"version":1,"config":"/tmp/caller.json"`, 1),
		strings.Replace(good, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(good, `"serve":{"configuration"`, `"serve":{"url":"http://outside.invalid","configuration"`, 1),
		strings.Replace(good, `"action":"serve"`, `"action":"execute"`, 1),
		good + `{}`,
	} {
		if _, err := DecodeRequest([]byte(data)); err == nil {
			t.Fatal("unbounded maintenance request admitted")
		}
	}
}

func TestTailscaleLoginTakesNoCallerInputs(t *testing.T) {
	if _, err := DecodeRequest([]byte(`{"version":1,"action":"tailscale-login"}`)); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`{"version":1,"action":"tailscale-login","command":"login"}`,
		`{"version":1,"action":"tailscale-login","peer":{"userId":1,"nodeId":"foreign","login":"foreign"}}`,
		`{"version":1,"action":"tailscale-login","remote":{}}`,
	} {
		if _, err := DecodeRequest([]byte(input)); err == nil {
			t.Fatal("caller-selected Tailscale authority was accepted")
		}
	}
}

func TestRemotePreflightOriginTakesNoCallerInputs(t *testing.T) {
	if _, err := DecodeRequest([]byte(`{"version":1,"action":"remote-preflight-origin"}`)); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`{"version":1,"action":"remote-preflight-origin","command":"/bin/sh"}`,
		`{"version":1,"action":"remote-preflight-origin","peer":{"userId":1,"nodeId":"foreign","login":"foreign"}}`,
		`{"version":1,"action":"remote-preflight-origin","remote":{}}`,
	} {
		if _, err := DecodeRequest([]byte(input)); err == nil {
			t.Fatal("caller-selected protected origin input was accepted")
		}
	}
}

func TestEnrollmentImageRequestCannotSelectUIDOrPath(t *testing.T) {
	instance := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	good := `{"version":1,"action":"custody-enrollment-image","image":{"operationId":"` + strings.Repeat("a", 32) + `","action":"import","slot":"stage","instanceId":"` + instance + `","digest":"` + strings.Repeat("b", 64) + `"}}`
	if _, err := DecodeRequest([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(good, `"slot":"stage"`, `"slot":"stage","path":"/etc/shadow"`, 1),
		strings.Replace(good, `"slot":"stage"`, `"slot":"stage","uid":0`, 1),
		strings.Replace(good, `"slot":"stage"`, `"slot":"app"`, 1),
		strings.Replace(good, `"action":"import"`, `"action":"execute"`, 1),
	} {
		if _, err := DecodeRequest([]byte(bad)); err == nil {
			t.Fatal("unbounded image request admitted")
		}
	}
}

func TestMaintenanceCustodyRequestIsOneTypedOperation(t *testing.T) {
	good := `{"version":1,"action":"custody-update","custody":{"operationId":"` + strings.Repeat("a", 32) + `","sequence":"8","releaseSha256":"` + strings.Repeat("b", 64) + `","bootstrap":true}}`
	if _, err := DecodeRequest([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(good, `"bootstrap":true`, `"bootstrap":true,"signerPath":"/tmp/signer"`, 1), strings.Replace(good, `"version":1`, `"version":1,"peer":{"userId":1}`, 1), strings.Replace(good, `"sequence":"8"`, `"sequence":"0"`, 1), strings.Replace(good, `"action":"custody-update"`, `"action":"remote-stop"`, 1)} {
		if _, err := DecodeRequest([]byte(bad)); err == nil {
			t.Fatal("unbound custody operation admitted")
		}
	}
}

func TestMaintenanceCustodyInitializationHasNoCallerPaths(t *testing.T) {
	good := `{"version":1,"action":"custody-initialize"}`
	if _, err := DecodeRequest([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(good, `"version":1`, `"version":1,"source":"/tmp/core"`, 1),
		strings.Replace(good, `"version":1`, `"version":1,"owner":0`, 1),
		strings.Replace(good, `"version":1`, `"version":1,"custody":{"operationId":"`+strings.Repeat("a", 32)+`","sequence":"1","releaseSha256":"`+strings.Repeat("b", 64)+`"}`, 1),
	} {
		if _, err := DecodeRequest([]byte(bad)); err == nil {
			t.Fatal("caller-controlled initialization input admitted")
		}
	}
}

func TestCustodyEnrollmentPrepareDoesNotAdmitOSAuthority(t *testing.T) {
	good := `{"version":1,"action":"custody-enrollment-prepare","enrollment":{"operationId":"` + strings.Repeat("a", 32) + `","network":"devnet","listen":"127.0.0.1:7332","coreSha256":"` + strings.Repeat("b", 64) + `","signerSha256":"` + strings.Repeat("c", 64) + `"}}`
	if _, err := DecodeRequest([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(good, `"network":"devnet"`, `"network":"devnet","path":"/tmp/binary"`, 1),
		strings.Replace(good, `"network":"devnet"`, `"network":"devnet","uid":0`, 1),
		strings.Replace(good, `"version":1`, `"version":1,"command":["/bin/sh"]`, 1),
		strings.Replace(good, `"network":"devnet"`, `"network":"testnet"`, 1),
		strings.Replace(good, strings.Repeat("b", 64), strings.Repeat("g", 64), 1),
		strings.Replace(good, `"action":"custody-enrollment-prepare"`, `"action":"custody-initialize"`, 1),
	} {
		if _, err := DecodeRequest([]byte(bad)); err == nil {
			t.Fatal("unbounded enrollment request admitted")
		}
	}
}

func TestEnrollmentCutoverAllowsOnlyFixedSteps(t *testing.T) {
	id := strings.Repeat("a", 32)
	reserve := `{"version":1,"action":"custody-enrollment-cutover","enrollment":{"operationId":"` + id + `","step":"reserve"}}`
	if _, err := DecodeRequest([]byte(reserve)); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"custody-enrollment-cutover", "custody-enrollment-activation"} {
		step := "fence"
		if action == "custody-enrollment-activation" {
			step = "activate"
		}
		good := `{"version":1,"action":"` + action + `","enrollment":{"operationId":"` + id + `","step":"` + step + `"}}`
		if _, err := DecodeRequest([]byte(good)); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{
			strings.Replace(good, `"step":"`+step+`"`, `"step":"execute"`, 1),
			strings.Replace(good, `"step":"`+step+`"`, `"step":"`+step+`","path":"/etc/shadow"`, 1),
			strings.Replace(good, `"step":"`+step+`"`, `"step":"`+step+`","uid":0`, 1),
		} {
			if _, err := DecodeRequest([]byte(bad)); err == nil {
				t.Fatal("unbounded enrollment step admitted")
			}
		}
	}
	for _, action := range []string{"remote-enrollment-validate", "remote-enrollment-status", "remote-enrollment-prepared-status", "remote-enrollment-suspend", "remote-enrollment-restore"} {
		good := `{"version":1,"action":"` + action + `"}`
		if _, err := DecodeRequest([]byte(good)); err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeRequest([]byte(strings.Replace(good, `"version":1`, `"version":1,"command":["/bin/sh"]`, 1))); err == nil {
			t.Fatal("arbitrary Remote command admitted")
		}
	}
}
