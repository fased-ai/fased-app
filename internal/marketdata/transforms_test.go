package marketdata

import (
	"math"
	"testing"
)

func sampleCandles(n int) []Candle {
	out := make([]Candle, n)
	price := 100.0
	baseTs := int64(1760000000)
	for i := 0; i < n; i++ {
		delta := math.Sin(float64(i)*0.45)*3.5 + 0.6
		o := price
		c := math.Max(o+delta, 20)
		h := math.Max(o, c) + 1.8
		l := math.Max(math.Min(o, c)-1.4, 10)
		out[i] = Candle{
			Time:       baseTs + int64(i*3600),
			Open:       o,
			High:       h,
			Low:        l,
			Close:      c,
			Volume:     250 + float64(i*10),
			BuyVolume:  150,
			SellVolume: 100 + float64(i*10),
			Trades:     30,
		}
		price = c
	}
	return out
}

func TestTransformCandlesAllStyles(t *testing.T) {
	base := sampleCandles(60)
	styles := []TransformOptions{
		{Style: StyleStandard},
		{Style: StyleHeikinAshi},
		{Style: StyleRenko, BrickSize: 1.5},
		{Style: StyleRange, RangeSize: 2.5},
		{Style: StyleKagi, Reversal: 0.02},
		{Style: StylePointFigure, BoxSize: 1.5, Reversal: 3},
		{Style: StyleThreeLineBreak, LineBreakCount: 3},
		{Style: StyleTick, TickCount: 60},
		{Style: StyleVolume, VolumeThreshold: 700},
	}

	for _, opt := range styles {
		t.Run(opt.Style, func(t *testing.T) {
			out, err := TransformCandles(base, opt)
			if err != nil {
				t.Fatalf("TransformCandles(%s) error: %v", opt.Style, err)
			}
			if len(out) == 0 {
				t.Fatalf("TransformCandles(%s) returned 0 bars", opt.Style)
			}
			var prevTime int64
			for i, b := range out {
				if !isFinitePositive(b.Open) || !isFinitePositive(b.High) || !isFinitePositive(b.Low) || !isFinitePositive(b.Close) {
					t.Fatalf("%s bar %d non-finite/non-positive: %+v", opt.Style, i, b)
				}
				if b.High < b.Open || b.High < b.Close || b.Low > b.Open || b.Low > b.Close {
					t.Fatalf("%s bar %d invalid HL envelope: %+v", opt.Style, i, b)
				}
				if i > 0 && b.Time <= prevTime {
					t.Fatalf("%s bar %d non-monotonic timestamp %d <= %d", opt.Style, i, b.Time, prevTime)
				}
				prevTime = b.Time
			}
		})
	}

	if _, err := TransformCandles([]Candle{{Time: 1, Open: math.NaN(), High: 10, Low: 9, Close: 10}}, TransformOptions{Style: StyleHeikinAshi}); err == nil {
		t.Fatal("expected NaN candle rejection")
	}
}

func TestAggregateTicksToCandles(t *testing.T) {
	var ticks []TradeTick
	for i := 0; i < 25; i++ {
		side := "buy"
		if i%3 == 0 {
			side = "sell"
		}
		ticks = append(ticks, TradeTick{
			ID:    "t",
			Time:  1760000000000 + int64(i*250),
			Price: 100 + float64(i)*0.5,
			Size:  2.0,
			Side:  side,
		})
	}
	tickBars, err := AggregateTicksToCandles(ticks, TransformOptions{Style: StyleTick, TickCount: 5})
	if err != nil {
		t.Fatalf("tick aggregation failed: %v", err)
	}
	if len(tickBars) != 5 {
		t.Fatalf("expected 5 tick bars, got %d", len(tickBars))
	}
	volBars, err := AggregateTicksToCandles(ticks, TransformOptions{Style: StyleVolume, VolumeThreshold: 10.0})
	if err != nil {
		t.Fatalf("volume aggregation failed: %v", err)
	}
	if len(volBars) != 5 {
		t.Fatalf("expected 5 volume bars (10.0 each = 5 ticks * 2.0), got %d", len(volBars))
	}
}

func TestComputeFootprintAndProfile(t *testing.T) {
	candles := []Candle{
		{Time: 1000, Open: 100, High: 104, Low: 99, Close: 103, Volume: 100},
		{Time: 2000, Open: 103, High: 106, Low: 101, Close: 102, Volume: 80},
	}
	ticks := []TradeTick{
		{Time: 1000 * 1000, Price: 101.0, Size: 10, Side: "buy"},
		{Time: 1100 * 1000, Price: 101.0, Size: 4, Side: "sell"},
		{Time: 1200 * 1000, Price: 102.0, Size: 20, Side: "buy"},
		{Time: 2000 * 1000, Price: 104.0, Size: 15, Side: "sell"},
		{Time: 2100 * 1000, Price: 104.0, Size: 5, Side: "buy"},
	}

	bars, err := ComputeFootprintAndProfile(candles, ticks, 1.0)
	if err != nil {
		t.Fatalf("ComputeFootprintAndProfile failed: %v", err)
	}
	if len(bars) != 2 {
		t.Fatalf("expected 2 footprint bars, got %d", len(bars))
	}
	// Bar 0: buy=30, sell=4 -> delta=+26, vpoc=102
	if math.Abs(bars[0].Delta-26.0) > 1e-6 || math.Abs(bars[0].VPOC-102.0) > 1e-6 {
		t.Fatalf("unexpected bar 0 footprint: %+v", bars[0])
	}
	if bars[0].VAL > bars[0].VPOC || bars[0].VAH < bars[0].VPOC {
		t.Fatalf("invalid value area ordering bar 0: VAL=%.2f VPOC=%.2f VAH=%.2f", bars[0].VAL, bars[0].VPOC, bars[0].VAH)
	}
	// Bar 1: buy=5, sell=15 -> delta=-10 -> CVD = 26 - 10 = 16
	if math.Abs(bars[1].Delta-(-10.0)) > 1e-6 || math.Abs(bars[1].CVD-16.0) > 1e-6 {
		t.Fatalf("unexpected bar 1 CVD/Delta: %+v", bars[1])
	}
}
