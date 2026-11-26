package core

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/automation"
	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/providersecrets"
	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/worldintel"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Server struct {
	Store        *Store
	HTTP         *http.Server
	Listener     net.Listener
	IPC          net.Listener
	IPCServer    *http.Server
	Remote       net.Listener
	RemoteLegacy net.Listener
	RemoteHTTP   *http.Server
	Lock         *InstanceLock
	URL          string
	agentFixture bool
	dir          string
	ipcNetwork   string
	ipcAddress   string
	serverCtx    context.Context
	serverCancel context.CancelFunc
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

// StartInstalledProviderFixture is the installed launcher composition after
// identity/sandbox validation. Only compiled provider transports are selected;
// credentials use the OS source before restoration, never the standalone key.
func StartInstalledProviderFixture(ctx context.Context, dir, listen, publicOrigin string, assets fs.FS, custodyConfig ...string) (*Server, error) {
	if len(custodyConfig) != 0 {
		return nil, errors.New("installed provider fixture cannot configure a signer")
	}
	return startModesWithTaskSecrets(ctx, dir, listen, publicOrigin, assets, true, false, providersecrets.Native{}, true)
}

// StartSessionFixture enables only finite compiled shadow sessions. It cannot
// configure a signer, model transport, external news or research endpoint.
func StartSessionFixture(ctx context.Context, dir, listen string, assets fs.FS) (*Server, error) {
	return startModes(ctx, dir, listen, "", assets, false, true)
}
func start(ctx context.Context, dir, listen, publicOrigin string, assets fs.FS, fixture bool, custodyConfig ...string) (*Server, error) {
	return startModes(ctx, dir, listen, publicOrigin, assets, fixture, false, custodyConfig...)
}
func startModes(ctx context.Context, dir, listen, publicOrigin string, assets fs.FS, fixture, sessionFixture bool, custodyConfig ...string) (*Server, error) {
	var taskSecrets providersecrets.Source = providersecrets.Native{}
	if fixture {
		taskSecrets = fixtureTaskSecrets{}
	}
	return startModesWithTaskSecrets(ctx, dir, listen, publicOrigin, assets, fixture, sessionFixture, taskSecrets, false, custodyConfig...)
}

