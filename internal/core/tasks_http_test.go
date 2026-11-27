package core

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fased-ai/fased-app/internal/tasks"
)

func TestUniversalTaskEngineCoreRoutesWSKillAndAgentDraft(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Close(ctx)
		<-serveDone
	})

	token := claimFixture(t, srv.Store)
	h := srv.HTTP.Handler.(*HTTPHandler)
	csrf := csrfToken(token)

	// 1. Optional SQLite path registration preserves clean backup/migration detection.
	if !slices.Contains(OptionalStateSQLitePaths(), "tasks/tasks.sqlite") {
		t.Fatalf("missing tasks/tasks.sqlite in OptionalStateSQLitePaths(): %v", OptionalStateSQLitePaths())
	}

	// 2. Libraries endpoint exposes seeded personas, skills, and ready-made templates.
	libResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/tasks/libraries", "", token, "", "")
	if libResp.Code != http.StatusOK || !strings.Contains(libResp.Body.String(), "tpl-contrarian-ev-miner") {
		t.Fatalf("GET /api/v1/tasks/libraries failed (%d): %s", libResp.Code, libResp.Body.String())
	}

	// 3. Instantiate a draft task, execute run-now, and verify runs + logs endpoints.
	instResp := stage4Req(
		h, http.MethodPost, srv.URL,
		"/api/v1/tasks/from-template",
		`{"templateId":"tpl-news-sentinel","overrides":{"name":"Live Macro Scan","status":"draft"}}`,
		token, srv.URL, csrf,
	)
	if instResp.Code != http.StatusCreated {
		t.Fatalf("instantiate failed (%d): %s", instResp.Code, instResp.Body.String())
	}
	var instWrap struct {
		Task tasks.Task `json:"task"`
	}
	if err := json.Unmarshal(instResp.Body.Bytes(), &instWrap); err != nil || instWrap.Task.ID == "" {
		t.Fatalf("decode instantiated task: %v body=%s", err, instResp.Body.String())
	}
	created := instWrap.Task

	runResp := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/tasks/"+created.ID+"/run", `{}`, token, srv.URL, csrf)
	if runResp.Code != http.StatusOK {
		t.Fatalf("run failed (%d): %s", runResp.Code, runResp.Body.String())
	}
	runsResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/tasks/"+created.ID+"/runs?limit=5", "", token, "", "")
	if runsResp.Code != http.StatusOK || !strings.Contains(runsResp.Body.String(), created.ID) {
		t.Fatalf("GET runs failed (%d): %s", runsResp.Code, runsResp.Body.String())
	}
	logsResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/tasks/"+created.ID+"/logs?limit=10", "", token, "", "")
	if logsResp.Code != http.StatusOK {
		t.Fatalf("GET logs failed (%d): %s", logsResp.Code, logsResp.Body.String())
	}

	// 4. Swarm DAG creation builds parent->child edge.
	swarmResp := stage4Req(
		h, http.MethodPost, srv.URL,
		"/api/v1/tasks/swarm",
		`{
			"name":"News Macro Trigger",
			"goalDomain":"news",
			"goalObjective":"Monitor macro catalysts and notify downstream",
			"scheduleKind":"interval",
			"scheduleSpec":{"intervalSec":300},
			"approvalPolicy":"manual",
			"children":[{"name":"Downstream Notify","goalDomain":"notifications","execMode":"standalone","condition":"on_signal"}]
		}`,
		token, srv.URL, csrf,
	)
	if swarmResp.Code != http.StatusCreated || !strings.Contains(swarmResp.Body.String(), "Downstream Notify") {
		t.Fatalf("POST /api/v1/tasks/swarm failed (%d): %s", swarmResp.Code, swarmResp.Body.String())
	}

	// 5. Step-up gate enforced when activating auto+wallet task.
	autoTaskResp := stage4Req(
		h, http.MethodPost, srv.URL,
		"/api/v1/tasks",
		`{"name":"WEN EV Miner","goalDomain":"mining","scheduleKind":"interval","scheduleSpec":{"intervalSec":60},"execMode":"standalone","approvalPolicy":"auto","executionBinding":{"walletHandle":"@wallet:wal-mining-1"},"status":"draft"}`,
		token, srv.URL, csrf,
	)
	if autoTaskResp.Code != http.StatusCreated {
		t.Fatalf("create auto draft failed (%d): %s", autoTaskResp.Code, autoTaskResp.Body.String())
	}
	var autoWrap struct {
		Task tasks.Task `json:"task"`
	}
	_ = json.Unmarshal(autoTaskResp.Body.Bytes(), &autoWrap)
	autoTask := autoWrap.Task

	unstepped := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/tasks/"+autoTask.ID+"/activate", `{}`, token, srv.URL, csrf)
	if unstepped.Code == http.StatusOK {
		t.Fatalf("expected step-up rejection activating auto+wallet task, got 200: %s", unstepped.Body.String())
	}

	stepBody, _ := json.Marshal(map[string]string{
		"password": testPassword,
		"action":   "task-activate",
		"walletId": autoTask.ID,
	})
	stepResp := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/auth/step-up", string(stepBody), token, srv.URL, csrf)
	if stepResp.Code != http.StatusOK {
		t.Fatalf("step-up failed (%d): %s", stepResp.Code, stepResp.Body.String())
	}
	var stepOut struct {
		StepUpToken string `json:"stepUpToken"`
	}
	_ = json.Unmarshal(stepResp.Body.Bytes(), &stepOut)
	stepped := stage4ReqWithStepUp(h, http.MethodPost, srv.URL, "/api/v1/tasks/"+autoTask.ID+"/activate", `{}`, token, srv.URL, csrf, stepOut.StepUpToken)
	if stepped.Code != http.StatusOK || !strings.Contains(stepped.Body.String(), `"status":"active"`) {
		t.Fatalf("step-up task activation failed (%d): %s", stepped.Code, stepped.Body.String())
	}

	// 6. Stream WS topics "tasks" & "task:<id>" parse and live WS receives task mutations.
	if p, err := parseStreamTopic("tasks"); err != nil || p.kind != "tasks" {
		t.Fatalf("parseStreamTopic(tasks): %+v err=%v", p, err)
	}
	if p, err := parseStreamTopic("task:" + autoTask.ID); err != nil || p.kind != "task" || p.symbol != autoTask.ID {
		t.Fatalf("parseStreamTopic(task:id): %+v err=%v", p, err)
	}

	ticketResp := doV2JSONRequest(t, http.MethodPost, srv.URL+"/api/v1/stream/ticket", srv.URL, token, csrf, `{}`, "application/json")
	var ticketPayload struct {
		Ticket string `json:"ticket"`
	}
	if ticketResp.StatusCode != http.StatusOK || json.NewDecoder(ticketResp.Body).Decode(&ticketPayload) != nil || ticketPayload.Ticket == "" {
		ticketResp.Body.Close()
		t.Fatalf("expected 200 stream ticket, got %d", ticketResp.StatusCode)
	}
	ticketResp.Body.Close()

	conn, _, err := websocket.Dial(t.Context(), httpToWSURL(srv.URL)+"/api/v1/stream", &websocket.DialOptions{
		Subprotocols: []string{"fased.stream.v1", "fased.ticket." + ticketPayload.Ticket},
		HTTPHeader:   http.Header{"Origin": []string{srv.URL}},
	})
	if err != nil {
		t.Fatalf("WS Dial failed: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	readFrame := func() streamOutboundFrame {
		t.Helper()
		rCtx, rCancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer rCancel()
		_, raw, rErr := conn.Read(rCtx)
		if rErr != nil {
			t.Fatalf("WS Read failed: %v", rErr)
		}
		var out streamOutboundFrame
		_ = json.Unmarshal(raw, &out)
		return out
	}

	if ready := readFrame(); ready.Type != "ready" {
		t.Fatalf("expected ready frame, got %+v", ready)
	}
	if err := conn.Write(t.Context(), websocket.MessageText, []byte(`{"op":"sub","topics":["tasks"]}`)); err != nil {
		t.Fatalf("write sub failed: %v", err)
	}
	if ack := readFrame(); ack.Type != "ack" || ack.Event != "sub" {
		t.Fatalf("expected sub ack, got %+v", ack)
	}

	// Trigger pause and confirm WS pushes task event.
	pauseResp := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/tasks/"+autoTask.ID+"/pause", `{"reason":"manual check"}`, token, srv.URL, csrf)
	if pauseResp.Code != http.StatusOK || !strings.Contains(pauseResp.Body.String(), `"status":"paused"`) {
		t.Fatalf("pause failed (%d): %s", pauseResp.Code, pauseResp.Body.String())
	}
	foundTaskEvent := false
	for range 6 {
		f := readFrame()
		if f.Topic == "tasks" {
			foundTaskEvent = true
			break
		}
	}
	if !foundTaskEvent {
		t.Fatal("expected WS tasks notification on pause mutation")
	}

	// 7. Global kill switch halts active tasks cleanly.
	actCreated := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/tasks/"+created.ID+"/activate", `{}`, token, srv.URL, csrf)
	if actCreated.Code != http.StatusOK {
		t.Fatalf("activate created task failed (%d): %s", actCreated.Code, actCreated.Body.String())
	}
	srv.Store.syncGlobalKill(t.Context(), true, "emergency test", "tester")
	eng, tErr := srv.Store.tasksEngine()
	if tErr != nil {
		t.Fatalf("tasksEngine: %v", tErr)
	}
	gotAfterKill, _ := eng.Store().GetTask(t.Context(), created.ID)
	if gotAfterKill.Status != tasks.StatusKilled {
		t.Fatalf("expected task killed on global kill switch, got %s", gotAfterKill.Status)
	}
	srv.Store.syncGlobalKill(t.Context(), false, "clear", "tester")

	// 8. Agent V2 draft_task_v2 preserves taint and always saves in draft status.
	taintedCtx := WithTaintState(t.Context(), true)
	res, err := executeDraftTaskV2(taintedCtx, srv.Store, "thread-1", json.RawMessage(`{"name":"Agent Drafted Scan","goalDomain":"news","scheduleKind":"interval","intervalSec":300,"execMode":"ai","approvalPolicy":"auto"}`))
	if err != nil {
		t.Fatalf("executeDraftTaskV2: %v", err)
	}
	outMap := res.(map[string]any)
	drafted := outMap["task"].(tasks.Task)
	if drafted.Status != tasks.StatusDraft || !drafted.Tainted {
		t.Fatalf("expected tainted draft status from agent tool, got status=%s tainted=%v", drafted.Status, drafted.Tainted)
	}
}
