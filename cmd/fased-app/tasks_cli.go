package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/tasks"
)

func tasksCommand(args []string, _ *os.File, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: fased-app tasks list|libraries|create|from-template|activate|pause|stop|run|logs|edges|kill-all [flags]")
	}
	sub := strings.TrimSpace(args[0])
	flags := flag.NewFlagSet("tasks "+sub, flag.ContinueOnError)
	flags.SetOutput(errOut)
	base, err := defaultDir()
	if err != nil {
		return err
	}
	dir := flags.String("data-dir", base, "private instance directory")
	goal := flags.String("goal", "", "goal domain filter or value (mining|trading|news|notifications|portfolio|research|monitoring|custom)")
	status := flags.String("status", "", "status filter")
	name := flags.String("name", "", "task display name")
	objective := flags.String("objective", "", "goal objective constraint")
	mode := flags.String("mode", "standalone", "execution mode (standalone|ai|hybrid)")
	schedule := flags.String("schedule", "interval", "schedule kind (continuous|interval|cron|once|event)")
	interval := flags.Int64("interval", 60, "interval seconds")
	cronExpr := flags.String("cron", "", "cron expression")
	approval := flags.String("approval", "manual", "approval policy (manual|semi_auto|auto)")
	wallet := flags.String("wallet", "", "wallet handle (@wallet:...)")
	venue := flags.String("venue", "", "venue/exchange/broker")
	symbol := flags.String("symbol", "", "market symbol")
	persona := flags.String("persona", "", "persona ID")
	strategy := flags.String("strategy", "", "strategy ID")
	reason := flags.String("reason", "", "pause/kill reason")
	childID := flags.String("child", "", "child task ID for edges")
	condition := flags.String("condition", "on_success", "DAG edge condition")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	rest := flags.Args()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, enrolled, err := agentsPublicClient(ctx, *dir)
	if err != nil {
		return err
	}
	defer c.Close()
	if enrolled {
		err = c.NativeLocalOwner(ctx)
	} else {
		err = c.LoadSession(ctx, *dir)
	}
	if err != nil {
		return err
	}

	switch sub {
	case "list":
		q := url.Values{}
		if *goal != "" {
			q.Set("goal", *goal)
		}
		if *status != "" {
			q.Set("status", *status)
		}
		path := "/api/v1/tasks"
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}
		var res map[string]any
		if err := c.Do(ctx, http.MethodGet, path, nil, &res); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(res)

	case "libraries":
		var res tasks.TaskLibraries
		if err := c.Do(ctx, http.MethodGet, "/api/v1/tasks/libraries", nil, &res); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(res)

	case "create":
		in := tasks.Task{
			Name:          *name,
			GoalDomain:    tasks.GoalDomain(*goal),
			GoalObjective: *objective,
			ScheduleKind:  tasks.ScheduleKind(*schedule),
			ScheduleSpec: tasks.ScheduleSpec{
				IntervalSec: *interval,
				CronExpr:    *cronExpr,
			},
			ExecMode:       tasks.ExecMode(*mode),
			ApprovalPolicy: tasks.ApprovalPolicy(*approval),
			ExecutionBinding: tasks.ExecutionBinding{
				WalletHandle: *wallet,
				Venue:        *venue,
				Symbol:       *symbol,
			},
			Attachments: tasks.TaskAttachments{
				PersonaID:  *persona,
				StrategyID: *strategy,
			},
			CreatedVia: "cli",
		}
		var res map[string]any
		if err := c.Do(ctx, http.MethodPost, "/api/v1/tasks", in, &res); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(res)

	case "from-template":
		if len(rest) < 1 {
			return errors.New("usage: fased-app tasks from-template <template-id>")
		}
		in := map[string]any{
			"templateId": rest[0],
			"overrides": tasks.Task{
				Name:       *name,
				CreatedVia: "cli",
			},
		}
		var res map[string]any
		if err := c.Do(ctx, http.MethodPost, "/api/v1/tasks/from-template", in, &res); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(res)

	case "activate", "pause", "stop", "run":
		if len(rest) < 1 {
			return fmt.Errorf("usage: fased-app tasks %s <task-id>", sub)
		}
		var body any
		if sub == "pause" && *reason != "" {
			body = map[string]string{"reason": *reason}
		}
		var res map[string]any
		if err := c.Do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(rest[0])+"/"+sub, body, &res); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(res)

	case "logs":
		if len(rest) < 1 {
			return errors.New("usage: fased-app tasks logs <task-id>")
		}
		var res map[string]any
		if err := c.Do(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(rest[0])+"/logs", nil, &res); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(res)

	case "edges":
		if len(rest) < 1 || *childID == "" {
			return errors.New("usage: fased-app tasks edges --child <child-id> [--condition on_success] <parent-id>")
		}
		in := map[string]string{
			"childTaskId": *childID,
			"condition":   *condition,
		}
		var res map[string]any
		if err := c.Do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(rest[0])+"/edges", in, &res); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(res)

	case "kill-all":
		var res map[string]any
		if err := c.Do(ctx, http.MethodPost, "/api/v1/tasks/kill-all", map[string]string{"reason": *reason}, &res); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(res)

	default:
		return fmt.Errorf("unknown tasks subcommand %q", sub)
	}
}
