//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func attachEnrollmentFixture(h *memoryHost, o *hostOps, m Manifest, form string) (enrollmentJournalBinding, *custodyinstall.Manifest) {
	target := &custodyinstall.Manifest{Version: 2, Mode: "disposable-custody:v2", Network: "devnet", Enrollment: true, Location: "remote", Owner: m.OwnerUID, ID: strings.Repeat("a", 32), Root: custodyinstall.Root(m.OwnerUID), Listen: m.Listen, PublicOrigin: m.PublicOrigin, CoreHash: strings.Repeat("b", 64), SignerHash: strings.Repeat("c", 64), App: custodyinstall.Identity{Name: "fixture-app", UID: 995, GID: 995}, Signer: custodyinstall.Identity{Name: "fixture-signer", UID: 996, GID: 996}}
	f := enrollmentFenceBinding{Version: 1, ID: strings.Repeat("d", 32), Owner: m.OwnerUID, Source: enrollmentSourceBinding{Profile: "remote", Data: m.Data, Listen: m.Listen, PublicOrigin: m.PublicOrigin, ExpectedInstanceID: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), UID: m.AppUID, GID: m.AppGID, Running: true, Autostart: true}, Node: enrollmentNodeBinding{Device: 1, Inode: 2, UID: m.AppUID, GID: m.AppGID, Mode: 0700}, Digest: strings.Repeat("e", 64), Target: *target}
	j := enrollmentJournalBinding{enrollmentFenceBinding: f, Phase: "fencing", CoreHash: target.CoreHash, SignerHash: target.SignerHash, Network: target.Network}
	o.enrollmentJournal = func() ([]byte, error) { return encoded(j), nil }
	o.custodyTarget = func(int) (custodyinstall.Manifest, error) { return *target, nil }
	o.enrolledTarget = func(string, int) (custodyinstall.Manifest, error) {
		if form != "complete" {
			return custodyinstall.Manifest{}, errors.New("not completed")
		}
		return *target, nil
	}
	path := m.Data
	if form == "sibling" || form == "empty-sibling" || form == "archived" {
		path = m.Root + "/.enrollment-" + f.ID
	}
	h.files[path] = hostFile{UID: 0, GID: 0, Mode: os.ModeDir | 0755, Directory: true}
	if form != "empty-sibling" {
		h.files[path+"/enrollment.json"] = hostFile{UID: 0, GID: 0, Mode: 0644, Regular: true, Data: encoded(f)}
		h.files[path+"/.original"] = hostFile{UID: 0, GID: 0, Mode: os.ModeDir | 0700, Directory: true}
	}
	if form == "complete" {
		h.files[path+"/ready.json"] = hostFile{UID: 0, GID: 0, Mode: 0644, Regular: true, Data: []byte(`{"version":1,"binding":"fixture verified by pinned consumer"}`)}
	}
	if form == "archived" {
		delete(h.files, m.Data)
	}
	return j, target
}

func TestRemoteEnrollmentLeaseResumesOnlyBoundFences(t *testing.T) {
	for _, form := range []string{"complete", "published", "sibling", "empty-sibling", "archived"} {
		t.Run(form, func(t *testing.T) {
			h, o, m := installFixture(t)
			_, target := attachEnrollmentFixture(h, &o, m, form)
			if err := verifyFiles(o, m); err == nil {
				t.Fatal("ordinary data/inventory guard was weakened")
			}
			l, err := enrollmentLease(t.Context(), o, m)
			if err != nil {
				t.Fatal("bound enrollment retry refused", err)
			}
			if err = l.Close(); err != nil {
				t.Fatal(err)
			}
			// Verified executable updates preserve the stable enrollment identity.
			target.CoreHash, target.SignerHash = strings.Repeat("8", 64), strings.Repeat("9", 64)
			l, err = enrollmentLease(t.Context(), o, m)
			if err != nil {
				t.Fatal("verified binary hash update broke source enrollment", err)
			}
			_ = l.Close()
		})
	}
}

