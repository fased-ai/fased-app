package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

// Only inert changes to an existing fixed-allocation strategy are supported.
// Raw change input is checked before decoding so null cannot become zero.
type WENStrategyChanges struct {
	WeightsBps    *[]int  `json:"weightsBps,omitempty"`
	EveryNthRound *int    `json:"everyNthRound,omitempty"`
	Thesis        *string `json:"thesis,omitempty"`
}
type WENStrategyProposalInput struct {
	Profile         string `json:"profile"`
	Revision        int64  `json:"revision"`
	LibraryRevision int64  `json:"libraryRevision"`
	RequestID       string `json:"requestId"`
	CardID          string `json:"cardId"`
	ParentDigest    string `json:"parentDigest"`
	Name            string `json:"name"`
	Changes         string `json:"changes,omitempty"`
}
type WENStrategyProposalSource struct {
	RequestID        string       `json:"requestId"`
	SessionID        string       `json:"sessionId"`
	Prompt           string       `json:"prompt"`
	Answer           string       `json:"answer"`
	Context          string       `json:"context"`
	Disclosure       string       `json:"disclosure"`
	RequestDigest    string       `json:"requestDigest"`
	ContextSHA256    string       `json:"contextSha256"`
	DisclosureSHA256 string       `json:"disclosureSha256"`
	Card             AIActionCard `json:"card"`
}
type WENStrategyProposalReview struct {
	Schema          string                    `json:"schema"`
	Input           WENStrategyProposalInput  `json:"input"`
	PreparedAt      string                    `json:"preparedAt"`
	ExpiresAt       int64                     `json:"expiresAt"`
	Source          WENStrategyProposalSource `json:"source"`
	SourceSHA256    string                    `json:"sourceSha256"`
	Changes         WENStrategyChanges        `json:"changes"`
	Parent          WENVersion                `json:"parent"`
	Candidate       WENVersion                `json:"candidate"`
	AmountsLamports [5]string                 `json:"amountsLamports"`
	Witness         string                    `json:"witness"`
}

func decodeWENChanges(raw []byte) (WENStrategyChanges, error) {
	var changes WENStrategyChanges
	bad := errors.New("use a nonempty object of weightsBps, everyNthRound or thesis; null is unsupported")
	var fields map[string]json.RawMessage
	if update.StrictJSON(raw, &changes) != nil || json.Unmarshal(raw, &fields) != nil || len(fields) == 0 {
		return changes, bad
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return changes, bad
		}
	}
	if value, ok := fields["weightsBps"]; ok {
		var values []json.RawMessage
		if json.Unmarshal(value, &values) != nil || len(values) != 5 {
			return changes, bad
		}
		for _, value := range values {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return changes, bad
			}
		}
	}
	return changes, nil
}

