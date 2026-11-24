package core

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteFileURIFromSlash(t *testing.T) {
	const dsnQuery = "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
	cases := []struct {
		name      string
		slashPath string
		rawQuery  string
		want      string
	}{
		{
			name:      "unix absolute path",
			slashPath: "/tmp/fased/core.sqlite",
			rawQuery:  dsnQuery,
			want:      "file:///tmp/fased/core.sqlite?" + dsnQuery,
		},
		{
			name:      "windows drive letter path",
			slashPath: "C:/Users/runneradmin/AppData/Roaming/fased-app/core.sqlite",
			rawQuery:  dsnQuery,
			want:      "file:///C:/Users/runneradmin/AppData/Roaming/fased-app/core.sqlite?" + dsnQuery,
		},
		{
			name:      "windows path with spaces and reserved characters",
			slashPath: "d:/dir with spaces/a#b?c/core.sqlite",
			rawQuery:  "mode=ro&immutable=1",
			want:      "file:///d:/dir%20with%20spaces/a%23b%3Fc/core.sqlite?mode=ro&immutable=1",
		},
		{
			name:      "windows unc path",
			slashPath: "//server/share/core.sqlite",
			rawQuery:  "mode=rw",
			want:      "file:////server/share/core.sqlite?mode=rw",
		},
		{
			name:      "relative path",
			slashPath: "sub/core.sqlite",
			rawQuery:  "mode=rw",
			want:      "file:sub/core.sqlite?mode=rw",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sqliteFileURIFromSlash(tc.slashPath, tc.rawQuery)
			if got != tc.want {
				t.Fatalf("sqliteFileURIFromSlash(%q, %q) = %q, want %q", tc.slashPath, tc.rawQuery, got, tc.want)
			}
		})
	}
}

func TestOpenStoreWithSpacesInPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Fased App #1")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore(%q): %v", dir, err)
	}
	defer s.Close()
	if !strings.HasPrefix(sqliteDSN(filepath.Join(dir, "core.sqlite")), "file:///") {
		t.Fatalf("expected triple-slash file URI for absolute path")
	}
}
