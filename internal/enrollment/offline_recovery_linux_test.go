//go:build linux

package enrollment

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestOfflineRecoveryKeepsOriginalCustodyOperation(t *testing.T) {
	fixture := func() (fence, update.MaintenanceRecord) {
		base := custodyinstall.Manifest{Owner: 1000, ID: strings.Repeat("1", 32), Root: custodyinstall.Root(1000), Version: 3, Enrollment: true, Location: "local", NativeOwnerAccess: true, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.ControlledCustodyProfile, Network: "devnet", CoreHash: strings.Repeat("2", 64), SignerHash: strings.Repeat("3", 64)}
		target := base
		target.CoreHash, target.SignerHash = strings.Repeat("4", 64), strings.Repeat("5", 64)
		baseJSON, _ := json.Marshal(base)
		targetJSON, _ := json.Marshal(target)
		p := &update.MaintenancePackage{Version: "0.1.12", Source: strings.Repeat("6", 40), Sequence: 12, ReleaseSHA256: strings.Repeat("7", 64), DevelopmentOnly: true, Components: []update.Component{
			{Version: "0.1.12", Contract: core.OrdinaryUpdateContract(), Artifact: update.Artifact{File: "fased-app", Size: 1, SHA256: target.CoreHash}},
			{Version: "0.1.12", Contract: signer.OrdinaryUpdateContract(), Artifact: update.Artifact{File: "fased-signer", Size: 1, SHA256: target.SignerHash}},
		}}
		return fence{Owner: 1000, Source: sourceRecord{Profile: "local"}, Target: base}, update.MaintenanceRecord{Version: 1, Owner: 1000, OperationID: strings.Repeat("8", 32), Phase: "prepared", Start: "base", Base: baseJSON, Target: targetJSON, Package: p}
	}
	f, r := fixture()
	q, err := enrolledRecoveryRequest(f, r, 1000)
	if err != nil || q.OperationID != r.OperationID || q.Sequence != r.Package.Sequence || q.ReleaseSHA256 != r.Package.ReleaseSHA256 {
		t.Fatalf("original operation was not retained: %+v %v", q, err)
	}
	for name, change := range map[string]func(*fence, *update.MaintenanceRecord){
		"another owner":  func(_ *fence, r *update.MaintenanceRecord) { r.Owner++ },
		"remote source":  func(f *fence, _ *update.MaintenanceRecord) { f.Source.Profile = "remote" },
		"another target": func(_ *fence, r *update.MaintenanceRecord) { r.Target = []byte(`{}`) },
		"different package": func(_ *fence, r *update.MaintenanceRecord) {
			r.Package.Components[0].Artifact.SHA256 = strings.Repeat("9", 64)
		},
		"idle is not recovery":       func(_ *fence, r *update.MaintenanceRecord) { r.Phase = "idle" },
		"changed start":              func(_ *fence, r *update.MaintenanceRecord) { r.Start = "target" },
		"target without signer hold": func(_ *fence, r *update.MaintenanceRecord) { r.Phase, r.Start = "ready", "target" },
	} {
		t.Run(name, func(t *testing.T) {
			f, r := fixture()
			change(&f, &r)
			if _, err := enrolledRecoveryRequest(f, r, 1000); err == nil {
				t.Fatal("foreign or incomplete recovery was admitted")
			}
		})
	}
}
