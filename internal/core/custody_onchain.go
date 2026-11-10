package core

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// This authority is distinct from Accounts and cannot access the signer. Every
// mutation still passes the ordinary revision, owner approval and recovery path.
const custodyOnchainHeader = "X-Fased-Custody-Onchain"

func custodyOnchainRoute(method string, u *url.URL) bool {
	if u == nil || u.Path != "/api/v1/agents" || len(u.RawQuery) > 1024 {
		return false
	}
	if method == http.MethodPost {
		return u.RawQuery == ""
	}
	if method != http.MethodGet {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) > 1 {
		return false
	}
	for key, values := range q {
		if key != "profile" || len(values) != 1 || len(values[0]) > 128 {
			return false
		}
	}
	return true
}

func (h *HTTPHandler) custodyOnchainRequest(r *http.Request) bool {
	return custodyOnchainRoute(r.Method, r.URL) && h.custodyOwnerRequest(r, custodyOnchainHeader)
}

func custodyOnchainAction(action string) bool {
	switch action {
	case "swap-view", "swap-quote", "swap-review", "swap-submit", "swap-cancel", "swap-reconcile":
		return true
	}
	return false
}

// Do not disclose Chat, Memory, provider, or unrelated task records to the
// tokenless Onchain helper. Preserve only profile selection and trade state.
func writeAgentOwnerView(w http.ResponseWriter, r *http.Request, v AgentView) {
	if r.Header.Get(custodyOnchainHeader) == "" {
		writeJSON(w, http.StatusOK, v)
		return
	}
	type profile struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	profiles := make([]profile, 0, len(v.Profiles))
	for _, p := range v.Profiles {
		profiles = append(profiles, profile{ID: p.ID, Name: p.Name})
	}
	writeJSON(w, http.StatusOK, struct {
		Onchain   *OnchainView `json:"onchain,omitempty"`
		Revision  int64        `json:"revision"`
		ProfileID string       `json:"profileId"`
		Profiles  []profile    `json:"profiles"`
	}{v.Onchain, v.Revision, v.ProfileID, profiles})
}

// CustodyOnchain admits only the protected installed app IPC; it creates no
// session or token. The server repeats the route, owner and action checks.
func (c *Client) CustodyOnchain(ctx context.Context) error {
	if c.remote || c.Endpoint.Network != "unix" || c.Endpoint.InstanceID == "" || c.custodyAccounts {
		return errors.New("protected custody Onchain IPC required")
	}
	c.Token, c.CSRF = "", ""
	c.custodyOnchain = true
	var view AgentView
	if err := c.Do(ctx, http.MethodGet, "/api/v1/agents", nil, &view); err != nil {
		c.custodyOnchain = false
		return err
	}
	return nil
}
