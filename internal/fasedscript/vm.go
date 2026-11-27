package fasedscript

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/fased-ai/fased-app/internal/marketdata"
)

const (
	MaxStringBytes = 1024
	MaxAllocBytes  = 16 * 1024 * 1024
)

type valKind int

const (
	valNull valKind = iota
	valFloat
	valBool
	valStr
	valSeriesFloat
	valSeriesBool
	valObject
	valArray
	valBuiltin
)

type val struct {
	kind valKind
	f    float64
	b    bool
	s    string
	sf   []float64
	sb   []bool
	obj  map[string]val
	arr  []val
	fn   string
}

type envScope struct {
	parent *envScope
	vars   map[string]val
	consts map[string]bool
}

func newScope(parent *envScope) *envScope {
	return &envScope{
		parent: parent,
		vars:   make(map[string]val),
		consts: make(map[string]bool),
	}
}

func (e *envScope) define(name string, v val, isConst bool) {
	e.vars[name] = v
	if isConst {
		e.consts[name] = true
	}
}

func (e *envScope) get(name string) (val, bool) {
	for cur := e; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
			return v, true
		}
	}
	return val{}, false
}

func (e *envScope) assign(name string, v val) error {
	for cur := e; cur != nil; cur = cur.parent {
		if _, ok := cur.vars[name]; ok {
			if cur.consts[name] {
				return fmt.Errorf("cannot reassign const variable %q", name)
			}
			cur.vars[name] = v
			return nil
		}
	}
	return fmt.Errorf("undefined variable %q", name)
}

type vm struct {
	ctx        context.Context
	candles    []marketdata.Candle
	barCount   int
	params     map[string]float64
	resolved   map[string]float64
	inputs     []InputSpec
	seenIn     map[string]bool
	plots      []PlotSeries
	signals    []SignalMarker
	strategy   StrategySpec
	warnings   []string
	steps      int
	allocBytes int
}

// Eval executes an immutable compiled FasedScript Program against causal OHLCV bars.
// Safe for concurrent calls across goroutines with distinct parameter overrides.
func (p *Program) Eval(candles []marketdata.Candle, params map[string]float64) (EvalResult, error) {
	return p.EvalContext(context.Background(), candles, params)
}

// EvalContext executes an immutable compiled FasedScript Program with context cancellation/deadline support.
func (p *Program) EvalContext(ctx context.Context, candles []marketdata.Candle, params map[string]float64) (EvalResult, error) {
	if p == nil {
		return EvalResult{}, errors.New("nil FasedScript program")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return EvalResult{}, err
	}
	n := len(candles)
	if n == 0 {
		return EvalResult{}, errors.New("at least 1 candle required for evaluation")
	}
	if n > MaxCandles {
		return EvalResult{}, fmt.Errorf("candle count %d exceeds %d maximum", n, MaxCandles)
	}

	m := &vm{
		ctx:      ctx,
		candles:  candles,
		barCount: n,
		params:   params,
		resolved: make(map[string]float64),
		seenIn:   make(map[string]bool),
	}

	openS := make([]float64, n)
	highS := make([]float64, n)
	lowS := make([]float64, n)
	closeS := make([]float64, n)
	volS := make([]float64, n)
	timeS := make([]float64, n)
	hl2S := make([]float64, n)
	hlc3S := make([]float64, n)
	ohlc4S := make([]float64, n)

	for i, c := range candles {
		openS[i] = c.Open
		highS[i] = c.High
		lowS[i] = c.Low
		closeS[i] = c.Close
		volS[i] = c.Volume
		timeS[i] = float64(c.Time)
		hl2S[i] = (c.High + c.Low) / 2.0
		hlc3S[i] = (c.High + c.Low + c.Close) / 3.0
		ohlc4S[i] = (c.Open + c.High + c.Low + c.Close) / 4.0
	}

	root := newScope(nil)
	root.define("open", val{kind: valSeriesFloat, sf: openS}, true)
	root.define("high", val{kind: valSeriesFloat, sf: highS}, true)
	root.define("low", val{kind: valSeriesFloat, sf: lowS}, true)
	root.define("close", val{kind: valSeriesFloat, sf: closeS}, true)
	root.define("volume", val{kind: valSeriesFloat, sf: volS}, true)
	root.define("time", val{kind: valSeriesFloat, sf: timeS}, true)
	root.define("hl2", val{kind: valSeriesFloat, sf: hl2S}, true)
	root.define("hlc3", val{kind: valSeriesFloat, sf: hlc3S}, true)
	root.define("ohlc4", val{kind: valSeriesFloat, sf: ohlc4S}, true)
	root.define("barCount", val{kind: valFloat, f: float64(n)}, true)

	for _, name := range builtinNames {
		root.define(name, val{kind: valBuiltin, fn: name}, true)
	}
	root.define("Math", val{
		kind: valObject,
		obj: map[string]val{
			"abs":   {kind: valBuiltin, fn: "abs"},
			"min":   {kind: valBuiltin, fn: "min"},
			"max":   {kind: valBuiltin, fn: "max"},
			"sqrt":  {kind: valBuiltin, fn: "sqrt"},
			"log":   {kind: valBuiltin, fn: "log"},
			"pow":   {kind: valBuiltin, fn: "pow"},
			"round": {kind: valBuiltin, fn: "round"},
			"floor": {kind: valBuiltin, fn: "floor"},
			"ceil":  {kind: valBuiltin, fn: "ceil"},
		},
	}, true)

	for _, st := range p.stmts {
		if err := m.execStmt(st, root); err != nil {
			return EvalResult{}, err
		}
	}

	inputs := m.inputs
	if len(inputs) == 0 && len(p.Inputs) > 0 {
		inputs = append([]InputSpec(nil), p.Inputs...)
	}

	return EvalResult{
		Inputs:    inputs,
		Params:    m.resolved,
		Plots:     m.plots,
		Signals:   m.signals,
		Strategy:  m.strategy,
		StepsUsed: m.steps,
		Warnings:  m.warnings,
		BarCount:  n,
	}, nil
}