func TestRemoteEnrollmentFactoryLoadAndStatus(t *testing.T) {
	for _, form := range []string{"ordinary", "complete", "published", "sibling", "empty-sibling", "archived"} {
		t.Run(form, func(t *testing.T) {
			h, o, m := installFixture(t)
			if form != "ordinary" {
				attachEnrollmentFixture(h, &o, m, form)
			}
			got, err := load(o, m.OwnerUID)
			if err != nil || got != m {
				t.Fatal("factory load blocked its guarded enrollment lease", err)
			}
			st, err := enrollmentStatus(t.Context(), o, m)
			if err != nil {
				t.Fatal("factory status blocked an owned enrollment retry", err)
			}
			if form == "ordinary" {
				if st.State != "inactive" {
					t.Fatal("ordinary runtime status changed", st)
				}
			} else {
				want := "enrollment-pending"
				if form == "complete" {
					want = "enrolled"
				}
				if !st.Installed || st.Unit != m.Unit || st.State != want || st.Active || st.URL != "" || st.InstanceID != "" {
					t.Fatal("metadata status fabricated a connected runtime", st)
				}
				if _, err = status(t.Context(), o, m); err == nil {
					t.Fatal("ordinary mutation/runtime guard was bypassed")
				}
			}
		})
	}
}

func TestRemoteEnrollmentRefusesForeignMetadata(t *testing.T) {
	for _, mode := range []string{"owner", "source-uid", "source-origin", "target-uid", "extra-sibling", "foreign-fence", "foreign-child", "missing-journal", "bad-phase", "unclassified-root", "bad-code"} {
		t.Run(mode, func(t *testing.T) {
			h, o, m := installFixture(t)
			j, target := attachEnrollmentFixture(h, &o, m, "published")
			switch mode {
			case "owner":
				j.Owner++
			case "source-uid":
				j.Source.UID++
			case "source-origin":
				j.Source.PublicOrigin = "https://different.owner.ts.net"
			case "target-uid":
				target.App.UID = m.AppUID
			case "extra-sibling":
				h.files[m.Root+"/.enrollment-"+strings.Repeat("f", 32)] = hostFile{UID: 0, GID: 0, Mode: os.ModeDir | 0755, Directory: true}
			case "foreign-fence":
				v := h.files[m.Data+"/enrollment.json"]
				v.Data = []byte(`{"foreign":true}`)
				h.files[m.Data+"/enrollment.json"] = v
			case "foreign-child":
				h.files[m.Data+"/foreign"] = hostFile{UID: 0, GID: 0, Mode: 0644, Regular: true, Data: []byte("preserve")}
			case "missing-journal":
				o.enrollmentJournal = func() ([]byte, error) { return nil, os.ErrNotExist }
			case "bad-phase":
				j.Phase = "prepared"
			case "unclassified-root":
				h.files[m.Root+"/foreign"] = hostFile{UID: 0, GID: 0, Mode: 0644, Regular: true, Data: []byte("preserve")}
			case "bad-code":
				v := h.files[m.Core]
				v.Data = []byte("changed executable")
				h.files[m.Core] = v
			}
			if mode != "missing-journal" {
				o.enrollmentJournal = func() ([]byte, error) { return encoded(j), nil }
			}
			before := len(h.calls)
			if _, err := load(o, m.OwnerUID); err == nil {
				t.Fatal("factory load adopted foreign enrollment state")
			}
			if _, err := enrollmentStatus(t.Context(), o, m); err == nil {
				t.Fatal("factory status adopted foreign enrollment state")
			}
			if l, err := enrollmentLease(t.Context(), o, m); err == nil {
				l.Close()
				t.Fatal("foreign enrollment state accepted")
			}
			if len(h.calls) != before {
				t.Fatal("refusal mutated host or queried foreign account")
			}
		})
	}
}

func TestRemoteEnrollmentNetworkMustBeSettled(t *testing.T) {
	for _, mode := range []string{"none", "pending", "expired", "committed", "rolled-back", "pointer-only"} {
		t.Run(mode, func(t *testing.T) {
			h, n, m := networkFixture(t)
			if mode != "none" {
				st, err := n.begin(t.Context(), m, NetworkApproval{PrivateUI: true, ConsoleRecovery: true})
				if err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "expired":
					h.clock = h.clock.Add(10 * time.Minute)
				case "committed":
					h.proof.SessionID += "-different"
					if _, err = n.confirm(t.Context(), m, st.AttemptID); err != nil {
						t.Fatal(err)
					}
				case "rolled-back":
					if _, err = n.rollback(t.Context(), m); err != nil {
						t.Fatal(err)
					}
				case "pointer-only":
					a, err := n.load(m)
					if err != nil {
						t.Fatal(err)
					}
					delete(h.base.files, networkAttemptRoot(m, a.ID)+"/attempt.json")
				}
			}
			// Use the fixture clock; no sleeps or timer/timeout changes.
			err := enrollmentNetworkSettledAt(t.Context(), n.ops, m, n.now)
			allowed := mode == "none" || mode == "committed" || mode == "rolled-back"
			if (err == nil) != allowed {
				t.Fatal("unexpected network admission", mode, err)
			}
		})
	}
}

