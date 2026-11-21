package core

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// A Serve switch must not remove the old private listener before the new
// listener can answer. The test uses only disposable loopback listeners.
func TestRemoteListenerTransitionServesBothPaths(t *testing.T) {
	t.Parallel()
	listen := func() net.Listener {
		t.Helper()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		return l
	}
	s := &Server{
		Listener: listen(), IPC: listen(), Remote: listen(), RemoteLegacy: listen(),
		HTTP: &http.Server{}, IPCServer: &http.Server{},
		RemoteHTTP: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ready") })},
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve() }()
	t.Cleanup(func() { _ = s.HTTP.Close(); _ = s.IPCServer.Close(); _ = s.RemoteHTTP.Close() })
	client := &http.Client{Timeout: 3 * time.Second}
	for _, listener := range []net.Listener{s.Remote, s.RemoteLegacy} {
		response, err := client.Get("http://" + listener.Addr().String())
		if err != nil {
			t.Fatal("private listener did not answer:", err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || string(body) != "ready" {
			t.Fatal("private listener response differs", response.StatusCode, string(body), err)
		}
	}
	if err := s.HTTP.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop")
	}
}