func (m *vm) step(cost int) error {
	if m.ctx != nil && m.ctx.Err() != nil {
		return m.ctx.Err()
	}
	m.steps += cost
	if m.steps > MaxEvalSteps {
		return fmt.Errorf("FasedScript execution exceeded %d step budget", MaxEvalSteps)
	}
	return nil
}

func (m *vm) chargeAlloc(bytes int) error {
	if bytes <= 0 {
		return nil
	}
	m.allocBytes += bytes
	if m.allocBytes > MaxAllocBytes {
		return fmt.Errorf("FasedScript allocation exceeded %d byte memory budget", MaxAllocBytes)
	}
	return nil
}

func (m *vm) execStmt(st stmt, scope *envScope) error {
	if err := m.step(1); err != nil {
		return err
	}
	switch s := st.(type) {
	case *varDeclStmt:
		v, err := m.evalExpr(s.init, scope)
		if err != nil {
			return err
		}
		scope.define(s.name, v, s.isConst)
		return nil

	case *assignStmt:
		v, err := m.evalExpr(s.val, scope)
		if err != nil {
			return err
		}
		return scope.assign(s.name, v)

	case *blockStmt:
		child := newScope(scope)
		for _, inner := range s.stmts {
			if err := m.execStmt(inner, child); err != nil {
				return err
			}
		}
		return nil

	case *ifStmt:
		cond, err := m.evalExpr(s.cond, scope)
		if err != nil {
			return err
		}
		if cond.kind == valSeriesBool || cond.kind == valSeriesFloat {
			return errors.New("if condition cannot be a bar series (causes look-ahead bias); use bar-wise ternary (cond ? a : b) or boolean operators (&& / ||)")
		}
		if asScalarBool(cond) {
			return m.execStmt(s.then, scope)
		}
		if s.els != nil {
			return m.execStmt(s.els, scope)
		}
		return nil

	case *exprStmt:
		_, err := m.evalExpr(s.e, scope)
		return err

	default:
		return errors.New("unsupported statement")
	}
}

