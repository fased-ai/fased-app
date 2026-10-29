//go:build linux

package enrollment

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func fixtureImage(t *testing.T) core.EnrollmentImage {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	image, lock, err := core.ExportEnrollmentImage(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	return image
}

type fixtureHost struct {
	image                                                                 core.EnrollmentImage
	j                                                                     *journal
	steps                                                                 []string
	fail                                                                  int
	before, tripped                                                       bool
	prepared, held, archived, fenced, staged, published, activated, ready bool
	starts, restores                                                      int
	running, autostart                                                    bool
	signerForeign, sourceBusy, wrongSource, wrongStage                    bool
	n                                                                     node
	m                                                                     custodyinstall.Manifest
}

func fixture(t *testing.T, image core.EnrollmentImage, profile string) (*fixtureHost, Source, Options, operations) {
	t.Helper()
	owner := os.Getuid()
	h := &fixtureHost{image: image, running: true, autostart: true, n: node{1, 2, owner, os.Getgid(), 0700}}
	h.m = custodyinstall.Manifest{Version: 2, Mode: "disposable-custody:v2", Network: "devnet", Enrollment: true, Location: profile, Owner: owner, ID: strings.Repeat("a", 32), Root: custodyinstall.Root(owner), Listen: "127.0.0.1:17447", CoreHash: strings.Repeat("b", 64), SignerHash: strings.Repeat("c", 64), App: custodyinstall.Identity{Name: "fixture-app", UID: owner + 10000, GID: owner + 10000}, Signer: custodyinstall.Identity{Name: "fixture-signer", UID: owner + 10001, GID: owner + 10001}}
	if profile == "remote" {
		h.m.PublicOrigin = "https://controlled.owner.ts.net"
	}
	s := Source{Profile: profile, Data: filepath.Join(t.TempDir(), "source"), Listen: h.m.Listen, PublicOrigin: h.m.PublicOrigin, ExpectedInstanceID: image.InstanceID, UID: owner, GID: os.Getgid(), Running: true, Autostart: true}
	s.Validate = func(context.Context) error {
		if h.archived || h.wrongSource {
			return errState
		}
		return h.step("validate-source", nil)
	}
	s.Suspend = func(context.Context) error {
		return h.step("suspend", func() { h.running = false; h.autostart = false })
	}
	s.Restore = func(_ context.Context, running, autostart bool) error {
		h.restores++
		if h.activated {
			t.Fatal("source restored after possible target activation")
		}
		h.running, h.autostart = running, autostart
		return nil
	}
	o := Options{Core: "inert-core", Signer: "inert-signer", Network: "devnet", PrepareHost: true, Disposable: true}
	op := operations{
		owner: owner,
		load: func() (journal, error) {
			if h.j == nil {
				return journal{}, os.ErrNotExist
			}
			return *h.j, nil
		},
		save: func(j journal) error { return h.step("save:"+j.Phase, func() { copy := j; h.j = &copy }) },
		node: func(string) (node, error) {
			if h.archived {
				if h.fenced {
					return node{1, 3, 0, 0, 0755}, nil
				}
				return node{}, os.ErrNotExist
			}
			if h.wrongSource {
				return node{1, 77, owner, os.Getgid(), 0700}, nil
			}
			return h.n, nil
		},
		targetExists: func() bool { return h.prepared },
		prepare: func(context.Context, journal, Options) (custodyinstall.Manifest, error) {
			err := h.step("prepare-target", func() { h.prepared = true })
			return h.m, err
		},
		target: func(m custodyinstall.Manifest) error {
			if !h.prepared || m != h.m {
				return errState
			}
			return nil
		},
		emptySigner: func(context.Context, custodyinstall.Manifest) error {
			if h.signerForeign {
				return errState
			}
			return h.step("empty-signer", nil)
		},
		export: func(context.Context, custodyinstall.Manifest, sourceRecord) (*imageLease, error) {
			if h.running || h.sourceBusy || h.archived {
				return nil, errors.New("fixture source lock busy")
			}
			if err := h.step("export-source", func() { h.held = true }); err != nil {
				h.held = false
				return nil, err
			}
			return &imageLease{image: h.image, check: func() error {
				if !h.held {
					return errors.New("fixture helper exited")
				}
				return nil
			}, close: func() error { h.held = false; return nil }, release: func() error {
				if !h.fenced {
					t.Fatal("source lock released before fence")
				}
				h.held = false
				return h.step("release-source", nil)
			}}, nil
		},
		importImage: func(_ context.Context, j journal, image core.EnrollmentImage) error {
			if !h.held || h.activated || j.Digest != core.EnrollmentImageDigest(image) || h.wrongStage {
				return errState
			}
			return h.step("import-stage", func() { h.staged = true })
		},
		fence: func(context.Context, journal) error {
			if !h.archived {
				if !h.held {
					t.Fatal("live source archived without its lock")
				}
				if err := h.step("archive-source", func() { h.archived = true }); err != nil {
					return err
				}
			}
			return h.step("publish-fence", func() { h.fenced = true })
		},
		publish: func(context.Context, journal) error {
			if !h.fenced || !h.staged && !h.published {
				return errState
			}
			return h.step("publish-target", func() { h.published = true; h.staged = false })
		},
		verifyTarget: func(context.Context, journal) error {
			if !h.published || !h.fenced || h.wrongStage {
				return errState
			}
			return h.step("verify-target", nil)
		},
		activate: func(_ context.Context, j journal) error {
			if h.j.Phase != "activation-requested" || !h.fenced || !h.published || h.running || h.held || j.Source.ExpectedInstanceID != h.image.InstanceID || !j.Source.Running || !j.Source.Autostart {
				t.Fatal("activation escaped durable commit or original intent")
			}
			return h.step("activate", func() {
				if !h.activated {
					h.starts++
				}
				h.activated = true
			})
		},
		finish: func(context.Context, journal) error {
			if !h.activated || !h.fenced {
				return errState
			}
			return h.step("finish", func() { h.ready = true })
		},
	}
	return h, s, o, op
}

func (h *fixtureHost) step(name string, effect func()) error {
	h.steps = append(h.steps, name)
	fail := !h.tripped && len(h.steps) == h.fail
	if fail && h.before {
		h.tripped = true
		return errors.New("disposable interruption before " + name)
	}
	if effect != nil {
		effect()
	}
	if fail {
		h.tripped = true
		return errors.New("disposable interruption after " + name)
	}
	return nil
}

func TestEnrollmentControllerConnectedProfiles(t *testing.T) {
	image := fixtureImage(t)
	for _, profile := range []string{"local", "remote"} {
		t.Run(profile, func(t *testing.T) {
			h, s, o, op := fixture(t, image, profile)
			m, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op)
			if err != nil || m != h.m || !h.ready || h.starts != 1 || h.running || h.autostart || h.held || h.j.Phase != "complete" || h.j.Source.ExpectedInstanceID != image.InstanceID {
				t.Fatal("connected enrollment failed", err)
			}
			if m.Location != profile || m.PublicOrigin != s.PublicOrigin || m.Listen != s.Listen {
				t.Fatal("profile listener/origin lost")
			}
			s.Running, s.Autostart = false, false
			if _, err = run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err != nil || h.starts != 1 {
				t.Fatal("completed retry restarted an engine", err)
			}
		})
	}
}

