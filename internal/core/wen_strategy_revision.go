package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

type WENStrategyRevisionInput struct {
	Profile         string `json:"profile"`
	Revision        int64  `json:"revision"` // ordinary Chat workspace
	LibraryRevision int64  `json:"libraryRevision"`
	SourceMessageID string `json:"sourceMessageId"`
	ParentDigest    string `json:"parentDigest"`
	Raw             string `json:"raw"`
}

// A transient owner review; neither this projection nor a library version grants authority.
type WENStrategyRevisionReview struct {
	Schema          string       `json:"schema"`
	Profile         string       `json:"profile"`
	Revision        int64        `json:"revision"`
	LibraryRevision int64        `json:"libraryRevision"`
	PreparedAt      string       `json:"preparedAt"`
	ExpiresAt       int64        `json:"expiresAt"`
	Source          AgentMessage `json:"source"`
	SourceSHA256    string       `json:"sourceSha256"`
	Parent          WENVersion   `json:"parent"`
	Candidate       WENVersion   `json:"candidate"`
	Witness         string       `json:"witness"`
}
type WENStrategyRevisionResult struct {
	Library       WENLibrary   `json:"library"`
	AgentRevision int64        `json:"agentRevision"`
	Receipt       AgentMessage `json:"receipt"`
}