func (m *vm) evalExpr(ex expr, scope *envScope) (val, error) {
	if err := m.step(1); err != nil {
		return val{}, err
	}
	switch e := ex.(type) {
	case *literalExpr:
		switch e.kind {
		case "float":
			return val{kind: valFloat, f: e.f}, nil
		case "bool":
			return val{kind: valBool, b: e.b}, nil
		case "string":
			return val{kind: valStr, s: e.s}, nil
		default:
			return val{kind: valNull}, nil
		}

	case *identExpr:
		v, ok := scope.get(e.name)
		if !ok {
			return val{}, fmt.Errorf("line %d: undefined identifier %q", e.line, e.name)
		}
		return v, nil

	case *unaryExpr:
		rhs, err := m.evalExpr(e.rhs, scope)
		if err != nil {
			return val{}, err
		}
		return m.evalUnary(e.op, rhs)

	case *binaryExpr:
		lhs, err := m.evalExpr(e.lhs, scope)
		if err != nil {
			return val{}, err
		}
		// Short-circuit scalar boolean operators when possible
		if e.op == tokAnd && lhs.kind == valBool && !lhs.b {
			return val{kind: valBool, b: false}, nil
		}
		if e.op == tokOr && lhs.kind == valBool && lhs.b {
			return val{kind: valBool, b: true}, nil
		}
		rhs, err := m.evalExpr(e.rhs, scope)
		if err != nil {
			return val{}, err
		}
		return m.evalBinary(e.op, lhs, rhs)

	case *ternaryExpr:
		cond, err := m.evalExpr(e.cond, scope)
		if err != nil {
			return val{}, err
		}
		if cond.kind != valSeriesBool && cond.kind != valSeriesFloat {
			if asScalarBool(cond) {
				return m.evalExpr(e.t, scope)
			}
			return m.evalExpr(e.f, scope)
		}
		tVal, err := m.evalExpr(e.t, scope)
		if err != nil {
			return val{}, err
		}
		fVal, err := m.evalExpr(e.f, scope)
		if err != nil {
			return val{}, err
		}
		return m.evalVecTernary(cond, tVal, fVal)

	case *memberExpr:
		obj, err := m.evalExpr(e.obj, scope)
		if err != nil {
			return val{}, err
		}
		if obj.kind != valObject {
			return val{}, fmt.Errorf("line %d: cannot read property %q on non-object", e.line, e.prop)
		}
		if prop, ok := obj.obj[e.prop]; ok {
			return prop, nil
		}
		return val{kind: valNull}, nil

	case *arrayExpr:
		arr := make([]val, 0, len(e.elems))
		for _, el := range e.elems {
			ev, err := m.evalExpr(el, scope)
			if err != nil {
				return val{}, err
			}
			arr = append(arr, ev)
		}
		return val{kind: valArray, arr: arr}, nil

	case *objectExpr:
		obj := make(map[string]val, len(e.fields))
		for _, f := range e.fields {
			fv, err := m.evalExpr(f.val, scope)
			if err != nil {
				return val{}, err
			}
			obj[f.key] = fv
		}
		return val{kind: valObject, obj: obj}, nil

	case *callExpr:
		callee, err := m.evalExpr(e.callee, scope)
		if err != nil {
			return val{}, err
		}
		if callee.kind != valBuiltin {
			return val{}, fmt.Errorf("line %d: expression is not callable", e.line)
		}
		args := make([]val, 0, len(e.args))
		for _, a := range e.args {
			av, err := m.evalExpr(a, scope)
			if err != nil {
				return val{}, err
			}
			args = append(args, av)
		}
		return m.callBuiltin(callee.fn, args)
	}

	return val{}, errors.New("unsupported expression")
}

func asScalarBool(v val) bool {
	switch v.kind {
	case valBool:
		return v.b
	case valFloat:
		return isFinite(v.f) && v.f != 0
	case valStr:
		return v.s != ""
	case valSeriesBool:
		return len(v.sb) > 0 && v.sb[len(v.sb)-1]
	case valSeriesFloat:
		return len(v.sf) > 0 && isFinite(v.sf[len(v.sf)-1]) && v.sf[len(v.sf)-1] != 0
	default:
		return false
	}
}

func (m *vm) toBoolSeries(v val) ([]bool, error) {
	switch v.kind {
	case valSeriesBool:
		return v.sb, nil
	case valBool:
		out := make([]bool, m.barCount)
		if v.b {
			for i := range out {
				out[i] = true
			}
		}
		return out, nil
	case valSeriesFloat:
		out := make([]bool, m.barCount)
		for i := 0; i < m.barCount && i < len(v.sf); i++ {
			out[i] = isFinite(v.sf[i]) && v.sf[i] != 0
		}
		return out, nil
	case valFloat:
		out := make([]bool, m.barCount)
		if isFinite(v.f) && v.f != 0 {
			for i := range out {
				out[i] = true
			}
		}
		return out, nil
	default:
		return nil, errors.New("expected boolean or condition series")
	}
}

func (m *vm) toFloatSeries(v val) ([]float64, error) {
	switch v.kind {
	case valSeriesFloat:
		return v.sf, nil
	case valFloat:
		out := make([]float64, m.barCount)
		for i := range out {
			out[i] = v.f
		}
		return out, nil
	case valBool:
		out := make([]float64, m.barCount)
		if v.b {
			for i := range out {
				out[i] = 1
			}
		}
		return out, nil
	case valSeriesBool:
		out := make([]float64, m.barCount)
		for i := 0; i < m.barCount && i < len(v.sb); i++ {
			if v.sb[i] {
				out[i] = 1
			}
		}
		return out, nil
	default:
		return nil, errors.New("expected numeric value or series")
	}
}

func asScalarFloat(v val) (float64, error) {
	switch v.kind {
	case valFloat:
		return v.f, nil
	case valBool:
		if v.b {
			return 1, nil
		}
		return 0, nil
	default:
		return 0, errors.New("expected numeric scalar")
	}
}

func asScalarInt(v val, minVal, maxVal int) (int, error) {
	f, err := asScalarFloat(v)
	if err != nil || !isFinite(f) {
		return 0, errors.New("expected finite integer scalar")
	}
	iv := int(math.Round(f))
	if iv < minVal || iv > maxVal {
		return 0, fmt.Errorf("integer parameter %d out of range [%d, %d]", iv, minVal, maxVal)
	}
	return iv, nil
}