func TestEnrollmentControllerInterruptionRecovery(t *testing.T) {
	image := fixtureImage(t)
	h, s, o, op := fixture(t, image, "remote")
	if _, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err != nil {
		t.Fatal(err)
	}
	count := len(h.steps)
	for _, before := range []bool{true, false} {
		for fail := 1; fail <= count; fail++ {
			t.Run(strconv.Itoa(fail)+"-"+strconv.FormatBool(before), func(t *testing.T) {
				h, s, o, op := fixture(t, image, "remote")
				h.fail, h.before = fail, before
				_, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op)
				if err == nil || !h.tripped {
					t.Fatal("fault did not interrupt", fail)
				}
				if h.held {
					t.Fatal("failed controller leaked source lock")
				}
				s.Running, s.Autostart = h.running, h.autostart
				if _, err = run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err != nil {
					t.Fatal("retry failed", err, h.j)
				}
				if !h.ready || !h.fenced || !h.archived || h.starts != 1 || h.running || h.autostart || h.held || h.j.Phase != "complete" {
					t.Fatal("interruption lost original or duplicated engine")
				}
			})
		}
	}
}

func TestEnrollmentControllerPreservesForeignState(t *testing.T) {
	image := fixtureImage(t)
	for _, mode := range []string{"target", "signer", "source", "source-lock", "stage", "consent"} {
		t.Run(mode, func(t *testing.T) {
			h, s, o, op := fixture(t, image, "local")
			switch mode {
			case "target":
				h.prepared = true
			case "signer":
				h.signerForeign = true
			case "source":
				h.wrongSource = true
			case "source-lock":
				h.sourceBusy = true
			case "stage":
				h.wrongStage = true
			case "consent":
				o.Disposable = false
			}
			if _, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err == nil {
				t.Fatal("foreign state accepted")
			}
			if h.archived || h.published || h.activated || h.starts != 0 || h.held {
				t.Fatal("refusal crossed cutover")
			}
		})
	}
}