func TestRemoteEnrollmentNetworkRecoveryPreservesPolicyBinding(t *testing.T) {
	for _, form := range []string{"complete", "published"} {
		t.Run(form, func(t *testing.T) {
			h, n, m := networkFixture(t)
			st, err := n.begin(t.Context(), m, NetworkApproval{PrivateUI: true, ConsoleRecovery: true})
			if err != nil {
				t.Fatal(err)
			}
			h.proof.SessionID += "-different"
			if _, err = n.confirm(t.Context(), m, st.AttemptID); err != nil {
				t.Fatal(err)
			}
			attachEnrollmentFixture(h.base, &n.ops, m, form)
			before := bytes.Clone(h.tables["foreign"])
			if _, err = n.inspect(t.Context(), m); err != nil {
				t.Fatal("inspection unavailable after source fencing", err)
			}
			if form == "published" {
				if _, err = n.begin(t.Context(), m, NetworkApproval{PrivateUI: true, ConsoleRecovery: true}); err == nil {
					t.Fatal("pending enrollment presented as completed private application")
				}
			}
			st, err = n.rollback(t.Context(), m)
			if err != nil || st.Phase != "rolled-back" || !bytes.Equal(before, h.tables["foreign"]) {
				t.Fatal("rollback unavailable or foreign policy changed", st, err)
			}
		})
	}
}

func TestRemoteEnrolledNetworkProofBindsInstanceAndOwner(t *testing.T) {
	m := Manifest{OwnerUID: 1000, Listen: "127.0.0.1:17445", PublicOrigin: "https://controlled.owner.ts.net"}
	identity := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	for _, mode := range []string{"good", "wrong-loopback", "wrong-private", "wrong-owner"} {
		t.Run(mode, func(t *testing.T) {
			client := &http.Client{Transport: networkRoundTrip(func(r *http.Request) (*http.Response, error) {
				body := []byte(`{"claimed":true}`)
				if r.URL.Path == "/api/v1/health" {
					h := core.Health{Status: "ok", APIVersion: core.APIVersion, InstanceID: identity, Connection: &core.ConnectionInfo{Location: "remote", Transport: "private-https", OwnerUID: m.OwnerUID}}
					if mode == "wrong-loopback" && r.URL.Scheme == "http" || mode == "wrong-private" && r.URL.Scheme == "https" {
						h.InstanceID = strings.Repeat("a", 43)
					}
					if mode == "wrong-owner" {
						h.Connection.OwnerUID++
					}
					body, _ = json.Marshal(h)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
			})}
			_, err := checkPrivateApplicationBound(context.Background(), m, client, identity)
			if (err == nil) != (mode == "good") {
				t.Fatal("private proof lost enrolled identity", mode, err)
			}
		})
	}
}

func TestRemoteEnrollmentLostLockKeepsOnlyNetworkRecovery(t *testing.T) {
	h, o, m := installFixture(t)
	j, _ := attachEnrollmentFixture(h, &o, m, "published")
	j.Phase = "source-lock-lost"
	o.enrollmentJournal = func() ([]byte, error) { return encoded(j), nil }
	if err := verifyNetworkInstallation(t.Context(), o, m, true); err != nil {
		t.Fatal("held source blocked network recovery", err)
	}
	if err := verifyNetworkInstallation(t.Context(), o, m, false); err == nil {
		t.Fatal("held source allowed new network activation")
	}
	status, err := enrollmentStatus(t.Context(), o, m)
	if err != nil || status.Active || status.URL != "" || status.InstanceID != "" {
		t.Fatal("held source claimed a running app", status, err)
	}
}
