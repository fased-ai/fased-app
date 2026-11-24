package core

import (
	"net/url"
	"path/filepath"
	"strings"
)

func isWindowsDriveSlashPath(p string) bool {
	return len(p) >= 2 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':'
}

func sqliteFileURIFromSlash(slashPath, rawQuery string) string {
	u := url.URL{
		Scheme:   "file",
		RawQuery: rawQuery,
	}
	switch {
	case isWindowsDriveSlashPath(slashPath):
		u.Path = "/" + slashPath
	case strings.HasPrefix(slashPath, "/"):
		u.Path = slashPath
	default:
		u.OmitHost = true
		u.Path = slashPath
	}
	return u.String()
}

func sqliteFileURI(dbPath, rawQuery string) string {
	return sqliteFileURIFromSlash(filepath.ToSlash(dbPath), rawQuery)
}