func (m *vm) evalUnary(op tokenKind, rhs val) (val, error) {
	switch op {
	case tokBang:
		if rhs.kind == valSeriesBool || rhs.kind == valSeriesFloat {
			sb, err := m.toBoolSeries(rhs)
			if err != nil {
				return val{}, err
			}
			out := make([]bool, len(sb))
			for i, b := range sb {
				out[i] = !b
			}
			return val{kind: valSeriesBool, sb: out}, nil
		}
		return val{kind: valBool, b: !asScalarBool(rhs)}, nil

	case tokMinus:
		if rhs.kind == valSeriesFloat {
			out := make([]float64, len(rhs.sf))
			for i, f := range rhs.sf {
				out[i] = -f
			}
			return val{kind: valSeriesFloat, sf: out}, nil
		}
		f, err := asScalarFloat(rhs)
		if err != nil {
			return val{}, err
		}
		return val{kind: valFloat, f: -f}, nil

	case tokPlus:
		return rhs, nil
	}
	return val{}, errors.New("unsupported unary operator")
}

func (m *vm) evalBinary(op tokenKind, lhs, rhs val) (val, error) {
	switch op {
	case tokAnd, tokOr:
		isSeries := lhs.kind == valSeriesBool || rhs.kind == valSeriesBool || lhs.kind == valSeriesFloat || rhs.kind == valSeriesFloat
		if !isSeries {
			lb, rb := asScalarBool(lhs), asScalarBool(rhs)
			if op == tokAnd {
				return val{kind: valBool, b: lb && rb}, nil
			}
			return val{kind: valBool, b: lb || rb}, nil
		}
		lsb, err := m.toBoolSeries(lhs)
		if err != nil {
			return val{}, err
		}
		rsb, err := m.toBoolSeries(rhs)
		if err != nil {
			return val{}, err
		}
		out := make([]bool, m.barCount)
		for i := 0; i < m.barCount; i++ {
			if op == tokAnd {
				out[i] = lsb[i] && rsb[i]
			} else {
				out[i] = lsb[i] || rsb[i]
			}
		}
		return val{kind: valSeriesBool, sb: out}, nil

	case tokLT, tokLE, tokGT, tokGE, tokEQ, tokNE:
		if (op == tokEQ || op == tokNE) && (lhs.kind == valNull || rhs.kind == valNull) {
			same := lhs.kind == valNull && rhs.kind == valNull
			if op == tokNE {
				same = !same
			}
			return val{kind: valBool, b: same}, nil
		}
		if lhs.kind == valStr && rhs.kind == valStr {
			if op == tokEQ {
				return val{kind: valBool, b: lhs.s == rhs.s}, nil
			}
			if op == tokNE {
				return val{kind: valBool, b: lhs.s != rhs.s}, nil
			}
		}
		if lhs.kind == valBool && rhs.kind == valBool && (op == tokEQ || op == tokNE) {
			res := lhs.b == rhs.b
			if op == tokNE {
				res = !res
			}
			return val{kind: valBool, b: res}, nil
		}
		isSeries := lhs.kind == valSeriesFloat || rhs.kind == valSeriesFloat || lhs.kind == valSeriesBool || rhs.kind == valSeriesBool
		if !isSeries {
			lf, err := asScalarFloat(lhs)
			if err != nil {
				return val{}, err
			}
			rf, err := asScalarFloat(rhs)
			if err != nil {
				return val{}, err
			}
			return val{kind: valBool, b: compareScalar(op, lf, rf)}, nil
		}
		lsf, err := m.toFloatSeries(lhs)
		if err != nil {
			return val{}, err
		}
		rsf, err := m.toFloatSeries(rhs)
		if err != nil {
			return val{}, err
		}
		out := make([]bool, m.barCount)
		for i := 0; i < m.barCount; i++ {
			out[i] = compareScalar(op, lsf[i], rsf[i])
		}
		return val{kind: valSeriesBool, sb: out}, nil

	case tokPlus, tokMinus, tokStar, tokSlash, tokPercent:
		if op == tokPlus && (lhs.kind == valStr || rhs.kind == valStr) {
			ls := fmt.Sprint(scalarPrintable(lhs))
			rs := fmt.Sprint(scalarPrintable(rhs))
			total := len(ls) + len(rs)
			if total > MaxStringBytes {
				return val{}, fmt.Errorf("FasedScript string exceeds %d byte limit", MaxStringBytes)
			}
			m.allocBytes += total
			if m.allocBytes > MaxAllocBytes {
				return val{}, fmt.Errorf("FasedScript memory allocation exceeded %d byte limit", MaxAllocBytes)
			}
			return val{kind: valStr, s: ls + rs}, nil
		}
		isSeries := lhs.kind == valSeriesFloat || rhs.kind == valSeriesFloat || lhs.kind == valSeriesBool || rhs.kind == valSeriesBool
		if !isSeries {
			lf, err := asScalarFloat(lhs)
			if err != nil {
				return val{}, err
			}
			rf, err := asScalarFloat(rhs)
			if err != nil {
				return val{}, err
			}
			return val{kind: valFloat, f: arithScalar(op, lf, rf)}, nil
		}
		lsf, err := m.toFloatSeries(lhs)
		if err != nil {
			return val{}, err
		}
		rsf, err := m.toFloatSeries(rhs)
		if err != nil {
			return val{}, err
		}
		out := make([]float64, m.barCount)
		for i := 0; i < m.barCount; i++ {
			out[i] = arithScalar(op, lsf[i], rsf[i])
		}
		return val{kind: valSeriesFloat, sf: out}, nil
	}
	return val{}, errors.New("unsupported binary operator")
}

