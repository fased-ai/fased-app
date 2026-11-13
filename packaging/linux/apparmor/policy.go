// Package apparmor contains the reviewed policy shipped in the application
// bundle. The maintenance helper uses these same bytes, never caller input.
package apparmor

import _ "embed"

//go:embed bwrap-userns-restrict
var policy []byte

func Policy() []byte { return append([]byte(nil), policy...) }
