package update

import (
	"fmt"
	"testing"
)

func TestStateFeatureGrowthKeepsProtocolBoundsAndDuplicateRefusal(t *testing.T) {
	features := []string{}
	for i := 0; i < 64; i++ {
		features = append(features, fmt.Sprintf("state-feature-%d", i))
	}
	if !stateFeaturesOK(features) || listOK(features) {
		t.Fatal("state and protocol limits conflated")
	}
	if stateFeaturesOK(append(features, "state-feature-64")) {
		t.Fatal("unbounded state contract")
	}
	if stateFeaturesOK(append(features[:33:33], features[0])) {
		t.Fatal("duplicate state feature accepted")
	}
}
