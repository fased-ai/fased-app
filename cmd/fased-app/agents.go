package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

func agentsCommand(args []string, in *os.File, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("agents", flag.ContinueOnError)
	flags.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := flags.String("data-dir", base, "private instance directory")
	profile := flags.String("profile", "", "profile ID for scoped reading")
	search := flags.String("search", "", "search this profile's conversations, messages and completed AI questions/answers")
	memorySearch := flags.String("memory-search", "", "read-only search of current profile memory text and source (200 bytes maximum)")
	memorySource := flags.String("memory-source", "", "inspect one profile memory's retained chat evidence without writing or fetching")
	memoryRestore := flags.String("memory-restore-draft", "", "review one profile memory's retained revision as an inert correction command")
	memoryRevision := flags.Int("memory-revision", 0, "zero-based retained revision for --memory-restore-draft; explicit selection required")
	playbookDraft := flags.String("playbook-draft", "", "inspect a profile-owned playbook version and inert run-review command")
	playbookVersion := flags.Int("playbook-version", 0, "saved version for --playbook-draft; explicit selection required")
	draft := flags.String("ai-draft", "", "read a saved manual AI request as editable settings; no write, consent or execution")
	reminderDraft := flags.String("reminder-draft", "", "inspect a saved plain reminder as a transient editable draft; no task or run is created")
	evidence := flags.String("evidence", "", "read-only inspection: memory, headline or reminder-run")
	evidenceID := flags.String("evidence-id", "", "one saved source identity")
	evidenceItem := flags.String("evidence-item", "", "item identity for headline inspection only")
	history := flags.Bool("history", false, "read immutable completed reminder history; no execution authority")
	historyKind := flags.String("history-kind", "tasks", "tasks, conversations or memories")
	historyQuery := flags.String("history-search", "", "search archived task text or conversation questions/answers")
	historyTask := flags.String("history-task", "", "inspect/export one archived task with --history")
	before := flags.Int64("before", 0, "read the next --history page before this archive row")
	change := flags.Bool("request-stdin", false, "read one revision-checked local agent command JSON (including chat-review/chat-allow/chat-send/chat-revoke and ai-prepare memorySelection (relevant/all/none/selected IDs), news-item-explain, task-run-explain, memory-chat-save and ai-reminder-review/create); no credentials")
	if e = flags.Parse(args); e != nil {
		return e
	}
	memoryMode, sourceSet, chatSearchSet, draftSet := false, false, false, false
	playbookSet, playbookVersionSet := false, false
	reminderDraftSet := false
	restoreSet, memoryRevisionSet := false, false
	evidenceMode, evidenceItemSet := false, false
	flags.Visit(func(f *flag.Flag) {
		playbookSet = playbookSet || f.Name == "playbook-draft"
		playbookVersionSet = playbookVersionSet || f.Name == "playbook-version"
		restoreSet = restoreSet || f.Name == "memory-restore-draft"
		memoryRevisionSet = memoryRevisionSet || f.Name == "memory-revision"
		if f.Name == "memory-search" || f.Name == "memory-source" {
			memoryMode = true
		}
		evidenceMode = evidenceMode || f.Name == "evidence" || f.Name == "evidence-id" || f.Name == "evidence-item"
		evidenceItemSet = evidenceItemSet || f.Name == "evidence-item"
		draftSet = draftSet || f.Name == "ai-draft"
		reminderDraftSet = reminderDraftSet || f.Name == "reminder-draft"
		sourceSet = sourceSet || f.Name == "memory-source"
		chatSearchSet = chatSearchSet || f.Name == "search"
	})
	if playbookSet || playbookVersionSet {
		if !playbookSet || !playbookVersionSet || *playbookDraft == "" || *playbookVersion < 1 || *playbookVersion > 8 || *profile == "" || *change || restoreSet || memoryRevisionSet || reminderDraftSet || evidenceMode || memoryMode || draftSet || chatSearchSet {
			return errors.New("use --profile PROFILE --playbook-draft ID --playbook-version VERSION without other read modes or writes")
		}
	}
	if restoreSet || memoryRevisionSet {
		if !restoreSet || !memoryRevisionSet || *profile == "" || *memoryRestore == "" || *memoryRevision < 0 || *memoryRevision >= 8 || *change || reminderDraftSet || evidenceMode || memoryMode || draftSet || chatSearchSet {
			return errors.New("use --profile PROFILE --memory-restore-draft MEMORY_ID --memory-revision INDEX (0–7) without other read modes or writes")
		}
	}
	if reminderDraftSet && (*profile == "" || *reminderDraft == "" || *change || evidenceMode || memoryMode || draftSet || chatSearchSet) {
		return errors.New("use --profile PROFILE --reminder-draft TASK_ID without other read modes or writes")
	}
	if evidenceMode && (*profile == "" || *evidenceID == "" || (*evidence != "memory" && *evidence != "headline" && *evidence != "reminder-run") || (*evidence == "headline" && *evidenceItem == "") || (*evidence != "headline" && evidenceItemSet) || *change || memoryMode || draftSet || chatSearchSet) {
		return errors.New("use --profile PROFILE --evidence memory|headline|reminder-run --evidence-id ID; --evidence-item is required only for headline; no search/draft/write flags")
	}
	if draftSet && (*draft == "" || *profile == "" || *change || memoryMode || chatSearchSet) {
		return errors.New("use --profile PROFILE --ai-draft REQUEST_ID without search or writes")
	}
	if memoryMode && (*change || *profile == "" || chatSearchSet || (sourceSet && *memorySource == "")) {
		return errors.New("use --profile with --memory-search and/or a nonempty --memory-source; do not combine with chat search or writes")
	}
	if flags.NArg() != 0 || (*change && (*profile != "" || *search != "")) {
		return errors.New("use scoped reading or one command from stdin")
	}
	if *history || *historyTask != "" || *before != 0 || *historyKind != "tasks" || *historyQuery != "" {
		if !*history || (*historyKind != "tasks" && *historyKind != "conversations" && *historyKind != "memories") || *profile == "" || *before < 0 || (*before != 0 && *historyTask != "") || *change || memoryMode || evidenceMode || draftSet || reminderDraftSet || restoreSet || playbookSet || chatSearchSet {
			return errors.New("use --profile PROFILE --history with optional --before CURSOR or --history-task ID")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, e := core.NewClient(ctx, *dir)
	if e != nil {
		return e
	}
	defer c.Close()
	if e = c.LoadSession(ctx, *dir); e != nil {
		return e
	}
	if *history {
		q := url.Values{"profile": {*profile}, "history": {*historyKind}}
		if *historyQuery != "" {
			q.Set("history-q", *historyQuery)
		}
		if *before != 0 {
			q.Set("before", strconv.FormatInt(*before, 10))
		}
		if *historyTask != "" {
			q.Set("history-task", *historyTask)
		}
		var v core.TaskHistoryView
		if e = c.Do(ctx, http.MethodGet, "/api/v1/agents?"+q.Encode(), nil, &v); e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	}
	if playbookSet {
		q := url.Values{"profile": {*profile}, "playbook-draft": {*playbookDraft}, "playbook-version": {strconv.Itoa(*playbookVersion)}}
		var v core.AgentPlaybookDraft
		if e = c.Do(ctx, http.MethodGet, "/api/v1/agents?"+q.Encode(), nil, &v); e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	}
	if restoreSet {
		q := url.Values{"profile": {*profile}, "memory-restore-draft": {*memoryRestore}, "memory-revision": {strconv.Itoa(*memoryRevision)}}
		var v core.AgentMemoryRestoreDraft
		if e = c.Do(ctx, http.MethodGet, "/api/v1/agents?"+q.Encode(), nil, &v); e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	}
	if reminderDraftSet {
		q := url.Values{"profile": {*profile}, "reminder-draft": {*reminderDraft}}
		var v core.TaskReminderReview
		if e = c.Do(ctx, http.MethodGet, "/api/v1/agents?"+q.Encode(), nil, &v); e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	}
	if evidenceMode {
		q := url.Values{"profile": {*profile}, "evidence": {*evidence}, "evidence-id": {*evidenceID}}
		if evidenceItemSet {
			q.Set("evidence-item", *evidenceItem)
		}
		var v core.AgentEvidenceView
		if e = c.Do(ctx, http.MethodGet, "/api/v1/agents?"+q.Encode(), nil, &v); e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	}
	if draftSet {
		q := url.Values{"profile": {*profile}, "ai-draft": {*draft}}
		var v core.AgentAIRequestDraft
		if e = c.Do(ctx, http.MethodGet, "/api/v1/agents?"+q.Encode(), nil, &v); e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	}
	if memoryMode {
		q := url.Values{"profile": {*profile}, "memory-search": {*memorySearch}}
		if sourceSet {
			q.Set("memory-source", *memorySource)
		}
		var v core.AgentMemoryView
		if e = c.Do(ctx, http.MethodGet, "/api/v1/agents?"+q.Encode(), nil, &v); e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	}
	var v core.AgentView
	if *change {
		b, e := io.ReadAll(io.LimitReader(in, 16385))
		if e != nil {
			return e
		}
		if len(b) > 16384 {
			return errors.New("oversized agent command")
		}
		var command core.AgentCommand
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&command) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("one strict agent command required; credentials are forbidden")
		}
		e = c.Do(ctx, http.MethodPost, "/api/v1/agents", command, &v)
		if e != nil {
			return e
		}
	} else {
		q := url.Values{}
		if *profile != "" {
			q.Set("profile", *profile)
		}
		if *search != "" {
			q.Set("q", *search)
		}
		if e = c.Do(ctx, http.MethodGet, "/api/v1/agents?"+q.Encode(), nil, &v); e != nil {
			return e
		}
	}
	return json.NewEncoder(out).Encode(v)
}
