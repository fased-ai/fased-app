//go:build linux

package main

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestProviderFixtureOptionsOwnModeAndExactPackage(t *testing.T) {
	args := []string{"--disposable-fixture", "--engine", "/tmp/verified/fased-app", "--engine-sha256", strings.Repeat("a", 64)}
	o, e := providerOptions(args)
	if e != nil || !o.ProviderFixture || !o.Start || !o.Autostart || o.PublicOrigin != "" || o.Listen != "127.0.0.1:17485" {
		t.Fatal(o, e)
	}
	for _, bad := range [][]string{nil, args[1:], append(append([]string{}, args...), "--provider-fixture=false"), append(append([]string{}, args...), "--engine-sha256", strings.Repeat("A", 64)), append(append([]string{}, args...), "--engine", "relative"), append(append([]string{}, args...), "--unknown", "0")} {
		if _, e := providerOptions(bad); e == nil {
			t.Fatal("accepted unbound mode", bad)
		}
	}
	var out bytes.Buffer
	if e := run(append([]string{"installed-provider"}, args...), &out); e == nil || out.Len() != 0 {
		t.Fatal("owner host admitted to disposable installer", e)
	}
}

func TestProviderNamespaceRequiresActualReadonlyOwnedProcess(t *testing.T) {
	uid := strconv.Itoa(os.Getuid())
	status := "Uid:\t" + strings.Repeat(uid+"\t", 4) + "\nNoNewPrivs:\t1\n"
	mounts := "1 0 0:1 / / ro - tmpfs tmpfs ro\n2 1 0:2 / /home/owner/data rw - tmpfs tmpfs rw\n"
	if !providerNamespace(status, mounts, "/home/owner/app", "/home/owner/data") {
		t.Fatal("valid namespace refused")
	}
	for _, s := range []string{strings.ReplaceAll(status, "NoNewPrivs:\t1", "NoNewPrivs:\t0"), strings.ReplaceAll(status, uid+"\t", "0\t")} {
		if providerNamespace(s, mounts, "/home/owner/app", "/home/owner/data") {
			t.Fatal("unrestricted process accepted")
		}
	}
	if providerNamespace(status, strings.ReplaceAll(mounts, " / ro ", " / rw "), "/home/owner/app", "/home/owner/data") {
		t.Fatal("writable app root accepted")
	}
}
