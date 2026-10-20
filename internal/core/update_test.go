package core

import (
	"context"
	"strconv"
	"testing"
)

func TestUpdateContractMatchesDurableCore(t *testing.T) {
	s := fixtureStore(t)
	var schema int
	if err := s.db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	c := UpdateContract()
	if schema != StateSchema || c.State[0].Versions.Min != schema || c.State[0].Versions.Max != schema || strconv.Itoa(c.Protocol) != APIVersion {
		t.Fatal("stale core update contract")
	}
}
