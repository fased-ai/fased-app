package fasedscript

import (
	"context"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/marketdata"
)

func TestWaveS1StringAmplificationOOMBlockedAndContextCancel(t *testing.T) {
	candles := []marketdata.Candle{
		{Time: 1, Open: 100, High: 105, Low: 99, Close: 102, Volume: 10},
		{Time: 2, Open: 102, High: 106, Low: 101, Close: 104, Volume: 12},
	}

	// H8: Repeated string doubling MUST fail cleanly at MaxStringBytes before OOM.
	src := `
let s = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
s = s + s;
s = s + s;
s = s + s;
s = s + s;
s = s + s;
s = s + s;
s = s + s;
`
	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	_, err = prog.Eval(candles, nil)
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("expected string byte limit error, got %v", err)
	}

	// H8: Canceled context halts evaluation immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	okProg, err := Compile(`let x = close; plot(x, "c");`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := okProg.EvalContext(ctx, candles, nil); err == nil {
		t.Fatal("expected canceled context error from EvalContext")
	}
}
