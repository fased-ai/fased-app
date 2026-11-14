//go:build linux

package main

import (
	"bytes"
	"os"
	"strconv"
	"testing"
)

func TestPrivateIngressCommandRefusesMissingOrForeignSocket(t *testing.T) {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		t.Skip("ordinary user required")
	}
	var out bytes.Buffer
	if err := privateIngressCommand([]string{"private-ingress-capability"}, &out); err != nil || out.String() != "fased-protected-ingress-v1\n" {
		t.Fatal("fixed ingress capability differs", err)
	}
	for _, args := range [][]string{
		{"private-ingress-check"},
		{"private-ingress-check", "--owner", "0", "--kind", "vps"},
		{"private-ingress-check", "--owner", strconv.Itoa(os.Getuid()), "--kind", "foreign"},
		{"private-ingress-check", "--owner", "0123", "--kind", "vps"},
	} {
		if err := privateIngressCommand(args, &out); err == nil {
			t.Fatal("untrusted or missing listener accepted", args)
		}
	}
}
