package wenlv1x

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
)

func (e *Engine) ListSavedStrategies(ctx context.Context) ([]SavedStrategy, error) {
	rows, err := e.db.QueryContext(ctx, `SELECT id, name, description, mode, buckets_json, curve_json, source, created_at, updated_at FROM lv1x_saved_strategies ORDER BY updated_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SavedStrategy, 0)
	for rows.Next() {
		var s SavedStrategy
		var bRaw, cRaw string
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Mode, &bRaw, &cRaw, &s.Source, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(bRaw), &s.Buckets)
		_ = json.Unmarshal([]byte(cRaw), &s.Curve)
		s.Curve = NormalizeCurve(s.Curve)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (e *Engine) SaveStrategy(ctx context.Context, in SavedStrategy) (SavedStrategy, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return SavedStrategy{}, errors.New("strategy name is required")
	}
	if len(name) > 80 {
		name = name[:80]
	}
	curve := NormalizeCurve(in.Curve)
	buckets := BucketsFromCurve(curve)
	if len(in.Curve) != CurveBarCount {
		var sum int
		for _, v := range in.Buckets {
			sum += int(v)
		}
		if sum > 0 {
			buckets = normalizeBucketsSum(in.Buckets)
			curve = CurveFromBuckets(buckets)
		}
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		var b [5]byte
		_, _ = rand.Read(b[:])
		id = "strat-" + hex.EncodeToString(b[:])
	}
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode != "directional" && mode != "defensive" {
		mode = "neutral"
	}
	src := strings.TrimSpace(in.Source)
	if src == "" {
		src = "custom"
	}
	nowSec := e.now().UTC().Unix()
	out := SavedStrategy{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(in.Description),
		Mode:        mode,
		Buckets:     buckets,
		Curve:       curve,
		Source:      src,
		CreatedAt:   nowSec,
		UpdatedAt:   nowSec,
	}
	bRaw, _ := json.Marshal(out.Buckets)
	cRaw, _ := json.Marshal(out.Curve)
	if _, err := e.db.ExecContext(ctx, `
INSERT INTO lv1x_saved_strategies(id, name, description, mode, buckets_json, curve_json, source, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, mode=excluded.mode, buckets_json=excluded.buckets_json, curve_json=excluded.curve_json, source=excluded.source, updated_at=excluded.updated_at`,
		out.ID, out.Name, out.Description, out.Mode, string(bRaw), string(cRaw), out.Source, out.CreatedAt, out.UpdatedAt,
	); err != nil {
		return SavedStrategy{}, err
	}
	return out, nil
}

func (e *Engine) DeleteStrategy(ctx context.Context, id string) error {
	clean := strings.TrimSpace(id)
	if clean == "" {
		return errors.New("strategy id required")
	}
	res, err := e.db.ExecContext(ctx, `DELETE FROM lv1x_saved_strategies WHERE id = ?`, clean)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("strategy not found")
	}
	return nil
}

func normalizeBucketsSum(raw [5]uint16) [5]uint16 {
	var total int
	for _, v := range raw {
		total += int(v)
	}
	if total <= 0 {
		return BuiltInPresetWeights["wide"]
	}
	var out [5]uint16
	var assigned int
	for i := range 5 {
		v := int(math.Round((float64(raw[i]) / float64(total)) * 10000.0))
		if v < 0 {
			v = 0
		}
		out[i] = uint16(v)
		assigned += v
	}
	if assigned != 10000 {
		maxI := 0
		for i := 1; i < 5; i++ {
			if out[i] > out[maxI] {
				maxI = i
			}
		}
		out[maxI] = uint16(max(0, int(out[maxI])+10000-assigned))
	}
	return out
}

// SimulateStrategy evaluates the WEN exact + near-miss scoring formula across historical rounds.
func (e *Engine) SimulateStrategy(ctx context.Context, req SimulateRequest) (SimulateResponse, error) {
	weightsBps := normalizeBucketsSum(req.BucketsBps)
	if len(req.Curve) == CurveBarCount {
		weightsBps = BucketsFromCurve(req.Curve)
	}
	perRound := req.PerRoundSOL
	if perRound <= 0 {
		perRound = 0.05
	}
	limit := req.Limit
	if limit <= 0 || limit > 48 {
		limit = 24
	}
	rounds, err := e.ListStreak(ctx, limit)
	if err != nil {
		return SimulateResponse{}, err
	}
	var weights [5]float64
	for i := range 5 {
		weights[i] = float64(weightsBps[i]) / 10000.0
	}

	var totalCommitted, totalReturned, exactStakeSum, creditSum, estSat float64
	outcomes := make([]SimulateOutcome, 0, len(rounds))

	for _, r := range rounds {
		inner := r.InnerBps
		if inner < 4 {
			inner = 5
		}
		outer := r.OuterBps
		if outer < inner*2 {
			outer = 20
		}
		win := int(r.WinningRange)
		moveBpsExact := float64(r.MoveBps)
		bounds := [4]float64{float64(-outer), float64(-inner), float64(inner), float64(outer)}
		var roundRet, roundCredit float64

		for sel := range 5 {
			w := weights[sel]
			if w <= 0 {
				continue
			}
			dist := sel - win
			if dist < 0 {
				dist = -dist
			}
			switch dist {
			case 0:
				roundRet += w * 0.995
				roundCredit += w * 1.0
			case 1:
				var edge float64
				if sel < win {
					edge = bounds[sel]
				} else {
					edge = bounds[sel-1]
				}
				width := math.Max(1, math.Min(float64(2*inner), float64(outer-inner)))
				prox := math.Max(0, math.Min(1, 1.0-math.Abs(moveBpsExact-edge)/width))
				roundRet += w * (0.90 + 0.05*prox)
				roundCredit += w * prox
			default:
				roundRet += w * 0.90
			}
		}

		exactW := weights[win]
		var adjW float64
		if win > 0 {
			adjW += weights[win-1]
		}
		if win < 4 {
			adjW += weights[win+1]
		}
		kind := "miss"
		if exactW >= 0.2 {
			kind = "exact"
		} else if exactW > 0 || adjW >= 0.2 {
			kind = "near"
		}
		exactStakeSum += perRound * exactW
		totalCommitted += perRound
		totalReturned += perRound * roundRet
		creditSum += roundCredit
		pool := math.Max(1.0, r.PoolSOL)
		minerSat := r.MinerSat
		if minerSat <= 0 {
			minerSat = 120.0
		}
		estSat += minerSat * ((perRound * roundCredit) / (pool + perRound*roundCredit))
		wCopy := r.WinningRange
		outcomes = append(outcomes, SimulateOutcome{
			RoundSlot:    r.RoundSlot,
			WinningRange: &wCopy,
			Kind:         kind,
			ReturnPct:    math.Round(roundRet*10000) / 100.0,
		})
	}

	n := len(outcomes)
	exactHit := 0.0
	retained := 100.0
	if totalCommitted > 0 {
		exactHit = (exactStakeSum / totalCommitted) * 100.0
		retained = (totalReturned / totalCommitted) * 100.0
	}
	avgCredit := 0.0
	if n > 0 {
		avgCredit = (creditSum / float64(n)) * 100.0
	}
	return SimulateResponse{
		Rounds:         n,
		ExactHitPct:    math.Round(exactHit*100) / 100.0,
		AvgCreditPct:   math.Round(avgCredit*100) / 100.0,
		RetainedPct:    math.Round(retained*100) / 100.0,
		NetSOL:         math.Round((totalReturned-totalCommitted)*1e6) / 1e6,
		EstSat:         math.Round(estSat*1000) / 1000.0,
		TotalCommitted: math.Round(totalCommitted*1e6) / 1e6,
		Outcomes:       outcomes,
	}, nil
}
