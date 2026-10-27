package wenstrategy

import (
	"encoding/json"
	"os"
	"testing"
)

func TestHistorySharedVectors(t *testing.T) {
	b, e := os.ReadFile("testdata/history-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Name, Raw string
		Valid     bool
	}
	if e = json.Unmarshal(b, &cases); e != nil {
		t.Fatal(e)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			_, e := ParseHistory([]byte(c.Raw))
			if (e == nil) != c.Valid {
				t.Fatalf("valid=%v error=%v", c.Valid, e)
			}
		})
	}
}
