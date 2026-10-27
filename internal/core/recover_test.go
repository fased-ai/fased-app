package core

import (
	"context"
	"testing"
)

func TestOwnerRecoveryRevokesSessionsAndPreservesIdentity(t *testing.T) {
	dir := fixtureDir(t)
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	code, e := s.IssueClaimCode(ctx)
	if e != nil {
		t.Fatal(e)
	}
	token, e := s.Claim(ctx, code.Code, "FixtureOriginalPassword")
	if e != nil {
		t.Fatal(e)
	}
	id := s.Health().InstanceID
	s.Close()
	server, e := Start(ctx, dir, "127.0.0.1:0", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = RecoverPassword(ctx, dir, "FixtureReplacementPassword"); e == nil {
		t.Fatal("recovery while engine running")
	}
	server.Close(ctx)
	if e = RecoverPassword(ctx, dir, "FixtureReplacementPassword"); e != nil {
		t.Fatal(e)
	}
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Health().InstanceID != id || s.Session(ctx, token) {
		t.Fatal("identity changed or old session survived")
	}
	if _, e = s.Login(ctx, "FixtureOriginalPassword"); e == nil {
		t.Fatal("old password accepted")
	}
	if _, e = s.Login(ctx, "FixtureReplacementPassword"); e != nil {
		t.Fatal(e)
	}
}
