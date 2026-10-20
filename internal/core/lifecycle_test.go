package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestLifecycleSnapshotRequiresAuthenticatedCore(t *testing.T) {
	s := fixtureStore(t)
	code, err := s.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Claim(t.Context(), code.Code, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, loggedIn := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/updates/inventory?probe=11111111111111111111111111111111", nil)
		if loggedIn {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 401
		if loggedIn {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("inventory status %d, expected %d", w.Code, want)
		}
		if loggedIn {
			var v update.CoreSnapshot
			if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil || v.ProbeID != "11111111111111111111111111111111" || v.State.Version != 2 || strings.Contains(w.Body.String(), "password") {
				t.Fatal("invalid or secret-bearing snapshot")
			}
		}
	}
}

func TestLifecycleSnapshotBindsMetadataCredentialAndBoot(t *testing.T) {
	s := fixtureStore(t)
	_ = claimFixture(t, s)
	p := update.NewProbe()
	a, e := s.UpdateSnapshot(t.Context(), p)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Login(t.Context(), testPassword); e != nil {
		t.Fatal(e)
	}
	b, e := s.UpdateSnapshot(t.Context(), p)
	if e != nil || a.State.Anchor != b.State.Anchor {
		t.Fatal("ephemeral session changed durable metadata anchor", e)
	}
	for _, q := range []string{"UPDATE instance SET revision=revision+1", "UPDATE accounts SET revision=revision+1", "UPDATE instance SET password_hash=x'010203'"} {
		if _, e = s.db.ExecContext(t.Context(), q); e != nil {
			t.Fatal(e)
		}
		b, e = s.UpdateSnapshot(t.Context(), p)
		if e != nil || a.State.Anchor == b.State.Anchor {
			t.Fatal("metadata or credential change not bound", e)
		}
		a = b
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	b, e = next.UpdateSnapshot(t.Context(), p)
	if e != nil || b.State.Anchor != a.State.Anchor || b.BootID == a.BootID {
		t.Fatal("restart identity not separated from durable anchor", e)
	}
	if _, e = next.db.ExecContext(t.Context(), "PRAGMA user_version=99"); e != nil {
		t.Fatal(e)
	}
	if _, e = next.UpdateSnapshot(t.Context(), p); e == nil {
		t.Fatal("unknown schema snapshot accepted")
	}
}
