package releaseprovenance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	"github.com/fased-ai/fased-app/internal/releaseprovenance/fixture"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestCommitBoundProvenance(t *testing.T) {
	p, original, now := inputs()
	raw, _ := json.Marshal(original)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	fields["buildRef"] = "refs/heads/main"
	raw, _ = json.Marshal(fields)
	var expected releaseprovenance.Expected
	if err := update.StrictJSON(raw, &expected); err != nil {
		t.Fatal(err)
	}
	p, proof, err := fixture.New(p, expected, now)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := releaseprovenance.Verify(p, proof, expected, now)
	if err != nil || !strings.HasSuffix(decision.Identity, "@refs/heads/main") {
		t.Fatal("commit-bound staging rejected", decision, err)
	}
	for _, ref := range []string{"refs/heads/topic", "refs/pull/2/merge", "refs/tags/v0.1.0", "https://attacker.invalid"} {
		fields["buildRef"] = ref
		raw, _ = json.Marshal(fields)
		if err := update.StrictJSON(raw, &expected); err != nil {
			t.Fatal(err)
		}
		requireRefusal(t, p, proof, expected, now)
	}
	requireRefusal(t, p, proof, original, now)
	fields["buildRef"] = "refs/heads/main"
	fields["runAttempt"] = "1"
	raw, _ = json.Marshal(fields)
	_ = update.StrictJSON(raw, &expected)
	requireRefusal(t, p, proof, expected, now)
}