func prepareWENStrategyProposal(ctx context.Context, reader snapshotReader, c WENStrategyProposalInput, now time.Time) (WENStrategyProposalReview, error) {
	var result WENStrategyProposalReview
	bad := errors.New("select one retained completed manual strategy card and matching saved parent")
	if !validAgentText(c.Profile, 80, true) || !validAgentText(c.RequestID, 80, true) || !validAgentText(c.CardID, 100, true) || !validAgentText(c.Name, 80, true) || !researchDigest.MatchString(c.ParentDigest) || c.Revision < 0 || c.LibraryRevision < 0 {
		return result, bad
	}
	d, revision, err := readAgents(ctx, reader)
	if err != nil {
		return result, err
	}
	if revision != c.Revision {
		return result, ErrConflict
	}
	profiles := 0
	for _, p := range d.Profiles {
		if p.ID == c.Profile {
			profiles++
		}
	}
	var q AgentAIRequest
	requests, cards := 0, 0
	for _, request := range d.AIRequests {
		if request.ID == c.RequestID {
			q = request
			requests++
		}
		for _, card := range request.Cards {
			if card.ID == c.CardID {
				cards++
			}
		}
	}
	if profiles != 1 || requests != 1 || cards != 1 || q.ProfileID != c.Profile || q.State != "completed" || q.TaskID != "" || q.HeldMicrousd != 0 || q.Digest != aiDigest(q) {
		return result, bad
	}
	wire, err := aiWire(q)
	if err != nil || string(wire) != q.Disclosure {
		return result, bad
	}
	sessions := 0
	for _, session := range d.Sessions {
		if session.ID == q.SessionID {
			sessions++
			if session.ProfileID != c.Profile {
				return result, bad
			}
		}
	}
	var card AIActionCard
	for _, item := range q.Cards {
		if item.ID == c.CardID {
			card = item
		}
	}
	if sessions != 1 || card.Kind != "paper-strategy-draft" || (card.Review != "unreviewed" && card.Review != "kept") || !validAgentText(card.Text, 1000, true) {
		return result, bad
	}
	var proposal struct {
		Schema       string          `json:"schema"`
		ParentDigest string          `json:"parentDigest"`
		Changes      json.RawMessage `json:"changes"`
	}
	if update.StrictJSON([]byte(card.Text), &proposal) != nil || proposal.Schema != "wen-strategy-change/1" || proposal.ParentDigest != c.ParentDigest {
		return result, errors.New("unsupported strategy card; expected a strict wen-strategy-change/1 for this parent")
	}
	changes, err := decodeWENChanges(proposal.Changes)
	if err != nil {
		return result, err
	}
	if c.Changes != "" {
		changes, err = decodeWENChanges([]byte(c.Changes))
		if err != nil {
			return result, err
		}
	}
	all, libRevision, err := readWENLibraries(ctx, reader)
	if err != nil {
		return result, err
	}
	if libRevision != c.LibraryRevision {
		return result, ErrConflict
	}
	library := libraryFor(all, c.Profile, libRevision)
	parent, original, err := wenRevisionParent(library, c.ParentDigest)
	if err != nil {
		return result, err
	}
	doc := original.Document
	if doc.Allocation.Mode != "fixed" || len(doc.Allocation.Rules) != 0 {
		return result, errors.New("strategy cards currently support fixed-allocation parents without rules")
	}
	doc.Name = c.Name
	if changes.WeightsBps != nil {
		doc.Allocation.WeightsBps = append([]int(nil), (*changes.WeightsBps)...)
		doc.Allocation.Preset = "custom"
	}
	if changes.EveryNthRound != nil {
		doc.Schedule.EveryNthRound = *changes.EveryNthRound
	}
	if changes.Thesis != nil {
		doc.Research.Thesis = *changes.Thesis
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return result, err
	}
	raw = append(raw, '\n')
	candidate, err := wenRevisionCandidate(library, original, string(raw))
	if err != nil {
		return result, err
	}
	amounts, err := wenstrategy.CompileAllocation(doc.Limits.PerRoundLamports, doc.Allocation.WeightsBps)
	if err != nil {
		return result, err
	}
	source := WENStrategyProposalSource{RequestID: q.ID, SessionID: q.SessionID, Prompt: q.Prompt, Answer: q.Answer, Context: q.Context, Disclosure: q.Disclosure, RequestDigest: q.Digest, ContextSHA256: wenHash([]byte(q.Context)), DisclosureSHA256: wenHash([]byte(q.Disclosure)), Card: card}
	stamp := now.UTC().Format(time.RFC3339Nano)
	return WENStrategyProposalReview{Schema: "wen-strategy-proposal-review/1", Input: c, PreparedAt: stamp, ExpiresAt: now.Add(120 * time.Second).Unix(), Source: source, SourceSHA256: wenHash([]byte(card.Text)), Changes: changes, Parent: parent, Candidate: WENVersion{candidate.Digest, string(raw), doc.Name, stamp, "unsaved-ai-revision"}, AmountsLamports: amounts}, nil
}

