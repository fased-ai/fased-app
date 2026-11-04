package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestRemoteReaderReleasedStatusPreservesPreparedPause(t *testing.T) {
	want := update.CorePauseRequest{Version: 1, IntentID: strings.Repeat("2", 32), Sequence: 1000008, ManifestSHA256: strings.Repeat("a", 64)}
	expected := want
	previous := want
	previous.IntentID, previous.Sequence, previous.ManifestSHA256 = strings.Repeat("1", 32), 1000006, strings.Repeat("b", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var command update.CorePauseCommand
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/updates/pause" || json.NewDecoder(r.Body).Decode(&command) != nil {
			http.Error(w, "unexpected pause request", http.StatusBadRequest)
			return
		}
		if command.Action == "status" {
			_ = json.NewEncoder(w).Encode(update.CorePauseReceipt{Version: 1, State: "released", Request: &previous})
			return
		}
		if command.Action != "pause" || command.Request == nil || !reflect.DeepEqual(*command.Request, expected) {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "released pause sequence cannot replay"})
			return
		}
		_ = json.NewEncoder(w).Encode(update.CorePauseReceipt{Version: 1, State: "paused", Request: command.Request})
	}))
	defer server.Close()
	client := &core.Client{Endpoint: core.Endpoint{URL: server.URL}, HTTP: server.Client()}
	predecessor := update.CorePauseReceipt{Version: 1, State: "prepared", Request: &want}
	if err := readRemoteReaderPauseStatus(t.Context(), client, &predecessor); err != nil {
		t.Fatal(err)
	}
	if err := client.Do(t.Context(), http.MethodPost, "/api/v1/updates/pause", update.CorePauseCommand{Action: "pause", ProbeID: update.NewProbe(), Request: &want}, &predecessor); err != nil {
		t.Fatal("released status replaced the newly prepared pause request:", err)
	}
	if predecessor.State != "paused" || !reflect.DeepEqual(want, expected) {
		t.Fatal("prepared release authority changed during status refresh")
	}
}
