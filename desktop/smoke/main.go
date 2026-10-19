//go:build linux

// This development-only runner is not part of any installed client or release.
package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/desktop/internal/window"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed smoke.js
var script string

func main() {
	data, err := filepath.Abs(os.Getenv("FASED_NATIVE_SMOKE_DATA"))
	if err != nil || !strings.HasSuffix(filepath.ToSlash(data), "/.evidence/native-smoke/state") {
		fmt.Fprintln(os.Stderr, "requires declared generated smoke fixture")
		os.Exit(1)
	}
	marker, err := core.ReadPrivateFile(filepath.Join(filepath.Dir(data), "fixture.json"))
	if err != nil || !strings.Contains(string(marker), `"nonauthoritative":true`) {
		fmt.Fprintln(os.Stderr, "missing fixture marker")
		os.Exit(1)
	}
	code, err := core.ReadPrivateFile(filepath.Join(data, "claim-code.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "missing fresh claim fixture")
		os.Exit(1)
	}
	var claim core.ClaimCode
	if json.Unmarshal(code, &claim) != nil {
		os.Exit(1)
	}
	password := make([]byte, 24)
	if _, err = rand.Read(password); err != nil {
		os.Exit(1)
	}
	fixture, _ := json.Marshal(map[string]string{"code": claim.Code, "password": "FixtureOnly-" + hex.EncodeToString(password)})
	js := strings.Replace(script, `"__FASED_FIXTURE__"`, string(fixture), 1)
	options := window.Options(data)
	delegate := options.AssetServer.Handler
	options.AssetServer.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := &statusWriter{ResponseWriter: w, status: 200}
		delegate.ServeHTTP(writer, r)
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/") {
			_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"nativeRequest": r.Method, "path": r.URL.Path, "host": r.Host, "scheme": r.URL.Scheme, "origin": r.Header.Get("Origin"), "contentType": r.Header.Get("Content-Type"), "status": writer.status})
		}
	})
	var mu sync.Mutex
	result := map[string]any{"ok": false, "error": "native UI timed out"}
	options.OnStartup = func(ctx context.Context) {
		runtime.EventsOn(ctx, "fased-native-smoke", func(args ...interface{}) {
			if len(args) == 1 {
				if text, ok := args[0].(string); ok {
					var received map[string]any
					if json.Unmarshal([]byte(text), &received) == nil {
						mu.Lock()
						result = received
						mu.Unlock()
					}
				}
			}
			runtime.Quit(ctx)
		})
		go func() { time.Sleep(30 * time.Second); runtime.Quit(ctx) }()
	}
	var once sync.Once
	options.OnDomReady = func(ctx context.Context) { once.Do(func() { runtime.WindowExecJS(ctx, js) }) }
	if err = wails.Run(options); err != nil {
		fmt.Fprintln(os.Stderr, "native window failed:", err)
		os.Exit(1)
	}
	mu.Lock()
	defer mu.Unlock()
	_ = json.NewEncoder(os.Stdout).Encode(result)
	if result["ok"] != true {
		os.Exit(1)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