func startModesWithTaskSecrets(ctx context.Context, dir, listen, publicOrigin string, assets fs.FS, fixture, sessionFixture bool, taskSecrets providersecrets.Source, providerOnly bool, custodyConfig ...string) (*Server, error) {
	if sessionFixture && (fixture || len(custodyConfig) > 0 || publicOrigin != "") {
		return nil, errors.New("session fixture requires a standalone local core")
	}

	host, _, err := net.SplitHostPort(listen)
	if err != nil || !isLoopbackHost(host) {
		return nil, errors.New("listen address must be an explicit loopback IP and port")
	}
	if err = preflightOpenAIPilotBinding(dir, compiledOpenAIPilotBinding, openAIPilotActivationEnabled); err != nil {
		return nil, err
	}
	if err = privateDir(dir); err != nil {
		return nil, err
	}
	lock, err := LockInstance(filepath.Join(dir, "instance.lock"))
	if err != nil {
		return nil, err
	}
	store, err := openStoreWithTaskSecrets(dir, taskSecrets)
	if err != nil {
		lock.Close()
		return nil, err
	}
	store.sessionFixture = sessionFixture
	store.providerFixture = fixture
	store.providerOnlyFixture = providerOnly
	if fixture || sessionFixture {
		store.rpcAdmission = false
		store.provider.apiCredentials = false
	}
	failed := true
	defer func() {
		if failed {
			store.Close()
			lock.Close()
		}
	}()
	if store.pilot.binding != nil && !pilotStandaloneOptionsAllowed(fixture, sessionFixture, publicOrigin, custodyConfig) {
		return nil, errors.New("pilot requires standalone Local core without fixture, signer or remote modes")
	}
	if len(custodyConfig) > 1 {
		return nil, errors.New("one signer configuration required")
	}
	if len(custodyConfig) == 1 {
		if err = store.configureSigner(custodyConfig[0]); err != nil {
			return nil, err
		}
	}
	if err = func() error {
		setupLock, err := LockOwnerSetup(dir)
		if err != nil {
			return err
		}
		defer setupLock.Close()
		claimed, err := store.Claimed(ctx)
		if err != nil {
			return err
		}
		if !claimed {
			_, err = store.IssueClaimCode(ctx)
		}
		return err
	}(); err != nil {
		return nil, err
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
	serverCtx, serverCancel := context.WithCancel(context.Background())
	baseCtx := func(net.Listener) context.Context { return serverCtx }
	server := &Server{
		Store:        store,
		Listener:     listener,
		Lock:         lock,
		URL:          baseURL,
		dir:          dir,
		serverCtx:    serverCtx,
		serverCancel: serverCancel,
		HTTP:         &http.Server{Handler: handler, BaseContext: baseCtx, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024},
	}
	ipc, network, address, err := listenIPC(dir, store.instanceID)
	if err != nil {
		serverCancel()
		listener.Close()
		return nil, err
	}
	server.IPC = ipc
	server.ipcNetwork = network
	server.ipcAddress = address
	server.IPCServer = &http.Server{Handler: privateIPCHandler(handler), BaseContext: baseCtx, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	if err = server.writeEndpoint(); err != nil {
		serverCancel()
		listener.Close()
		ipc.Close()
		return nil, err
	}
	if err = handler.initLocalBootstrapNonce(dir); err != nil {
		serverCancel()
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
	var newsRT http.RoundTripper = newPublicNewsTransport()
	var aiRT http.RoundTripper = s.Store.providerRuntimeTransport()
	if s.agentFixture {
		newsRT = fixtureNewsTransport{now: s.Store.now, store: s.Store}
		aiRT = fixtureAITransport{}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer recoverBackgroundPanic("task-scheduler")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		lastError := ""
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c, stop := context.WithTimeout(ctx, 5*time.Second)
				e := s.Store.runApplicationUpdates(c)
				if e == nil {
					e = s.Store.runAgentTasks(c, s.agentFixture)
				}
				if s.Store.sessionFixture {
					stop()
					c, stop = context.WithTimeout(ctx, 5*time.Second)
					runErr := s.Store.advanceWENFixtureRuns(c)
					if e == nil {
						e = runErr
					}
				}
				if e == nil && !s.Store.sessionFixture {
					e = s.Store.advanceAgentNews(c, newsRT, s.Store.aiExecutionAvailable(s.agentFixture))
				}
				stop()
				if e == nil && s.Store.onchain != nil {
					onchainContext, done := context.WithTimeout(ctx, 15*time.Second)
					e = s.Store.advanceOnchain(onchainContext)
					done()
				}
				if e == nil && s.Store.wenMining != nil {
					miningContext, done := context.WithTimeout(ctx, 15*time.Second)
					e = s.Store.advanceWENMining(miningContext)
					done()
				}
				message := ""
				if e != nil && !errors.Is(e, ErrCorePaused) && !errors.Is(e, context.Canceled) {
					message = sanitizeDiagValue(e.Error(), 240)
				}
				if message != "" && message != lastError {
					coreLogger.Warn("local task scheduler held", "loop", "tasks", "err", message)
				}
				lastError = message
			}
		}
	}()
	if !s.Store.sessionFixture {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer recoverBackgroundPanic("agent-ai-scheduler")
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			lastError := ""
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					c, stop := context.WithTimeout(ctx, 130*time.Second)
					e := s.Store.advanceAgentAI(c, aiRT, s.agentFixture)
					stop()
					message := ""
					if e != nil && !errors.Is(e, ErrCorePaused) && !errors.Is(e, context.Canceled) {
						message = sanitizeDiagValue(e.Error(), 240)
					}
					if message != "" && message != lastError {
						coreLogger.Warn("local task scheduler held", "loop", "agent-ai", "err", message)
					}
					lastError = message
				}
			}
		}()
	}
	if !s.Store.sessionFixture && !s.agentFixture {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer recoverBackgroundPanic("provider-model-refresh")
			startupTimer := time.NewTimer(5 * time.Second)
			defer startupTimer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-startupTimer.C:
				s.Store.refreshStaleProviderModels(ctx)
			}
			ticker := time.NewTicker(providerModelCacheTTL)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.Store.refreshStaleProviderModels(ctx)
				}
			}
		}()
	}
	if !s.Store.sessionFixture && !s.agentFixture {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer recoverBackgroundPanic("automation-evaluator")
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()

			var (
				mdCh       <-chan marketdata.Event
				mdUnsub    func()
				wiCh       <-chan worldintel.StreamEvent
				wiUnsub    func()
				mdDebounce *time.Timer
				mdDebCh    <-chan time.Time
				wiDebounce *time.Timer
				wiDebCh    <-chan time.Time
			)
			defer func() {
				if mdDebounce != nil {
					mdDebounce.Stop()
				}
				if wiDebounce != nil {
					wiDebounce.Stop()
				}
				if mdUnsub != nil {
					mdUnsub()
				}
				if wiUnsub != nil {
					wiUnsub()
				}
			}()

			activeService := func() *automation.Service {
				autoDB := filepath.Join(s.Store.dir, "automation", "automation.sqlite")
				s.Store.automationMu.Lock()
				initialized := s.Store.automationSvc != nil
				s.Store.automationMu.Unlock()
				if !initialized {
					if _, statErr := os.Stat(autoDB); statErr != nil {
						return nil
					}
				}
				svc, err := s.Store.automationService()
				if err != nil {
					return nil
				}
				return svc
			}

			attachBuses := func() {
				if activeService() == nil && s.Store.loadedOrExistingTradeService() == nil {
					return
				}
				if mdUnsub == nil {
					if md := s.Store.loadedMarketDataService(); md != nil {
						mdCh, mdUnsub = md.Subscribe(marketdata.Filter{})
					}
				}
				if wiUnsub == nil && activeService() != nil {
					if wi := s.Store.loadedWorldIntelService(); wi != nil {
						wiCh, wiUnsub = wi.Subscribe(worldintel.StreamFilter{})
					}
				}
			}

			scheduleDebounce := func(timer **time.Timer, ch *<-chan time.Time) {
				if *timer == nil {
					t := time.NewTimer(500 * time.Millisecond)
					*timer = t
					*ch = t.C
					return
				}
				if !(*timer).Stop() {
					select {
					case <-(*timer).C:
					default:
					}
				}
				(*timer).Reset(500 * time.Millisecond)
				*ch = (*timer).C
			}

			runSweep := func(kinds ...string) {
				if svc := activeService(); svc != nil {
					evalCtx, stop := context.WithTimeout(ctx, 30*time.Second)
					var eErr error
					if len(kinds) == 0 {
						_, eErr = svc.EvaluateNow(evalCtx)
					} else {
						_, eErr = svc.EvaluateMatching(evalCtx, kinds...)
					}
					stop()
					if eErr != nil && !errors.Is(eErr, context.Canceled) {
						coreLogger.Warn("automation evaluator sweep held", "err", sanitizeDiagValue(eErr.Error(), 200))
					}
				}
				if len(kinds) == 0 || len(kinds) > 2 {
					if ts := s.Store.loadedOrExistingTradeService(); ts != nil {
						evalCtx, stop := context.WithTimeout(ctx, 15*time.Second)
						_, tErr := ts.EvaluateActive(evalCtx)
						stop()
						if tErr != nil && !errors.Is(tErr, context.Canceled) {
							coreLogger.Warn("trade evaluator sweep held", "err", sanitizeDiagValue(tErr.Error(), 200))
						}
					}
				}
			}

			attachBuses()
			notifyCh := s.Store.busNotifyCh

			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-notifyCh:
					if !ok {
						notifyCh = nil
						continue
					}
					attachBuses()
				case ev, ok := <-mdCh:
					if !ok {
						mdCh = nil
						continue
					}
					if ev.Type == "candle" || ev.Type == "drawing" {
						scheduleDebounce(&mdDebounce, &mdDebCh)
					}
				case <-mdDebCh:
					mdDebCh = nil
					runSweep(automation.KindPrice, automation.KindIndicator, automation.KindDrawingBreach, automation.KindFasedScript)
				case ev, ok := <-wiCh:
					if !ok {
						wiCh = nil
						continue
					}
					if ev.Type == "event" || ev.Type == "prediction" {
						scheduleDebounce(&wiDebounce, &wiDebCh)
					}
				case <-wiDebCh:
					wiDebCh = nil
					runSweep(automation.KindNews, automation.KindPrediction)
				case <-ticker.C:
					attachBuses()
					runSweep()
				}
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer recoverBackgroundPanic("telegram-poller")
			for {
				autoDB := filepath.Join(s.Store.dir, "automation", "automation.sqlite")
				s.Store.automationMu.Lock()
				initialized := s.Store.telegramSvc != nil
				s.Store.automationMu.Unlock()
				if !initialized {
					if _, statErr := os.Stat(autoDB); statErr != nil {
						timer := time.NewTimer(5 * time.Second)
						select {
						case <-ctx.Done():
							timer.Stop()
							return
						case <-timer.C:
						}
						continue
					}
				}
				tg, err := s.Store.telegramService()
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					coreLogger.Warn("telegram poller initialization held", "err", sanitizeDiagValue(err.Error(), 200))
					timer := time.NewTimer(10 * time.Second)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
					continue
				}
				tg.RunPollingLoop(ctx)
				return
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer recoverBackgroundPanic("worldintel-sync")
			intelDB := filepath.Join(s.Store.dir, "worldintel", "intel.sqlite")
			s.Store.worldIntelMu.Lock()
			ready := s.Store.worldIntelSvc != nil
			s.Store.worldIntelMu.Unlock()
			if !ready {
				if _, statErr := os.Stat(intelDB); statErr == nil {
					ready = true
				}
			}
			if ready {
				startupTimer := time.NewTimer(4 * time.Second)
				select {
				case <-ctx.Done():
					startupTimer.Stop()
					return
				case <-startupTimer.C:
				}
			} else {
				for {
					s.Store.worldIntelMu.Lock()
					initialized := s.Store.worldIntelSvc != nil
					s.Store.worldIntelMu.Unlock()
					if initialized {
						break
					}
					if _, statErr := os.Stat(intelDB); statErr == nil {
						break
					}
					waitTimer := time.NewTimer(5 * time.Second)
					select {
					case <-ctx.Done():
						waitTimer.Stop()
						return
					case <-waitTimer.C:
					}
				}
			}
			runSync := func() {
				svc, err := s.Store.worldIntelService()
				if err != nil {
					coreLogger.Warn("worldintel background service held", "err", sanitizeDiagValue(err.Error(), 200))
					return
				}
				syncCtx, stop := context.WithTimeout(ctx, 25*time.Second)
				res, syncErr := svc.Sync(syncCtx, "")
				stop()
				if syncErr != nil && !errors.Is(syncErr, context.Canceled) {
					coreLogger.Warn("worldintel background sync held", "err", sanitizeDiagValue(syncErr.Error(), 200))
				} else if len(res.Errors) > 0 {
					coreLogger.Warn("worldintel background sync partial source errors", "count", len(res.Errors))
				}
			}
			runSync()
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					runSync()
				}
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer recoverBackgroundPanic("desk-mark-positions")
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					deskDB := filepath.Join(s.Store.dir, "desk", "desk.sqlite")
					s.Store.deskMu.Lock()
					initialized := s.Store.deskSvc != nil
					s.Store.deskMu.Unlock()
					if !initialized {
						if _, statErr := os.Stat(deskDB); statErr != nil {
							continue
						}
					}
					svc, err := s.Store.deskService()
					if err != nil {
						continue
					}
					markCtx, stop := context.WithTimeout(ctx, 10*time.Second)
					if _, mErr := svc.MarkPositions(markCtx); mErr != nil && !errors.Is(mErr, context.Canceled) {
						coreLogger.Warn("desk mark positions held", "err", sanitizeDiagValue(mErr.Error(), 200))
					}
					stop()
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer recoverBackgroundPanic("sqlite-wal-checkpoint")
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c, stop := context.WithTimeout(ctx, 15*time.Second)
				if err := s.Store.CheckpointWAL(c); err != nil && !errors.Is(err, context.Canceled) {
					coreLogger.Warn("sqlite WAL checkpoint failed", "err", sanitizeDiagValue(err.Error(), 200))
				}
				stop()
				if mdSvc := s.Store.loadedMarketDataService(); mdSvc != nil {
					if _, pErr := mdSvc.PruneOldPartitions(365); pErr != nil {
						coreLogger.Warn("marketdata partition prune held", "err", sanitizeDiagValue(pErr.Error(), 200))
					}
				}
			}
		}
	}()
	go func() {
		wg.Wait()
		close(s.taskDone)
	}()
}
func (s *Server) Serve() error {
	errorsC := make(chan error, 4)
	go func() { errorsC <- s.HTTP.Serve(s.Listener) }()
	go func() { errorsC <- s.IPCServer.Serve(s.IPC) }()
	if s.RemoteHTTP != nil {
		go func() { errorsC <- s.RemoteHTTP.Serve(s.Remote) }()
		if s.RemoteLegacy != nil {
			go func() { errorsC <- s.RemoteHTTP.Serve(s.RemoteLegacy) }()
		}
	}
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
	// Cancel long-lived request contexts (e.g. SSE streams) immediately so
	// HTTP.Shutdown does not stall waiting for streaming responses to end.
	if s.serverCancel != nil {
		s.serverCancel()
	}
	err := errors.Join(s.HTTP.Shutdown(ctx), s.IPCServer.Shutdown(ctx))
	if s.RemoteHTTP != nil {
		err = errors.Join(err, s.RemoteHTTP.Shutdown(ctx))
		_ = s.RemoteHTTP.Close()
		if s.RemoteLegacy != nil {
			_ = s.RemoteLegacy.Close()
		}
	}
	if err != nil {
		_ = s.HTTP.Close()
		_ = s.IPCServer.Close()
	}
	// Keep a stale endpoint harmless: clients verify the instance ID before sending credentials.
	_ = os.Remove(filepath.Join(s.dir, "endpoint.json"))
	_ = os.Remove(filepath.Join(s.dir, localBootstrapNonceFile))
	s.Store.clearHyperliquidRuntime()
	evictCodexAppServerSessions()
	return errors.Join(err, s.Store.Close(), s.Lock.Close())
}

