package update

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

// AcquireFixtureInspectionBundle is owner-side acquisition of a finite pinned
// public fixture set. It cannot inspect/install/execute or write trusted state.
// Only controlled loopback HTTPS is accepted; the signer never calls this path.
func AcquireFixtureInspectionBundle(ctx context.Context, seedBytes, ca []byte, indexSHA string) (map[string][]byte, error) {
	var seed TrustFixtureSeed
	if StrictJSON(seedBytes, &seed) != nil || !seed.FixtureBinding.valid() || !hashOK(indexSHA) {
		return nil, errors.New("pinned fixture source required")
	}
	u, e := acquisitionURL(seed.Origin)
	if e != nil || u.Path != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, errors.New("controlled loopback HTTPS only")
	}
	client, transport, e := acquisitionClient(ca)
	if e != nil {
		return nil, e
	}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	get := func(name string, limit int) ([]byte, error) {
		req, e := http.NewRequestWithContext(ctx, "GET", seed.Origin+"/tuf/offline/"+name, nil)
		if e != nil {
			return nil, e
		}
		req.Header.Set("Accept-Encoding", "identity")
		resp, e := client.Do(req)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("Content-Range") != "" || resp.ContentLength > int64(limit) {
			return nil, errors.New("bounded complete HTTPS fixture response required")
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
		if e != nil || len(b) == 0 || len(b) > limit {
			return nil, errors.New("bounded HTTPS fixture body required")
		}
		return b, nil
	}
	b, e := get("index.json", MaxDocument)
	if e != nil || digest(b) != indexSHA {
		return nil, errors.New("downloaded index pin mismatch")
	}
	var index inspectionIndex
	if StrictJSON(b, &index) != nil || index.Version != 1 || index.Profile != inspectionProfile || !index.FixtureOnly || len(index.Files) > 63 || len(index.Files) < 9 {
		return nil, errors.New("invalid pinned offline index")
	}
	out := map[string][]byte{"index.json": b}
	total := len(b)
	last := ""
	for _, f := range index.Files {
		limit := inspectionNameLimit(f.Name)
		if limit == 0 || f.Name <= last || f.Size < 1 || f.Size > int64(limit) || !hashOK(f.SHA) {
			return nil, errors.New("unsafe pinned fixture entry")
		}
		last = f.Name
		b, e = get(f.Name, limit)
		if e != nil || int64(len(b)) != f.Size || digest(b) != f.SHA {
			return nil, errors.New("downloaded fixture differs from pin")
		}
		total += len(b)
		if total > inspectionMaxBytes {
			return nil, errors.New("fixture acquisition byte budget")
		}
		out[f.Name] = b
	}
	return out, nil
}