func TestEnrollmentAtomicDirectoryPublication(t *testing.T) {
	for _, nonempty := range []bool{false, true} {
		t.Run(strconv.FormatBool(nonempty), func(t *testing.T) {
			root := t.TempDir()
			stage, target := filepath.Join(root, "stage"), filepath.Join(root, "app-state")
			if err := os.Mkdir(stage, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stage, "core.sqlite"), []byte("complete disposable image"), 0600); err != nil {
				t.Fatal(err)
			}
			if nonempty {
				if err := os.WriteFile(filepath.Join(target, "foreign"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := exec.CommandContext(t.Context(), "/usr/bin/mv", "--no-target-directory", "--", stage, target).Run()
			if nonempty {
				if err == nil {
					t.Fatal("nonempty destination replaced")
				}
				if _, err = os.Stat(filepath.Join(target, "foreign")); err != nil {
					t.Fatal("foreign data changed")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = os.Stat(filepath.Join(target, "core.sqlite")); err != nil {
					t.Fatal("complete image not published")
				}
				if _, err = os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("stage was not atomically renamed")
				}
			}
		})
	}
}

func TestEnrollmentLostSourceLockNeverFencesOrActivates(t *testing.T) {
	image := fixtureImage(t)
	for _, point := range []string{"import", "fence"} {
		t.Run(point, func(t *testing.T) {
			h, s, o, op := fixture(t, image, "local")
			if point == "import" {
				original := op.importImage
				op.importImage = func(ctx context.Context, j journal, image core.EnrollmentImage) error {
					if err := original(ctx, j, image); err != nil {
						return err
					}
					h.held = false
					return nil
				}
			} else {
				original := op.fence
				op.fence = func(ctx context.Context, j journal) error {
					if err := original(ctx, j); err != nil {
						return err
					}
					h.held = false
					return nil
				}
			}
			if _, err := run(t.Context(), s, o, strings.Repeat("b", 64), strings.Repeat("c", 64), op); err == nil || h.activated {
				t.Fatal("lost helper activated old image", err)
			}
			if point == "import" && h.fenced {
				t.Fatal("lost source lock fenced original")
			}
			if point == "fence" {
				if h.j.Phase != "source-lock-lost" {
					t.Fatal(h.j.Phase)
				}
				if _, err := run(t.Context(), s, o, strings.Repeat("b", 64), strings.Repeat("c", 64), op); err == nil || h.activated {
					t.Fatal("ambiguous lock loss silently resumed")
				}
			}
		})
	}
}
