package releaseops

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var stableReleaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// PublishedRelease is an immutable GitHub identity, never a wallet capability.
type PublishedRelease struct {
	ID        string                 `json:"id"`
	Tag       string                 `json:"tag"`
	Source    string                 `json:"source"`
	TagObject string                 `json:"tagObject"`
	Assets    map[string]GitHubAsset `json:"assets"`
}

func (g *GitHubTransport) LatestPublished(ctx context.Context, ownerID string) (PublishedRelease, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var result PublishedRelease
	ctx, cancel, err := g.context(ctx)
	if err != nil {
		return result, err
	}
	defer cancel()
	repository, err := g.apiObject(ctx, "")
	if err != nil {
		return result, err
	}
	name, e := stringJSON(repository["full_name"])
	owner, e2 := exactObject(repository["owner"], "id")
	if e != nil || e2 != nil || name != g.policy.Repository || idJSON(repository["id"]) != g.policy.RepositoryID || idJSON(owner["id"]) != ownerID {
		return result, errors.New("GitHub repository/owner identity mismatch")
	}
	release, err := g.apiObject(ctx, "/releases/latest")
	if err != nil {
		return result, err
	}
	for _, field := range []string{"draft", "prerelease", "immutable"} {
		var value bool
		if json.Unmarshal(release[field], &value) != nil || value != (field == "immutable") {
			return result, errors.New("immutable stable public release required")
		}
	}
	result.ID = idJSON(release["id"])
	result.Tag, e = stringJSON(release["tag_name"])
	if result.ID == "" || e != nil || !stableReleaseTag.MatchString(result.Tag) {
		return result, errors.New("stable release identity invalid")
	}
	ref, err := g.apiObject(ctx, "/git/ref/tags/"+result.Tag)
	if err != nil {
		return result, err
	}
	var object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	}
	if json.Unmarshal(ref["object"], &object) != nil || object.Type != "tag" || !commitHash(object.SHA) {
		return result, errors.New("annotated release tag required")
	}
	result.TagObject = object.SHA
	tag, err := g.apiObject(ctx, "/git/tags/"+object.SHA)
	if err != nil {
		return result, err
	}
	actual, e := stringJSON(tag["tag"])
	if e != nil || actual != result.Tag || json.Unmarshal(tag["object"], &object) != nil || object.Type != "commit" || !commitHash(object.SHA) {
		return result, errors.New("release tag commit invalid")
	}
	result.Source = object.SHA
	var assets []json.RawMessage
	if json.Unmarshal(release["assets"], &assets) != nil || len(assets) != 5 {
		return result, errors.New("complete attested release inventory required")
	}
	result.Assets = map[string]GitHubAsset{}
	membership := map[string]githubAssetIdentity{}
	for _, raw := range assets {
		obj, e := exactObject(raw, "id", "name", "size", "state", "digest")
		if e != nil {
			return result, e
		}
		id := idJSON(obj["id"])
		name, e := stringJSON(obj["name"])
		state, se := stringJSON(obj["state"])
		hash, he := stringJSON(obj["digest"])
		size, ne := strconv.ParseInt(strings.TrimSpace(string(obj["size"])), 10, 64)
		a := GitHubAsset{RepositoryID: g.policy.RepositoryID, ReleaseID: result.ID, AssetID: id, Name: name, Size: size, SHA256: strings.TrimPrefix(hash, "sha256:")}
		check := a
		check.CDNPath = g.policy.CDNPathPrefix + "discovery"
		if id == "" || e != nil || se != nil || he != nil || ne != nil || state != "uploaded" || !strings.HasPrefix(hash, "sha256:") || check.Validate(g.policy) != nil {
			return result, errors.New("release asset identity invalid")
		}
		if _, exists := result.Assets[name]; exists {
			return result, errors.New("duplicate release asset")
		}
		if _, exists := membership[id]; exists {
			return result, errors.New("duplicate release asset ID")
		}
		result.Assets[name] = a
		membership[id] = githubAssetIdentity{Name: name, Size: size, State: state}
	}
	g.repositoryChecked = true
	g.releases[result.ID] = membership
	return result, nil
}

func commitHash(s string) bool { return len(s) == 40 && strings.Trim(s, "0123456789abcdef") == "" }