func (m *vm) evalVecTernary(cond, tVal, fVal val) (val, error) {
	cb, err := m.toBoolSeries(cond)
	if err != nil {
		return val{}, err
	}
	if tVal.kind == valBool || tVal.kind == valSeriesBool {
		tb, err := m.toBoolSeries(tVal)
		if err != nil {
			return val{}, err
		}
		fb, err := m.toBoolSeries(fVal)
		if err != nil {
			return val{}, err
		}
		out := make([]bool, m.barCount)
		for i := 0; i < m.barCount; i++ {
			if cb[i] {
				out[i] = tb[i]
			} else {
				out[i] = fb[i]
			}
		}
		return val{kind: valSeriesBool, sb: out}, nil
	}
	tf, err := m.toFloatSeries(tVal)
	if err != nil {
		return val{}, err
	}
	ff, err := m.toFloatSeries(fVal)
	if err != nil {
		return val{}, err
	}
	out := make([]float64, m.barCount)
	for i := 0; i < m.barCount; i++ {
		if cb[i] {
			out[i] = tf[i]
		} else {
			out[i] = ff[i]
		}
	}
	return val{kind: valSeriesFloat, sf: out}, nil
}

func scalarPrintable(v val) string {
	switch v.kind {
	case valStr:
		return v.s
	case valFloat:
		return fmt.Sprintf("%g", v.f)
	case valBool:
		return fmt.Sprintf("%t", v.b)
	default:
		return ""
	}
}

func compareScalar(op tokenKind, a, b float64) bool {
	if !isFinite(a) || !isFinite(b) {
		return false
	}
	switch op {
	case tokLT:
		return a < b
	case tokLE:
		return a <= b
	case tokGT:
		return a > b
	case tokGE:
		return a >= b
	case tokEQ:
		return a == b
	case tokNE:
		return a != b
	default:
		return false
	}
}

func arithScalar(op tokenKind, a, b float64) float64 {
	if !isFinite(a) || !isFinite(b) {
		return math.NaN()
	}
	switch op {
	case tokPlus:
		return a + b
	case tokMinus:
		return a - b
	case tokStar:
		return a * b
	case tokSlash:
		if b == 0 {
			return math.NaN()
		}
		return a / b
	case tokPercent:
		if b == 0 {
			return math.NaN()
		}
		return math.Mod(a, b)
	default:
		return math.NaN()
	}
}

var builtinNames = []string{
	"input", "plot", "signal", "strategy",
	"sma", "ema", "rma", "wma", "rsi", "atr", "vwap",
	"stddev", "stdDev", "bbBasis", "bbUpper", "bbLower",
	"macdLine", "macdSignal", "macdHist",
	"highest", "lowest", "roc", "shift", "zscore", "zScore",
	"volumeRatio", "volume_ratio",
	"crossover", "crossOver", "crossunder", "crossUnder",
	"rising", "falling", "nz",
	"abs", "min", "max", "clamp", "sqrt", "log", "pow", "round", "floor", "ceil",
}

