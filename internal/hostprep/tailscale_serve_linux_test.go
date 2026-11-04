package hostprep

import (
	"strings"
	"testing"
)

func TestPreparationRefusesGenericElevatedCurl(t *testing.T) {
	system := &System{}
	for _, args := range [][]string{
		{"/usr/bin/curl", "https://outside.invalid"},
		{"/usr/bin/curl", "--unix-socket", "/run/docker.sock", "http://localhost/containers/json"},
	} {
		if _, err := system.Run(t.Context(), nil, args...); err == nil || !strings.Contains(err.Error(), "fixed conditional private Serve") {
			t.Fatal("generic curl reached OS authorization", err)
		}
	}
}
