package bootstrap

import (
	"encoding/json"
	"testing"
)

func TestValidateProductionSeedOffline(t *testing.T) {
	_, o, _ := productionFixture(t, "")
	seed, err := ValidateProductionSeed(o.Seed, o.SeedSHA256)
	if err != nil || seed.Version != 3 || seed.DevelopmentOnly || seed.Provenance == nil {
		t.Fatal(seed.Version, err)
	}
	if _, err = ValidateProductionSeed(o.Seed, ""); err == nil {
		t.Fatal("unpinned seed accepted")
	}
	if _, err = ValidateProductionSeed(append(o.Seed, ' '), o.SeedSHA256); err == nil {
		t.Fatal("changed seed accepted")
	}
	var changed Seed
	if err = json.Unmarshal(o.Seed, &changed); err != nil {
		t.Fatal(err)
	}
	changed.Version, changed.DevelopmentOnly, changed.Channel = 2, true, "development"
	changed.Purpose, changed.Provenance = GitHubPurpose, nil
	raw := jsonBytes(t, changed)
	if _, err = ValidateProductionSeed(raw, digest(raw)); err == nil {
		t.Fatal("development seed became a production binding")
	}
}
