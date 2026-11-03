//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func accessUpdateFixture(t *testing.T) (*remoteUpdateFixture, Manifest, accessJournal) {
	t.Helper()
	f := remoteApplicationFixture(t)
	previous := f.old
	if err := start(t.Context(), f.o, previous); err != nil {
		t.Fatal(err)
	}
	next, err := changeAccess(t.Context(), f.o, previous, "https://node.owner.ts.net", nil)
	if err != nil {
		t.Fatal(err)
	}
	var access accessJournal
	if strict(f.h.journals[accessJournalName], &access) != nil || access.Phase != "committed" {
		t.Fatal("missing committed access journal")
	}
	f.old = next
	f.c.ExpectedManifestSHA256 = ManifestSHA256(next)
	return f, previous, access
}

func TestRemoteAccessCommitAdvancesOwnerIntent(t *testing.T) {
	f, _, _ := accessUpdateFixture(t)
	var got intent
	if strict(f.h.journals["intent.json"], &got) != nil || got != intentFor(f.old) {
		t.Fatal("committed access left owner intent stale")
	}
}

func TestRemoteApplicationCompletionBridgesExactCommittedAccess(t *testing.T) {
	f, previous, _ := accessUpdateFixture(t)
	// Reproduce installations whose access operation committed before owner
	// intent synchronization existed. The update must use that exact chain.
	f.h.journals["intent.json"] = encoded(intentFor(previous))
	write := f.o.writeJournal
	cut := errors.New("interrupted held owner intent advance")
	f.o.writeJournal = func(name string, value any) error {
		if name == "intent.json" {
			return cut
		}
		return write(name, value)
	}
	if _, err := f.apply(t, false); !errors.Is(err, cut) {
		t.Fatal("committed access predecessor did not reach held completion", err)
	}
	held, err := readApplicationJournal(f.o)
	if err != nil || held.Status.Phase != "healthy-held" {
		t.Fatal("held update identity lost", held, err)
	}
	f.o.writeJournal = write
	delete(f.h.files, f.c.Engine)
	target, err := resumeRemoteApplication(t.Context(), f.o, f.old, f.c.ID, f.cb, false)
	if err != nil {
		t.Fatal("exact held completion could not catch up owner intent", err)
	}
	var got intent
	j, journalErr := readApplicationJournal(f.o)
	if strict(f.h.journals["intent.json"], &got) != nil || got != intentFor(target) || journalErr != nil || j.Status.Phase != "completed" {
		t.Fatal("owner intent did not join completed update", got, j, journalErr)
	}
}

func TestRemoteApplicationCompletionRejectsForeignAccessBridge(t *testing.T) {
	for _, kind := range []string{"prepared", "switched", "rolled-back", "id", "different-target", "foreign-intent", "missing", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			f, previous, access := accessUpdateFixture(t)
			prior := intentFor(previous)
			switch kind {
			case "prepared", "switched", "rolled-back":
				access.Phase = kind
			case "id":
				access.ID = "foreign"
			case "different-target":
				access.Next.PublicOrigin = "https://other.owner.ts.net"
			case "foreign-intent":
				prior.CoreSHA256 = strings.Repeat("b", 64)
			}
			f.h.journals["intent.json"] = encoded(prior)
			f.h.journals[accessJournalName] = encoded(access)
			if kind == "missing" {
				delete(f.h.journals, accessJournalName)
			} else if kind == "unknown" {
				f.h.journals[accessJournalName] = []byte(`{"unknown":true}`)
			}
			// Reach the held boundary using an otherwise admitted update; the
			// terminal access record is needed solely for the stale-intent bridge.
			badAccess := f.h.journals[accessJournalName]
			delete(f.h.journals, accessJournalName)
			f.cb.Complete = func(context.Context, Manifest, ApplicationUpdateStatus) error {
				if kind != "missing" {
					f.h.journals[accessJournalName] = badAccess
				}
				return nil
			}
			if _, err := f.apply(t, false); err == nil {
				t.Fatal("foreign or incomplete access chain adopted")
			}
			var got intent
			j, err := readApplicationJournal(f.o)
			if strict(f.h.journals["intent.json"], &got) != nil || got != prior || err != nil || j.Status.Phase != "healthy-held" {
				t.Fatal("rejected chain changed owner intent or lost held phase", got, j, err)
			}
		})
	}
}

func TestRemoteAccessOwnerIntentWriteInterruptionRetriesTerminalOperation(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "rolled-back"}[rollback], func(t *testing.T) {
			f := remoteApplicationFixture(t)
			if err := start(t.Context(), f.o, f.old); err != nil {
				t.Fatal(err)
			}
			write := f.o.writeJournal
			cut := errors.New("interrupted owner intent write")
			f.o.writeJournal = func(name string, value any) error {
				if name == "intent.json" {
					return cut
				}
				return write(name, value)
			}
			want := f.old
			if rollback {
				next := f.old
				next.PublicOrigin = "https://node.owner.ts.net"
				state, err := status(t.Context(), f.o, f.old)
				if err != nil {
					t.Fatal(err)
				}
				access := accessJournal{Version: 1, ID: strings.Repeat("c", 32), Phase: "rollback", InstanceID: state.InstanceID, Previous: f.old, Next: next}
				f.h.journals["intent.json"] = encoded(intentFor(next))
				if err := rollbackAccess(t.Context(), f.o, access, nil); !errors.Is(err, cut) {
					t.Fatal("rollback synchronization interruption missed", err)
				}
			} else {
				var err error
				want, err = changeAccess(t.Context(), f.o, f.old, "https://node.owner.ts.net", nil)
				if !errors.Is(err, cut) {
					t.Fatal("commit synchronization interruption missed", err)
				}
			}
			var before accessJournal
			if strict(f.h.journals[accessJournalName], &before) != nil || before.Phase != map[bool]string{false: "committed", true: "rolled-back"}[rollback] {
				t.Fatal("terminal access identity lost", before)
			}
			f.o.writeJournal = write
			got, err := changeAccess(t.Context(), f.o, want, want.PublicOrigin, nil)
			var after accessJournal
			var owner intent
			if err != nil || got != want || strict(f.h.journals[accessJournalName], &after) != nil || after != before || strict(f.h.journals["intent.json"], &owner) != nil || owner != intentFor(want) {
				t.Fatal("same terminal operation did not recover owner intent", got, after, owner, err)
			}
		})
	}
}
