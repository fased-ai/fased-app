package core

import (
	"context"
	"errors"
	"net/http"
)

const custodyAccountsHeader = "X-Fased-Custody-Accounts"

// This is per-request OS-owner access, not a session or a device credential.
// Only the installed, identity-checked account helper enables it. The server
// independently requires its protected app IPC and limits the API surface.
func custodyAccountsRoute(method, path string) bool {
	switch path {
	case "/api/v1/accounts":
		return method == http.MethodGet || method == http.MethodPut
	case "/api/v1/accounts/setup", "/api/v1/accounts/rpc", "/api/v1/accounts/rpc-credential":
		return method == http.MethodPut
	case "/api/v1/accounts/read", "/api/v1/accounts/rpc-check":
		return method == http.MethodPost
	}
	return false
}

func (h *HTTPHandler) custodyAccountsRequest(r *http.Request) bool {
	if r.Context().Value(privateIPCKey{}) != true || r.Header.Get(custodyAccountsHeader) != h.store.instanceID || !custodyAccountsRoute(r.Method, r.URL.Path) || h.instanceLocation != "remote" || h.instanceOwnerUID <= 0 || h.instanceOwnerKind != "custody" || !h.store.custodyOwnerSupported || h.store.custodyNetwork == "" {
		return false
	}
	owner, err := h.store.remoteOwner(r.Context())
	return err == nil && owner.UserID > 0 && len(owner.Devices) > 0
}

// CustodyAccounts never loads, creates, saves or exports a bearer token. Device
// revocation remains independent of the explicitly approved OS-owner helper.
func (c *Client) CustodyAccounts(ctx context.Context) error {
	if c.remote || c.Endpoint.Network != "unix" || c.Endpoint.InstanceID == "" {
		return errors.New("protected custody account IPC required")
	}
	c.Token, c.CSRF = "", ""
	c.custodyAccounts = true
	var view AccountsView
	if err := c.Do(ctx, http.MethodGet, "/api/v1/accounts", nil, &view); err != nil {
		c.custodyAccounts = false
		return err
	}
	return nil
}
