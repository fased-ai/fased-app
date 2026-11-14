package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

func wenFinancialCommand(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: wen-financial context|read|history|receipt|treasury-receipt|operating-receipt --data-dir PATH --input-file PATH")
	}
	action := args[0]
	switch action {
	case "context", "read", "history", "receipt", "treasury-receipt", "operating-receipt":
	default:
		return errors.New("unsupported financial read")
	}
	flags := flag.NewFlagSet("wen-financial "+action, flag.ContinueOnError)
	flags.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := flags.String("data-dir", base, "private core directory")
	path := flags.String("input-file", "", "public financial selection JSON")
	if e = flags.Parse(args[1:]); e != nil {
		return e
	}
	if flags.NArg() != 0 || (action == "context" && *path != "") || (action != "context" && *path == "") {
		return errors.New("invalid financial read arguments")
	}
	var input any
	method := http.MethodGet
	if action != "context" {
		f, e := os.Open(*path)
		if e != nil {
			return e
		}
		data, e := io.ReadAll(io.LimitReader(f, 4097))
		f.Close()
		if e != nil || len(data) > 4096 || !json.Valid(data) {
			return errors.New("invalid bounded financial input")
		}
		input = json.RawMessage(data)
		method = http.MethodPost
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, e := core.NewClient(ctx, *dir)
	if e != nil {
		return e
	}
	defer c.Close()
	c.HTTP.Timeout = 18 * time.Second
	if e = c.LoadSession(ctx, *dir); e != nil {
		return e
	}
	var result json.RawMessage
	if e = c.Do(ctx, method, "/api/v1/wen/financial/"+action, input, &result); e != nil {
		return e
	}
	_, e = out.Write(append(result, '\n'))
	return e
}