func (h *HTTPHandler) strategyProposalWitness(review WENStrategyProposalReview) string {
	review.Witness = ""
	raw, _ := json.Marshal(review)
	mac := hmac.New(sha256.New, h.wenResearchKey[:])
	_, _ = mac.Write([]byte("wen-strategy-proposal-review/1\n"))
	_, _ = mac.Write(raw)
	return "wen-strategy-proposal-v1:" + hex.EncodeToString(mac.Sum(nil))
}

func (h *HTTPHandler) saveWENStrategyProposal(ctx context.Context, review WENStrategyProposalReview) (WENStrategyRevisionResult, error) {
	var result WENStrategyRevisionResult
	bad := errors.New("strategy proposal changed, expired or belongs to another core; review the card again")
	now := h.store.now()
	prepared, err := time.Parse(time.RFC3339Nano, review.PreparedAt)
	if err != nil || prepared.After(now) || review.Schema != "wen-strategy-proposal-review/1" || review.ExpiresAt <= now.Unix() || review.ExpiresAt > now.Add(120*time.Second).Unix() || !hmac.Equal([]byte(review.Witness), []byte(h.strategyProposalWitness(review))) {
		return result, bad
	}
	tx, err := h.store.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	phase, _, err := readPause(ctx, tx)
	if err != nil {
		return result, err
	}
	if phase == "paused" {
		return result, ErrCorePaused
	}
	expected, err := prepareWENStrategyProposal(ctx, tx, review.Input, prepared)
	if err != nil {
		return result, err
	}
	expected.Witness = h.strategyProposalWitness(expected)
	if !reflect.DeepEqual(expected, review) {
		return result, bad
	}
	return h.commitWENRevision(ctx, tx, review.Input.Profile, review.Source.SessionID, review.Candidate, "wen-chat-ai-revision-v1:", now, func(revision, libRevision int64) any {
		return map[string]any{"schema": "wen-strategy-proposal-receipt/1", "profile": review.Input.Profile, "sessionId": review.Source.SessionID, "requestId": review.Source.RequestID, "cardId": review.Source.Card.ID, "sourceSha256": review.SourceSHA256, "contextSha256": review.Source.ContextSHA256, "disclosureSha256": review.Source.DisclosureSHA256, "requestDigest": review.Source.RequestDigest, "parentDigest": review.Parent.Digest, "candidateDigest": review.Candidate.Digest, "sourceWorkspaceRevision": revision, "libraryRevision": libRevision + 1}
	})
}

// The ordinary origin/auth/CSRF/strict-size guard is shared with user revisions.
func (h *HTTPHandler) wenStrategyProposalBody(w http.ResponseWriter, r *http.Request, raw []byte, save bool) {
	fail := func(err error) {
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrCorePaused) {
			writeError(w, 409, err.Error())
		} else {
			writeError(w, 400, err.Error())
		}
	}
	if save {
		var review WENStrategyProposalReview
		if update.StrictJSON(raw, &review) != nil {
			writeError(w, 400, "invalid strategy proposal review")
			return
		}
		result, err := h.saveWENStrategyProposal(r.Context(), review)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	var input WENStrategyProposalInput
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if bytes.Equal(bytes.TrimSpace(fields["changes"]), []byte("null")) {
		writeError(w, 400, "changes must be JSON text, not null")
		return
	}
	if update.StrictJSON(raw, &input) != nil {
		writeError(w, 400, "invalid strategy proposal input")
		return
	}
	tx, err := h.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(err)
		return
	}
	defer tx.Rollback()
	phase, _, err := readPause(r.Context(), tx)
	if err != nil {
		fail(err)
		return
	}
	if phase == "paused" {
		fail(ErrCorePaused)
		return
	}
	review, err := prepareWENStrategyProposal(r.Context(), tx, input, h.store.now())
	if err != nil {
		fail(err)
		return
	}
	review.Witness = h.strategyProposalWitness(review)
	body, err := json.Marshal(review)
	if err != nil || len(body)+1 > update.MaxDocument {
		writeError(w, 400, "complete strategy proposal review exceeds 64 KiB; no records omitted or saved")
		return
	}
	writeJSON(w, 200, review)
}
