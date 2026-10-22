package core

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type Server struct {
	Store        *Store
	HTTP         *http.Server
	Listener     net.Listener
	IPC          net.Listener
	IPCServer    *http.Server
	Lock         *InstanceLock
	URL          string
	agentFixture bool
	dir          string
	taskCancel   context.CancelFunc
	taskDone     chan struct{}
}

func Start(ctx context.Context, dir, listen, publicOrigin string, assets fs.FS, custodyConfig ...string) (*Server, error) {
	return start(ctx, dir, listen, publicOrigin, assets, false, custodyConfig...)
}

// StartAgentFixture enables only compiled, in-process model fixtures; bound
// disposable credentials may exercise authentication without contacting OpenAI.
func StartAgentFixture(ctx context.Context, dir, listen, publicOrigin string, assets fs.FS) (*Server, error) {
	return start(ctx, dir, listen, publicOrigin, assets, true)
}
func start(ctx context.Context, dir, listen, publicOrigin string, assets fs.FS, fixture bool, custodyConfig ...string) (*Server, error) {
	host, _, err := net.SplitHostPort(listen)
	if err != nil || !isLoopbackHost(host) {
		return nil, errors.New("listen address must be an explicit loopback IP and port")
	}
	if err = privateDir(dir); err != nil {
		return nil, err
	}
	lock, err := LockInstance(filepath.Join(dir, "instance.lock"))
	if err != nil {
		return nil, err
	}
	store, err := OpenStore(dir)
	if err != nil {
		lock.Close()
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			store.Close()
			lock.Close()
		}
	}()
	if len(custodyConfig) > 1 {
		return nil, errors.New("one signer configuration required")
	}
	if len(custodyConfig) == 1 {
		if err = store.configureSigner(custodyConfig[0]); err != nil {
			return nil, err
		}
	}
	claimed, err := store.Claimed(ctx)
	if err != nil {
		return nil, err
	}
	if !claimed {
		if _, err = store.IssueClaimCode(ctx); err != nil {
			return nil, err
		}
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	baseURL := "http://" + listener.Addr().String()
	handler, err := NewHTTPHandler(store, baseURL, publicOrigin, assets)
	if err != nil {
		listener.Close()
		return nil, err
	}
	server := &Server{Store: store, Listener: listener, Lock: lock, URL: baseURL, dir: dir, HTTP: &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}}
	ipc, network, address, err := listenIPC(dir, store.instanceID)
	if err != nil {
		listener.Close()
		return nil, err
	}
	server.IPC = ipc
	server.IPCServer = &http.Server{Handler: privateIPCHandler(handler), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	if err = WritePrivateJSON(filepath.Join(dir, "endpoint.json"), Endpoint{URL: baseURL, InstanceID: store.instanceID, Network: network, Address: address}); err != nil {
		listener.Close()
		ipc.Close()
		return nil, err
	}
	handler.agentFixture = fixture
	server.agentFixture = fixture
	server.startAgentTasks()
	failed = false
	return server, nil
}
func (s *Server) startAgentTasks() {
	ctx, cancel := context.WithCancel(context.Background())
	s.taskCancel = cancel
	s.taskDone = make(chan struct{})
	go func() {
		defer close(s.taskDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		lastError := ""
		var newsRT http.RoundTripper = newPublicNewsTransport()
		if s.agentFixture {
			newsRT = fixtureNewsTransport{now: s.Store.now}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c, stop := context.WithTimeout(ctx, 5*time.Second)
				e := s.Store.runAgentTasks(c, s.agentFixture)
				if e == nil {
					e = s.Store.advanceAgentNews(c, newsRT, s.agentFixture)
					stop()
					c, stop = context.WithTimeout(ctx, 5*time.Second)
					if e == nil {
						e = s.Store.advanceAgentAI(c, fixtureAITransport{}, s.agentFixture)
					}
				}
				stop()
				message := ""
				if e != nil && !errors.Is(e, ErrCorePaused) && !errors.Is(e, context.Canceled) {
					message = "local task scheduler held; inspect workspace capacity and core state"
				}
				if message != "" && message != lastError {
					log.Print(message)
				}
				lastError = message
			}
		}
	}()
}
func (s *Server) Serve() error {
	errorsC := make(chan error, 2)
	go func() { errorsC <- s.HTTP.Serve(s.Listener) }()
	go func() { errorsC <- s.IPCServer.Serve(s.IPC) }()
	err := <-errorsC
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func (s *Server) Close(ctx context.Context) error {
	if s.taskCancel != nil {
		s.taskCancel()
		<-s.taskDone
	}
	err := errors.Join(s.HTTP.Shutdown(ctx), s.IPCServer.Shutdown(ctx))
	if err != nil {
		_ = s.HTTP.Close()
		_ = s.IPCServer.Close()
	}
	// Keep a stale endpoint harmless: clients verify the instance ID before sending credentials.
	_ = os.Remove(filepath.Join(s.dir, "endpoint.json"))
	return errors.Join(err, s.Store.Close(), s.Lock.Close())
}