func (s *Server) writeEndpoint() error {
	if s.dir == "" || s.ipcAddress == "" {
		return nil
	}
	h := s.HTTP.Handler.(*HTTPHandler)
	return WritePrivateJSON(filepath.Join(s.dir, "endpoint.json"), Endpoint{
		URL:             s.URL,
		InstanceID:      s.Store.instanceID,
		Network:         s.ipcNetwork,
		Address:         s.ipcAddress,
		PublicOrigin:    h.publicOriginURL(),
		RemoteTransport: h.effectiveRemoteTransport(),
	})
}

// ConfigureConnection is called before Serve using the installed service's host
// profile. A browser preference can never change this runtime descriptor.
func (s *Server) ConfigureConnection(location string, owner ...int) error {
	return s.ConfigureConnectionWithTransport(location, "", owner...)
}

// ConfigureConnectionWithTransport binds the runtime host location and optional
// Remote ingress transport ("tailscale", "cloudflare", "domain", "proxy", "ssh").
func (s *Server) ConfigureConnectionWithTransport(location, remoteTransport string, owner ...int) error {
	if location != "local" && location != "remote" {
		return errors.New("instance location must be local or remote")
	}
	if s.Store != nil && s.Store.pilot.binding != nil && location != "local" {
		return errors.New("pilot is bound to Local mode")
	}
	if remoteTransport != "" && location != "remote" {
		return errors.New("remote transport requires a remote instance location")
	}
	switch remoteTransport {
	case "", "tailscale", "cloudflare", "domain", "proxy", "ssh":
	default:
		return errors.New("unsupported remote transport")
	}
	uid := 0
	if len(owner) > 1 {
		return errors.New("one installed owner identity is required")
	}
	if len(owner) == 1 {
		uid = owner[0]
	}
	if s.Store != nil && s.Store.unconfiguredProtectedSigner && uid != s.Store.protectedOwnerUID {
		return errors.New("verified pre-wallet owner identity required")
	}
	managedCustody := s.Store != nil && s.Store.fixture != nil && (s.Store.custodyNetwork != "" || s.Store.unconfiguredProtectedSigner)
	managedLocal := location == "local" && managedCustody
	if uid < 0 || int64(uid) > 4294967294 || (uid > 0 && location != "remote" && !managedLocal) {
		return errors.New("dedicated owner identity requires a verified managed profile and a valid UID")
	}
	h := s.HTTP.Handler.(*HTTPHandler)
	switch remoteTransport {
	case "tailscale":
		if h.publicHost == "" {
			return errors.New("Tailscale transport requires --public-origin")
		}
		if _, _, err := tailnet.Origin("https://" + h.publicHost); err != nil {
			return errors.New("Tailscale transport requires a .ts.net --public-origin")
		}
	case "cloudflare", "domain", "proxy":
		if h.publicHost == "" {
			return errors.New("proxy transport requires an exact HTTPS --public-origin")
		}
	case "ssh":
		if h.publicHost != "" {
			return errors.New("SSH tunnel transport cannot be combined with --public-origin")
		}
	}
	useTailscale := location == "remote" && h.publicHost != "" && (remoteTransport == "" || remoteTransport == "tailscale")
	if useTailscale {
		if s.RemoteHTTP != nil {
			return errors.New("Remote ingress is already configured")
		}
		listener, activated, err := tailnet.ActivatedListener(s.dir, uid)
		if err == nil && !activated {
			if tailnet.ProtectedDataSocketReady(s.dir, uid) {
				return errors.New("protected private listener was not passed by the service manager")
			}
			listener, err = tailnet.Listen(s.dir)
		}
		if err != nil {
			return err
		}
		if activated && !tailnet.ProtectedPackageData(s.dir, uid) {
			// Keep the original root-peer-only listener alive during the
			// conditional Serve handoff. It remains available if the route
			// change fails; no existing route is reset or silently adopted.
			// New package instances have no older route to hand off and must not
			// expose an app-writable replacement for their protected socket.
			s.RemoteLegacy, err = tailnet.Listen(s.dir)
			if err != nil {
				listener.Close()
				return err
			}
		}
		s.Remote = listener
		h.remoteIngress = true
		h.remoteProxyTrust = false
		h.remoteTransport = "tailscale"
		s.RemoteHTTP = &http.Server{Handler: h.remoteProxy(tailnet.WhoIs), BaseContext: func(net.Listener) context.Context { return s.serverCtx }, ConnContext: tailnet.ConnContext, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	} else if location == "remote" && (remoteTransport == "cloudflare" || remoteTransport == "domain" || remoteTransport == "proxy") {
		h.remoteIngress = false
		h.remoteProxyTrust = true
		h.remoteTransport = remoteTransport
	} else if location == "remote" {
		h.remoteIngress = false
		h.remoteProxyTrust = false
		h.remoteTransport = "ssh"
	} else {
		h.remoteIngress = false
		h.remoteProxyTrust = false
		h.remoteTransport = ""
	}
	h.instanceLocation, h.instanceOwnerUID = location, uid
	h.instanceOwnerKind = ""
	if uid > 0 && managedCustody {
		h.instanceOwnerKind = "custody"
	}
	return s.writeEndpoint()
}

// ConfigureInstallationSource is called only after main verifies its installed
// sandbox/configuration. Preferences, account metadata and HTTP cannot set it.
func (s *Server) ConfigureInstallationSource(source string) error {
	var value uint32
	switch source {
	case "":
	case "local":
		value = 1
	case "remote":
		value = 2
	default:
		return errors.New("unknown installation source")
	}
	if s.Store == nil {
		return errors.New("core store unavailable")
	}
	s.Store.installationSource.Store(value)
	return nil
}

// ConfigureRequirePassword switches Local instances from instant loopback
// auto-entry back to explicit launch links or owner passwords.
func (s *Server) ConfigureRequirePassword(v bool) {
	s.HTTP.Handler.(*HTTPHandler).SetRequirePassword(v)
}
