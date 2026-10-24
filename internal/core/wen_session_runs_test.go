package core

import "testing"

func TestWENFixtureRunAdmission(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	w := apiRequest(h, "GET", "/api/v1/wen/session-runs?profile=wen-operator", "", token, "http://127.0.0.1:7331", "")
	if w.Code != 200 {
		t.Fatalf("fixture-run inspect: %d %s", w.Code, w.Body.String())
	}
	if StateSchema < 15 {
		t.Fatal("run consent requires schema15 protection")
	}
}
