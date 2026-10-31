//go:build linux

package main

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/signer"
)

func configureOrdinaryEnrollmentFixture() {
	compiledOrdinaryEnrollment = enrollment.RunControlledOwnerCustody
}

func TestOrdinaryEnrollmentOwnerBoundary(t *testing.T) {
	held := compiledOrdinaryEnrollment
	compiledOrdinaryEnrollment = nil
	defer func() { compiledOrdinaryEnrollment = held }()
	var out bytes.Buffer
	if err := enrollmentCommand([]string{"--ordinary-custody"}, nil, &out, io.Discard); !errors.Is(err, signer.ErrUnavailable) || out.Len() != 0 {
		t.Fatal("ordinary admission crossed source/credential boundary", err)
	}
	m := custodyinstall.Manifest{Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.ControlledCustodyProfile}
	for _, action := range []string{"setup", "import", "recover"} {
		if flag, err := custodyOwnerFlag(m, action); err != nil || flag != "--disposable-fixture" {
			t.Fatal(action, flag, err)
		}
	}
	for _, action := range []string{"approve", "approve-swap", "receipt-rpc-save", "update-preferences"} {
		if _, err := custodyOwnerFlag(m, action); err == nil {
			t.Fatal("unsupported authority exposed", action)
		}
	}
	m.Provenance = signer.OwnerKeyProvenance
	if _, err := custodyOwnerFlag(m, "import"); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("real key admission enabled")
	}
}
