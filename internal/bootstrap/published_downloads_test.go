package bootstrap

import "testing"

func TestPublishedDownloadsRequireBuildOverlay(t *testing.T) {
	if productionGitHubActivationEnabled {
		t.Fatal("ordinary source enabled production acquisition")
	}
	_, options, hits := productionFixture(t, "ordinary-disabled")
	seed, err := ValidateProductionSeed(options.Seed, options.SeedSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = initialDownloads(options, seed); err == nil {
		t.Fatal("ordinary source made a production transport")
	}
	productionGitHubActivationEnabled = true
	t.Cleanup(func() { productionGitHubActivationEnabled = false })
	downloads, err := initialDownloads(options, seed)
	if err != nil {
		t.Fatal(err)
	}
	downloads.close()
	bad := seed
	bad.GitHub.MetadataHost = "attacker.invalid"
	if _, err = initialDownloads(options, bad); err == nil {
		t.Fatal("published acquisition accepted a different metadata host")
	}
	options.TLSCA = []byte("runtime override")
	if _, err = initialDownloads(options, seed); err == nil {
		t.Fatal("published acquisition accepted a TLS override")
	}
	if hits.Load() != 0 {
		t.Fatal("constructor-only check dispatched a request")
	}
}