func prepareWENStrategyRevision(ctx context.Context, reader snapshotReader, c WENStrategyRevisionInput, now time.Time) (WENStrategyRevisionReview, error) {
	var result WENStrategyRevisionReview
	bad := errors.New("select one retained same-profile user message and original saved strategy")
	if !validAgentText(c.Profile, 80, true) || !validAgentText(c.SourceMessageID, 80, true) || !researchDigest.MatchString(c.ParentDigest) || c.Revision < 0 || c.LibraryRevision < 0 {
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
	if profiles != 1 {
		return result, bad
	}
	var source AgentMessage
	sources := 0
	for _, m := range d.Messages {
		if m.ID == c.SourceMessageID {
			sources++
			source = m
		}
	}
	if sources != 1 || source.ProfileID != c.Profile || source.Role != "user" || !validAgentText(source.Text, 4000, true) || !utf8.ValidString(source.Text) {
		return result, bad
	}
	sessions := 0
	for _, session := range d.Sessions {
		if session.ID == source.SessionID {
			sessions++
			if session.ProfileID != c.Profile {
				return result, bad
			}
		}
	}
	if sessions != 1 {
		return result, bad
	}
	all, libraryRevision, err := readWENLibraries(ctx, reader)
	if err != nil {
		return result, err
	}
	if libraryRevision != c.LibraryRevision {
		return result, ErrConflict
	}
	library := libraryFor(all, c.Profile, libraryRevision)
	parent, original, err := wenRevisionParent(library, c.ParentDigest)
	if err != nil {
		return result, err
	}
	candidate, err := wenRevisionCandidate(library, original, c.Raw)
	if err != nil {
		return result, err
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	return WENStrategyRevisionReview{Schema: "wen-strategy-revision-review/1", Profile: c.Profile, Revision: revision, LibraryRevision: libraryRevision, PreparedAt: stamp, ExpiresAt: now.Add(120 * time.Second).Unix(), Source: source, SourceSHA256: wenHash([]byte(source.Text)), Parent: parent, Candidate: WENVersion{candidate.Digest, c.Raw, candidate.Document.Name, stamp, "unsaved-chat-revision"}}, nil
}

func wenRevisionParent(library WENLibrary, digest string) (WENVersion, *wenstrategy.Accepted, error) {
	var parent WENVersion
	count := 0
	for _, v := range library.Versions {
		if v.Digest == digest {
			parent = v
			count++
		}
	}
	original, err := wenstrategy.Decode([]byte(parent.Raw))
	if count != 1 || err != nil || original.Digest != parent.Digest || original.Document.Name != parent.Name || original.Document.Execution.Mode != "review" || original.Document.Execution.MandateID != nil {
		return parent, nil, errors.New("select one valid same-profile review-only parent")
	}
	return parent, original, nil
}
func wenRevisionCandidate(library WENLibrary, original *wenstrategy.Accepted, raw string) (*wenstrategy.Accepted, error) {
	candidate, err := wenstrategy.Decode([]byte(raw))
	if err != nil {
		return nil, err
	}
	if candidate.Document.Execution.Mode != "review" || candidate.Document.Execution.MandateID != nil {
		return nil, errors.New("revision must remain review-only without a mandate")
	}
	if candidate.Document.Market != original.Document.Market {
		return nil, errors.New("revision must keep the original market binding; import other markets separately")
	}
	for _, v := range library.Versions {
		if v.Digest == candidate.Digest {
			return nil, errors.New("candidate version already saved; inspect the existing record")
		}
	}
	return candidate, nil
}

func (h *HTTPHandler) strategyRevisionWitness(review WENStrategyRevisionReview) string {
	review.Witness = ""
	raw, _ := json.Marshal(review)
	mac := hmac.New(sha256.New, h.wenResearchKey[:])
	_, _ = mac.Write([]byte("wen-strategy-revision-review/1\n"))
	_, _ = mac.Write(raw)
	return "wen-strategy-revision-v1:" + hex.EncodeToString(mac.Sum(nil))
}

func (h *HTTPHandler) saveWENStrategyRevision(ctx context.Context, review WENStrategyRevisionReview) (WENStrategyRevisionResult, error) {
	var result WENStrategyRevisionResult
	bad := errors.New("strategy review changed, expired or belongs to another core; review the source again")
	now := h.store.now()
	if review.Schema != "wen-strategy-revision-review/1" || review.ExpiresAt <= now.Unix() || review.ExpiresAt > now.Add(120*time.Second).Unix() || !hmac.Equal([]byte(review.Witness), []byte(h.strategyRevisionWitness(review))) {
		return result, bad
	}
	prepared, err := time.Parse(time.RFC3339Nano, review.PreparedAt)
	if err != nil {
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
	c := WENStrategyRevisionInput{review.Profile, review.Revision, review.LibraryRevision, review.Source.ID, review.Parent.Digest, review.Candidate.Raw}
	expected, err := prepareWENStrategyRevision(ctx, tx, c, prepared)
	if err != nil {
		return result, err
	}
	expected.Witness = h.strategyRevisionWitness(expected)
	if expected != review {
		return result, bad
	}
	return h.commitWENRevision(ctx, tx, c.Profile, review.Source.SessionID, review.Candidate, "wen-chat-revision-v1:", now, func(revision, libRevision int64) any {
		return struct {
			Schema                  string `json:"schema"`
			Profile                 string `json:"profile"`
			SessionID               string `json:"sessionId"`
			SourceMessageID         string `json:"sourceMessageId"`
			SourceSHA256            string `json:"sourceSha256"`
			ParentDigest            string `json:"parentDigest"`
			CandidateDigest         string `json:"candidateDigest"`
			SourceWorkspaceRevision int64  `json:"sourceWorkspaceRevision"`
			LibraryRevision         int64  `json:"libraryRevision"`
		}{"wen-strategy-revision-receipt/1", c.Profile, review.Source.SessionID, review.Source.ID, review.SourceSHA256, review.Parent.Digest, review.Candidate.Digest, revision, libRevision + 1}
	})
}

// Both source kinds share the same bounded, atomic version/receipt writer.
func (h *HTTPHandler) commitWENRevision(ctx context.Context, tx *sql.Tx, profile, session string, version WENVersion, origin string, now time.Time, data func(int64, int64) any) (WENStrategyRevisionResult, error) {
	var result WENStrategyRevisionResult
	d, revision, err := readAgents(ctx, tx)
	if err != nil {
		return result, err
	}
	all, libRevision, err := readWENLibraries(ctx, tx)
	if err != nil {
		return result, err
	}
	library := libraryFor(all, profile, libRevision)
	if len(library.Versions) >= 100 || len(d.Messages) >= 200 {
		return result, errors.New("strategy/message capacity reached; no revision saved")
	}
	id, err := randomToken()
	if err != nil {
		return result, err
	}
	version.Origin = origin + id
	raw, err := json.Marshal(data(revision, libRevision))
	if err != nil {
		return result, err
	}
	receipt := AgentMessage{ID: id, ProfileID: profile, SessionID: session, Role: "system", Text: "Saved inert WEN strategy revision; no task or financial authority.\n" + string(raw), CreatedAt: now.Unix()}
	if !validAgentText(receipt.Text, 4000, true) {
		return result, errors.New("revision receipt capacity exceeded")
	}
	library.Versions = append(library.Versions, version)
	library, err = saveWENLibrary(ctx, tx, all, profile, library, libRevision)
	if err != nil {
		return result, err
	}
	d.Messages = append(d.Messages, receipt)
	if err = saveAgents(ctx, tx, d, revision); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return WENStrategyRevisionResult{Library: library, AgentRevision: revision + 1, Receipt: receipt}, nil
}

func (h *HTTPHandler) wenStrategyRevisionAPI(w http.ResponseWriter, r *http.Request, token string, save bool) {
	h.wenRevisionAPI(w, r, token, save, false)
}
func (h *HTTPHandler) wenRevisionAPI(w http.ResponseWriter, r *http.Request, token string, save, proposal bool) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, 400, "unexpected query")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, update.MaxDocument)
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "complete strategy command exceeds 64 KiB; no records saved")
		return
	}
	if proposal {
		h.wenStrategyProposalBody(w, r, raw, save)
		return
	}
	if save {
		var review WENStrategyRevisionReview
		if update.StrictJSON(raw, &review) != nil {
			writeError(w, 400, "invalid strategy review")
			return
		}
		result, err := h.saveWENStrategyRevision(r.Context(), review)
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrCorePaused) {
			writeError(w, 409, err.Error())
			return
		}
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, result)
		return
	}
	var input WENStrategyRevisionInput
	if update.StrictJSON(raw, &input) != nil {
		writeError(w, 400, "invalid strategy revision input")
		return
	}
	tx, err := h.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 400, "private records unavailable")
		return
	}
	defer tx.Rollback()
	review, err := prepareWENStrategyRevision(r.Context(), tx, input, h.store.now())
	if errors.Is(err, ErrConflict) {
		writeError(w, 409, err.Error())
		return
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	review.Witness = h.strategyRevisionWitness(review)
	body, err := json.Marshal(review)
	if err != nil || len(body)+1 > update.MaxDocument {
		writeError(w, 400, "complete strategy review exceeds 64 KiB; no records omitted or saved")
		return
	}
	writeJSON(w, 200, review)
}
