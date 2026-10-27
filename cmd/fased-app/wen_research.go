package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

func wenResearchCommand(args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("wen-research", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := f.String("data-dir", base, "private core directory")
	profile := f.String("profile", "", "profile for read")
	read := f.Bool("read-stdin", false, "inspect one selected research batch without writing or calling a model")
	aiNext := f.Bool("ai-prepare-next-stdin", false, "prepare one exact reviewed research continuation without starting it")
	aiStart := f.Bool("ai-start-stdin", false, "consent to one exact reviewed fixture research child")
	aiPrepare := f.Bool("ai-prepare-stdin", false, "prepare one inert source-bound research turn")
	aiInspect := f.Bool("ai-inspect-stdin", false, "inspect one retained research turn without writing")
	aiCancel := f.Bool("ai-cancel-stdin", false, "cancel one research turn without releasing uncertain charges")
	change := f.Bool("request-stdin", false, "one revision-checked save/history command")
	review := f.Bool("review-stdin", false, "review one retained same-profile strategy/history selection without saving")
	revisionReview := f.Bool("revision-review-stdin", false, "prepare an inert strategy revision from a retained user message")
	revisionSave := f.Bool("revision-save-stdin", false, "save an exact reviewed strategy revision and source receipt")
	proposalReview := f.Bool("proposal-review-stdin", false, "review one completed manual AI strategy card without saving")
	proposalSave := f.Bool("proposal-save-stdin", false, "save an exact reviewed AI strategy revision and source receipt")
	if e = f.Parse(args); e != nil {
		return e
	}
	modes := 0
	for _, selected := range []bool{*read, *aiNext, *aiStart, *aiPrepare, *aiInspect, *aiCancel, *change, *review, *revisionReview, *revisionSave, *proposalReview, *proposalSave, *profile != ""} {
		if selected {
			modes++
		}
	}
	if f.NArg() != 0 || modes != 1 {
		return errors.New("select exactly one research read, prepare, inspect, cancel, profile, save or review mode")
	}
	timeout := 10 * time.Second
	if *read || *aiPrepare || *aiInspect || *aiNext {
		timeout = 95 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c, e := core.NewClient(ctx, *dir)
	if e != nil {
		return e
	}
	defer c.Close()
	if e = c.LoadSession(ctx, *dir); e != nil {
		return e
	}
	if *aiPrepare || *aiInspect || *aiCancel || *aiStart || *aiNext {
		c.HTTP.Timeout = timeout
		raw, err := io.ReadAll(io.LimitReader(in, update.MaxDocument+1))
		if err != nil || len(raw) > update.MaxDocument {
			return errors.New("complete research command exceeds 64 KiB")
		}
		action := "ai-prepare"
		if *aiNext {
			var next core.WENResearchAINextInput
			if err = update.StrictJSON(raw, &next); err != nil {
				return err
			}
			action = "ai-prepare-next"
		} else if *aiStart {
			var consent core.WENResearchAIStartInput
			if err = update.StrictJSON(raw, &consent); err != nil {
				return err
			}
			action = "ai-start"
		} else if *aiPrepare {
			if _, err = core.DecodeWENResearchAIPrepare(raw); err != nil {
				return err
			}
		} else {
			var selection core.WENResearchAISelection
			if err = update.StrictJSON(raw, &selection); err != nil {
				return err
			}
			action = "ai-inspect"
			if *aiCancel {
				action = "ai-cancel"
			}
		}
		var result json.RawMessage
		if err = c.Do(ctx, http.MethodPost, "/api/v1/wen/research/"+action, json.RawMessage(raw), &result); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(result)
	}
	if *read {
		c.HTTP.Timeout = timeout
		raw, err := io.ReadAll(io.LimitReader(in, update.MaxDocument+1))
		if err != nil {
			return err
		}
		input, err := core.DecodeWENResearchRead(raw)
		if err != nil {
			return err
		}
		var result json.RawMessage
		if err = c.Do(ctx, http.MethodPost, "/api/v1/wen/research/read", input, &result); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(result)
	}
	if *revisionReview || *revisionSave || *proposalReview || *proposalSave {
		b, err := io.ReadAll(io.LimitReader(in, update.MaxDocument+1))
		if err != nil || len(b) > update.MaxDocument {
			return errors.New("complete strategy command exceeds 64 KiB")
		}
		var result json.RawMessage
		if *proposalReview {
			var input core.WENStrategyProposalInput
			if err = update.StrictJSON(b, &input); err != nil {
				return err
			}
			err = c.Do(ctx, http.MethodPost, "/api/v1/wen/research/proposal-review", json.RawMessage(b), &result)
		} else if *proposalSave {
			var input core.WENStrategyProposalReview
			if err = update.StrictJSON(b, &input); err != nil {
				return err
			}
			err = c.Do(ctx, http.MethodPost, "/api/v1/wen/research/proposal-save", json.RawMessage(b), &result)
		} else if *revisionReview {
			var input core.WENStrategyRevisionInput
			if err = update.StrictJSON(b, &input); err != nil {
				return err
			}
			err = c.Do(ctx, http.MethodPost, "/api/v1/wen/research/revision-review", input, &result)
		} else {
			var input core.WENStrategyRevisionReview
			if err = update.StrictJSON(b, &input); err != nil {
				return err
			}
			err = c.Do(ctx, http.MethodPost, "/api/v1/wen/research/revision-save", input, &result)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(result)
	}
	if *review {
		b, err := io.ReadAll(io.LimitReader(in, 8193))
		var selection core.WENSavedChatSelection
		if err != nil || len(b) > 8192 {
			return errors.New("selection too large")
		}
		if err = update.StrictJSON(b, &selection); err != nil {
			return err
		}
		var result core.WENResearchChatReview
		if err = c.Do(ctx, http.MethodPost, "/api/v1/wen/research/chat-review", selection, &result); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(result)
	}
	var result core.WENLibrary
	if *change {
		b, e := io.ReadAll(io.LimitReader(in, 512*1024+1))
		if e != nil || len(b) > 512*1024 {
			return errors.New("command too large")
		}
		var cmd core.WENResearchCommand
		if e = update.StrictJSON(b, &cmd); e != nil {
			return e
		}
		e = c.Do(ctx, http.MethodPost, "/api/v1/wen/research", cmd, &result)
		if e != nil {
			return e
		}
	} else {
		if e = c.Do(ctx, http.MethodGet, "/api/v1/wen/research?profile="+url.QueryEscape(*profile), nil, &result); e != nil {
			return e
		}
	}
	return json.NewEncoder(out).Encode(result)
}
