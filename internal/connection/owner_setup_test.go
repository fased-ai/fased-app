package connection

import (
	"strings"
	"testing"
)

func TestOwnerSetupURL(t *testing.T) {
	id, proof := strings.Repeat("i", 43), strings.Repeat("s", 43)
	got, err := OwnerSetupURL("http://127.0.0.1:7331", id, proof)
	if err != nil || got != "http://127.0.0.1:7331/?instance="+id+"#setup="+proof {
		t.Fatal(got, err)
	}
	for _, origin := range []string{"http://public.example", "https://private.example/?x=y", "https://private.example/#other"} {
		if _, err = OwnerSetupURL(origin, id, proof); err == nil {
			t.Fatal("unsafe origin accepted")
		}
	}
	for _, bad := range []string{"bad", proof + "&view=evil", proof + "\n"} {
		if _, err = OwnerSetupURL("https://private.example", id, bad); err == nil {
			t.Fatal("bad proof accepted")
		}
	}
	got, err = OwnerSetupURL("https://private.example", id, "")
	if err != nil || strings.Contains(got, "setup=") {
		t.Fatal("claimed reopen", got, err)
	}
}
