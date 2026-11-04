//go:build !linux

package tailnet

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
)

func Listen(string) (net.Listener, error) {
	return nil, errors.New("Remote ingress currently requires Linux")
}
func ConnContext(ctx context.Context, _ net.Conn) context.Context { return ctx }
func Trusted(*http.Request) bool                                  { return false }
func Target(dir string) string                                    { return "unix:" + filepath.Join(dir, "tailscale.sock") }

func dialDaemon(context.Context) (net.Conn, error) {
	return nil, errors.New("Remote host identity requires Linux")
}
