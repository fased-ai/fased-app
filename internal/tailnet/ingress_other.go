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

func ProtectedSocket(string, int) (string, error) {
	return "", errors.New("protected Remote ingress currently requires Linux")
}

func ProtectedSocketReady(string, int) bool     { return false }
func ProtectedDataSocketReady(string, int) bool { return false }
func ProtectedPackageData(string, int) bool     { return false }

func ProtectedSocketUnit(string, int) (string, []byte, error) {
	return "", nil, errors.New("protected Remote ingress currently requires Linux")
}

func ProtectedSocketDropIn(string, int) (string, []byte, error) {
	return "", nil, errors.New("protected Remote ingress currently requires Linux")
}

func ActivatedListener(string, int) (net.Listener, bool, error) { return nil, false, nil }

func dialDaemon(context.Context) (net.Conn, error) {
	return nil, errors.New("Remote host identity requires Linux")
}
