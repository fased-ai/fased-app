package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"net/http"
	"net/url"
	"time"
)

func wenMarketDataCommand(args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("wen-market-data", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := f.String("data-dir", base, "private core directory")
	lookback := f.String("lookback", "1", "research lookback minutes")
	profile := f.String("profile", "", "optionally prepare source-bound inert Chat context for this private profile")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	switch *lookback {
	case "1", "30", "60", "240", "720", "1440":
	default:
		return errors.New("unsupported research lookback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, e := core.NewClient(ctx, *dir)
	if e != nil {
		return e
	}
	defer c.Close()
	if e = c.LoadSession(ctx, *dir); e != nil {
		return e
	}
	var result json.RawMessage
	route := "/api/v1/wen/market-data?lookback=" + *lookback
	if *profile != "" {
		route += "&profile=" + url.QueryEscape(*profile)
	}
	if e = c.Do(ctx, http.MethodGet, route, nil, &result); e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(result)
}