func (m *vm) callBuiltin(name string, args []val) (val, error) {
	if err := m.step(maxIntVal(1, m.barCount/64)); err != nil {
		return val{}, err
	}
	switch name {
	case "input":
		if len(args) < 2 || args[0].kind != valStr || strings.TrimSpace(args[0].s) == "" {
			return val{}, errors.New("input(name, default, [min, max, step]) requires name string and numeric default")
		}
		paramName := strings.TrimSpace(args[0].s)
		def, err := asScalarFloat(args[1])
		if err != nil || !isFinite(def) {
			return val{}, errors.New("input default must be finite number")
		}
		spec := InputSpec{Name: paramName, Default: def, Min: def, Max: def, Step: 1}
		if len(args) >= 3 {
			if v, err := asScalarFloat(args[2]); err == nil && isFinite(v) {
				spec.Min = v
			}
		}
		if len(args) >= 4 {
			if v, err := asScalarFloat(args[3]); err == nil && isFinite(v) {
				spec.Max = v
			}
		}
		if len(args) >= 5 {
			if v, err := asScalarFloat(args[4]); err == nil && isFinite(v) && v > 0 {
				spec.Step = v
			}
		}
		if spec.Max < spec.Min {
			spec.Max = spec.Min
		}
		if !m.seenIn[paramName] && len(m.inputs) < MaxInputs {
			m.seenIn[paramName] = true
			m.inputs = append(m.inputs, spec)
		}
		chosen := def
		if override, ok := m.params[paramName]; ok && isFinite(override) {
			chosen = override
			if spec.Max > spec.Min {
				if chosen < spec.Min {
					chosen = spec.Min
				} else if chosen > spec.Max {
					chosen = spec.Max
				}
			}
		}
		m.resolved[paramName] = chosen
		return val{kind: valFloat, f: chosen}, nil

	case "plot":
		if len(args) < 2 || args[0].kind != valStr {
			return val{}, errors.New("plot(name, series, [tone, pane]) requires name and series")
		}
		if len(m.plots) >= MaxPlots {
			return val{kind: valNull}, nil
		}
		sf, err := m.toFloatSeries(args[1])
		if err != nil {
			return val{}, err
		}
		tone := "accent"
		if len(args) >= 3 && args[2].kind == valStr && args[2].s != "" {
			tone = strings.ToLower(strings.TrimSpace(args[2].s))
		}
		pane := "overlay"
		if len(args) >= 4 && args[3].kind == valStr && args[3].s != "" {
			pane = strings.ToLower(strings.TrimSpace(args[3].s))
		}
		vals := make([]NullableFloat, m.barCount)
		for i := 0; i < m.barCount; i++ {
			vals[i] = NullableFloat(sf[i])
		}
		m.plots = append(m.plots, PlotSeries{
			Name:   args[0].s,
			Tone:   tone,
			Pane:   pane,
			Values: vals,
		})
		return val{kind: valNull}, nil

	case "signal":
		if len(args) < 2 || args[0].kind != valStr {
			return val{}, errors.New("signal(kind, cond) requires string kind and boolean condition")
		}
		sb, err := m.toBoolSeries(args[1])
		if err != nil {
			return val{}, err
		}
		if len(m.signals) < MaxPlots {
			m.signals = append(m.signals, SignalMarker{
				Kind: strings.ToLower(strings.TrimSpace(args[0].s)),
				Bars: sb,
			})
		}
		return val{kind: valNull}, nil

	case "strategy":
		if len(args) < 1 || args[0].kind != valObject {
			return val{}, errors.New("strategy({...}) requires configuration object")
		}
		obj := args[0].obj
		if v, ok := obj["long"]; ok && v.kind != valNull {
			b, err := m.toBoolSeries(v)
			if err != nil {
				return val{}, fmt.Errorf("strategy.long: %w", err)
			}
			m.strategy.Long = b
		}
		if v, ok := obj["exitLong"]; ok && v.kind != valNull {
			b, err := m.toBoolSeries(v)
			if err != nil {
				return val{}, fmt.Errorf("strategy.exitLong: %w", err)
			}
			m.strategy.ExitLong = b
		}
		if v, ok := obj["short"]; ok && v.kind != valNull {
			b, err := m.toBoolSeries(v)
			if err != nil {
				return val{}, fmt.Errorf("strategy.short: %w", err)
			}
			m.strategy.Short = b
		}
		if v, ok := obj["exitShort"]; ok && v.kind != valNull {
			b, err := m.toBoolSeries(v)
			if err != nil {
				return val{}, fmt.Errorf("strategy.exitShort: %w", err)
			}
			m.strategy.ExitShort = b
		}
		if v, ok := obj["stopLossPct"]; ok {
			if f, err := asScalarFloat(v); err == nil && isFinite(f) && f >= 0 && f <= 95 {
				m.strategy.StopLossPct = f
			}
		}
		if v, ok := obj["takeProfitPct"]; ok {
			if f, err := asScalarFloat(v); err == nil && isFinite(f) && f >= 0 && f <= 1000 {
				m.strategy.TakeProfitPct = f
			}
		}
		if v, ok := obj["trailingAtrMult"]; ok {
			if f, err := asScalarFloat(v); err == nil && isFinite(f) && f >= 0 && f <= 20 {
				m.strategy.TrailingAtrMult = f
			}
		}
		if v, ok := obj["sizePct"]; ok {
			if f, err := asScalarFloat(v); err == nil && isFinite(f) && f > 0 && f <= 100 {
				m.strategy.SizePct = f
			}
		}
		return val{kind: valNull}, nil

	case "sma", "ema", "rma", "wma", "rsi", "stddev", "stdDev", "bbBasis", "highest", "lowest", "roc", "zscore", "zScore":
		if len(args) < 2 {
			return val{}, fmt.Errorf("%s(src, period) requires 2 arguments", name)
		}
		src, err := m.toFloatSeries(args[0])
		if err != nil {
			return val{}, err
		}
		period, err := asScalarInt(args[1], 1, 500)
		if err != nil {
			return val{}, err
		}
		if err := m.chargeAlloc(m.barCount * 8); err != nil {
			return val{}, err
		}
		switch name {
		case "wma", "highest", "lowest", "stddev", "stdDev", "zscore", "zScore":
			extraCost := (m.barCount * period) / 64
			if extraCost < 1 {
				extraCost = 1
			}
			if err := m.step(extraCost); err != nil {
				return val{}, err
			}
		}
		var res []float64
		switch name {
		case "sma", "bbBasis":
			res = SMA(src, period)
		case "ema":
			res = EMA(src, period)
		case "rma":
			res = RMA(src, period)
		case "wma":
			res = WMA(src, period)
		case "rsi":
			res = RSI(src, period)
		case "stddev", "stdDev":
			res = StdDev(src, period)
		case "highest":
			res = Highest(src, period)
		case "lowest":
			res = Lowest(src, period)
		case "roc":
			res = ROC(src, period)
		case "zscore", "zScore":
			res = ZScore(src, period)
		}
		return val{kind: valSeriesFloat, sf: res}, nil

	case "atr":
		period := 14
		if len(args) >= 1 {
			p, err := asScalarInt(args[0], 1, 500)
			if err != nil {
				return val{}, err
			}
			period = p
		}
		return val{kind: valSeriesFloat, sf: ATR(m.candles, period)}, nil

	case "vwap":
		return val{kind: valSeriesFloat, sf: VWAP(m.candles)}, nil

	case "bbUpper", "bbLower":
		if len(args) < 2 {
			return val{}, fmt.Errorf("%s(src, period, [mult]) requires at least 2 arguments", name)
		}
		src, err := m.toFloatSeries(args[0])
		if err != nil {
			return val{}, err
		}
		period, err := asScalarInt(args[1], 1, 500)
		if err != nil {
			return val{}, err
		}
		mult := 2.0
		if len(args) >= 3 {
			mv, err := asScalarFloat(args[2])
			if err != nil || !isFinite(mv) || mv <= 0 {
				return val{}, errors.New("bollinger multiplier must be positive")
			}
			mult = mv
		}
		if name == "bbUpper" {
			return val{kind: valSeriesFloat, sf: BBUpper(src, period, mult)}, nil
		}
		return val{kind: valSeriesFloat, sf: BBLower(src, period, mult)}, nil

	case "macdLine", "macdSignal", "macdHist":
		if len(args) < 1 {
			return val{}, fmt.Errorf("%s(src, [fast, slow, signal]) requires source series", name)
		}
		src, err := m.toFloatSeries(args[0])
		if err != nil {
			return val{}, err
		}
		fast, slow, sig := 12, 26, 9
		if len(args) >= 2 {
			if fast, err = asScalarInt(args[1], 1, 200); err != nil {
				return val{}, err
			}
		}
		if len(args) >= 3 {
			if slow, err = asScalarInt(args[2], 2, 500); err != nil {
				return val{}, err
			}
		}
		if len(args) >= 4 {
			if sig, err = asScalarInt(args[3], 1, 200); err != nil {
				return val{}, err
			}
		}
		l, s, h := MACD(src, fast, slow, sig)
		switch name {
		case "macdLine":
			return val{kind: valSeriesFloat, sf: l}, nil
		case "macdSignal":
			return val{kind: valSeriesFloat, sf: s}, nil
		default:
			return val{kind: valSeriesFloat, sf: h}, nil
		}

	case "shift":
		if len(args) < 2 {
			return val{}, errors.New("shift(src, lag) requires 2 arguments")
		}
		src, err := m.toFloatSeries(args[0])
		if err != nil {
			return val{}, err
		}
		lagF, err := asScalarFloat(args[1])
		if err != nil || !isFinite(lagF) {
			return val{}, errors.New("shift lag must be finite integer")
		}
		lag := int(math.Round(lagF))
		if lag > 5000 {
			return val{}, errors.New("shift lag exceeds maximum 5000 bars")
		}
		out, err := Shift(src, lag)
		if err != nil {
			return val{}, err
		}
		return val{kind: valSeriesFloat, sf: out}, nil

	case "volumeRatio", "volume_ratio":
		period := 20
		if len(args) >= 1 {
			p, err := asScalarInt(args[0], 1, 500)
			if err != nil {
				return val{}, err
			}
			period = p
		}
		volS := make([]float64, m.barCount)
		for i, c := range m.candles {
			volS[i] = c.Volume
		}
		mean := SMA(volS, period)
		out := nanSlice(m.barCount)
		for i := 0; i < m.barCount; i++ {
			if isFinite(volS[i]) && isFinite(mean[i]) && mean[i] > 0 {
				out[i] = volS[i] / mean[i]
			}
		}
		return val{kind: valSeriesFloat, sf: out}, nil

	case "crossover", "crossOver":
		if len(args) < 2 {
			return val{}, errors.New("crossover(a, b) requires 2 arguments")
		}
		a, err := m.toFloatSeries(args[0])
		if err != nil {
			return val{}, err
		}
		b, err := m.toFloatSeries(args[1])
		if err != nil {
			return val{}, err
		}
		return val{kind: valSeriesBool, sb: Crossover(a, b)}, nil

	case "crossunder", "crossUnder":
		if len(args) < 2 {
			return val{}, errors.New("crossunder(a, b) requires 2 arguments")
		}
		a, err := m.toFloatSeries(args[0])
		if err != nil {
			return val{}, err
		}
		b, err := m.toFloatSeries(args[1])
		if err != nil {
			return val{}, err
		}
		return val{kind: valSeriesBool, sb: Crossunder(a, b)}, nil

	case "rising", "falling":
		if len(args) < 1 {
			return val{}, fmt.Errorf("%s(src, [bars]) requires source series", name)
		}
		src, err := m.toFloatSeries(args[0])
		if err != nil {
			return val{}, err
		}
		lookback := 1
		if len(args) >= 2 {
			if lookback, err = asScalarInt(args[1], 1, 50); err != nil {
				return val{}, err
			}
		}
		if name == "rising" {
			return val{kind: valSeriesBool, sb: Rising(src, lookback)}, nil
		}
		return val{kind: valSeriesBool, sb: Falling(src, lookback)}, nil

	case "nz":
		if len(args) < 1 {
			return val{}, errors.New("nz(src, [fallback]) requires source")
		}
		fallback := 0.0
		if len(args) >= 2 {
			fb, err := asScalarFloat(args[1])
			if err != nil || !isFinite(fb) {
				return val{}, errors.New("nz fallback must be finite")
			}
			fallback = fb
		}
		if args[0].kind == valFloat {
			if isFinite(args[0].f) {
				return args[0], nil
			}
			return val{kind: valFloat, f: fallback}, nil
		}
		src, err := m.toFloatSeries(args[0])
		if err != nil {
			return val{}, err
		}
		return val{kind: valSeriesFloat, sf: NZ(src, fallback)}, nil

	case "abs", "sqrt", "log", "round", "floor", "ceil":
		if len(args) < 1 {
			return val{}, fmt.Errorf("%s(x) requires 1 argument", name)
		}
		fn := mapScalarMath(name)
		if args[0].kind != valSeriesFloat {
			f, err := asScalarFloat(args[0])
			if err != nil {
				return val{}, err
			}
			return val{kind: valFloat, f: fn(f)}, nil
		}
		out := make([]float64, len(args[0].sf))
		for i, v := range args[0].sf {
			out[i] = fn(v)
		}
		return val{kind: valSeriesFloat, sf: out}, nil

	case "min", "max", "pow":
		if len(args) < 2 {
			return val{}, fmt.Errorf("%s(a, b) requires 2 arguments", name)
		}
		if args[0].kind != valSeriesFloat && args[1].kind != valSeriesFloat {
			a, err := asScalarFloat(args[0])
			if err != nil {
				return val{}, err
			}
			b, err := asScalarFloat(args[1])
			if err != nil {
				return val{}, err
			}
			return val{kind: valFloat, f: pairScalarMath(name, a, b)}, nil
		}
		as, err := m.toFloatSeries(args[0])
		if err != nil {
			return val{}, err
		}
		bs, err := m.toFloatSeries(args[1])
		if err != nil {
			return val{}, err
		}
		out := make([]float64, m.barCount)
		for i := 0; i < m.barCount; i++ {
			out[i] = pairScalarMath(name, as[i], bs[i])
		}
		return val{kind: valSeriesFloat, sf: out}, nil

	case "clamp":
		if len(args) < 3 {
			return val{}, errors.New("clamp(x, lo, hi) requires 3 arguments")
		}
		lo, err := asScalarFloat(args[1])
		if err != nil {
			return val{}, err
		}
		hi, err := asScalarFloat(args[2])
		if err != nil {
			return val{}, err
		}
		if args[0].kind != valSeriesFloat {
			x, err := asScalarFloat(args[0])
			if err != nil {
				return val{}, err
			}
			return val{kind: valFloat, f: math.Min(hi, math.Max(lo, x))}, nil
		}
		out := make([]float64, len(args[0].sf))
		for i, x := range args[0].sf {
			if !isFinite(x) {
				out[i] = math.NaN()
			} else {
				out[i] = math.Min(hi, math.Max(lo, x))
			}
		}
		return val{kind: valSeriesFloat, sf: out}, nil
	}

	return val{}, fmt.Errorf("unknown builtin %q", name)
}

func mapScalarMath(name string) func(float64) float64 {
	return func(x float64) float64 {
		if !isFinite(x) {
			return math.NaN()
		}
		switch name {
		case "abs":
			return math.Abs(x)
		case "sqrt":
			if x < 0 {
				return math.NaN()
			}
			return math.Sqrt(x)
		case "log":
			if x <= 0 {
				return math.NaN()
			}
			return math.Log(x)
		case "round":
			return math.Round(x)
		case "floor":
			return math.Floor(x)
		case "ceil":
			return math.Ceil(x)
		default:
			return x
		}
	}
}

func pairScalarMath(name string, a, b float64) float64 {
	if !isFinite(a) || !isFinite(b) {
		return math.NaN()
	}
	switch name {
	case "min":
		return math.Min(a, b)
	case "max":
		return math.Max(a, b)
	case "pow":
		return math.Pow(a, b)
	default:
		return math.NaN()
	}
}

func maxIntVal(a, b int) int {
	if a > b {
		return a
	}
	return b
}
