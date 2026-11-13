package maintenance

import (
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
