// Package wenstrategy validates inert WEN strategy documents. It cannot sign,
// settle, schedule tasks, resolve markets, or grant execution authority.
package wenstrategy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"
)

const MaxDocumentBytes = 64 * 1024
const MinimumEntryLamports uint64 = 10_000_000

type Document struct {
	Schema      string     `json:"schema"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Market      Market     `json:"market"`
	Research    Research   `json:"research"`
	Allocation  Allocation `json:"allocation"`
	Schedule    Schedule   `json:"schedule"`
	Limits      Limits     `json:"limits"`
	Execution   Execution  `json:"execution"`
}
type Market struct {
	NetworkGenesisHash string `json:"networkGenesisHash"`
	ProgramID          string `json:"programId"`
	EconomyID          string `json:"economyId"`
	AssetMint          string `json:"assetMint"`
	TermsHash          string `json:"termsHash"`
	InterfaceVersion   string `json:"interfaceVersion"`
}
type Research struct {
	Thesis         string      `json:"thesis"`
	HorizonMinutes int         `json:"horizonMinutes"`
	Conditions     []Condition `json:"conditions"`
	Outlook        *Outlook    `json:"outlook"`
}
type Outlook struct {
	ProbabilitiesBps []int  `json:"probabilitiesBps"`
	Source           string `json:"source"`
	MethodVersion    string `json:"methodVersion"`
	AsOf             string `json:"asOf"`
}
type Condition struct {
	Metric          string `json:"metric"`
	LookbackMinutes int    `json:"lookbackMinutes"`
	Operator        string `json:"operator"`
	ValueBps        string `json:"valueBps"`
}
type Allocation struct {
	Preset     string `json:"preset"`
	WeightsBps []int  `json:"weightsBps"`
	Mode       string `json:"mode"`
	Rules      []Rule `json:"rules"`
}
type Rule struct {
	When       []Condition `json:"when"`
	Preset     string      `json:"preset"`
	WeightsBps []int       `json:"weightsBps"`
}
type Schedule struct {
	StartsAt           string           `json:"startsAt"`
	ExpiresAt          string           `json:"expiresAt"`
	EveryNthRound      int              `json:"everyNthRound"`
	AnchorRound        string           `json:"anchorRound"`
	UTCWindows         []Window         `json:"utcWindows"`
	SelectedRoundIDs   []string         `json:"selectedRoundIds"`
	SkipRoundIDs       []string         `json:"skipRoundIds"`
	MaxRoundsTotal     int              `json:"maxRoundsTotal"`
	MaxRoundsPerUTCDay int              `json:"maxRoundsPerUtcDay"`
	CooldownRounds     int              `json:"cooldownRounds"`
	RandomSelection    *RandomSelection `json:"randomSelection"`
}
type Window struct {
	StartMinute int `json:"startMinute"`
	EndMinute   int `json:"endMinute"`
}
type RandomSelection struct {
	SeedHex string `json:"seedHex"`
	Count   int    `json:"count"`
}
type Limits struct {
	PerRoundLamports            string `json:"perRoundLamports"`
	TotalTurnoverLamports       string `json:"totalTurnoverLamports"`
	DailyTurnoverLamports       string `json:"dailyTurnoverLamports"`
	MaxUnsettledLamports        string `json:"maxUnsettledLamports"`
	MaxUserFeesLamports         string `json:"maxUserFeesLamports"`
	MaxUserFeesPerRoundLamports string `json:"maxUserFeesPerRoundLamports"`
	MaxLossLamports             string `json:"maxLossLamports"`
}
type Execution struct {
	Mode      string  `json:"mode"`
	Runner    string  `json:"runner"`
	MandateID *string `json:"mandateId"`
}
type Accepted struct {
	Document Document
	Original []byte
	Digest   string
}

var decimal = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
var signed = regexp.MustCompile(`^(0|-?[1-9][0-9]*)$`)
var utc = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
var seed = regexp.MustCompile(`^[0-9a-f]{64}$`)

func amount(s string) (uint64, error) {
	if !decimal.MatchString(s) {
		return 0, fmt.Errorf("noncanonical unsigned amount")
	}
	return strconv.ParseUint(s, 10, 64)
}
func integer(s string) (*big.Int, error) {
	if !signed.MatchString(s) {
		return nil, fmt.Errorf("noncanonical signed metric")
	}
	n, _ := new(big.Int).SetString(s, 10)
	min := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 127))
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
	if n.Cmp(min) < 0 || n.Cmp(max) > 0 {
		return nil, fmt.Errorf("metric exceeds i128")
	}
	return n, nil
}
func timestamp(s string) (time.Time, error) {
	if !utc.MatchString(s) {
		return time.Time{}, fmt.Errorf("timestamp must be whole-second UTC")
	}
	return time.Parse(time.RFC3339, s)
}
func member(s string, options ...string) bool {
	for _, v := range options {
		if s == v {
			return true
		}
	}
	return false
}
func horizon(n int) bool { return n == 30 || n == 60 || n == 240 || n == 720 || n == 1440 }

// Decode retains precisely the accepted bytes. Presence/null checks precede typed
// decoding because zero values cannot distinguish omitted fields from explicit 0.
func Decode(raw []byte) (*Accepted, error) {
	if len(raw) == 0 || len(raw) > MaxDocumentBytes || !utf8.Valid(raw) || !validUnicodeEscapes(raw) {
		return nil, fmt.Errorf("invalid document size or UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tree, err := readValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	if err = checkShape(tree, reflect.TypeOf(Document{}), "strategy"); err != nil {
		return nil, err
	}
	var doc Document
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&doc); err != nil {
		return nil, err
	}
	if err = doc.Validate(); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	return &Accepted{doc, append([]byte(nil), raw...), hex.EncodeToString(digest[:])}, nil
}
func Parse(raw []byte) (*Document, error) {
	a, e := Decode(raw)
	if e != nil {
		return nil, e
	}
	return &a.Document, nil
}
func readValue(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("JSON nesting limit")
	}
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				s, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("object key")
				}
				if _, ok = m[s]; ok {
					return nil, fmt.Errorf("duplicate key %s", s)
				}
				v, e := readValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				m[s] = v
			}
			_, e = d.Token()
			return m, e
		case '[':
			a := []any{}
			for d.More() {
				v, e := readValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			_, e = d.Token()
			return a, e
		default:
			return nil, fmt.Errorf("unexpected delimiter")
		}
	}
	if n, ok := t.(json.Number); ok {
		text := n.String()
		if text == "-0" || !signed.MatchString(text) {
			return nil, fmt.Errorf("non-integer JSON counter")
		}
	}
	return t, nil
}

// encoding/json replaces lone surrogate escapes; reject them before decoding so
// hashes and code-point bounds have the same interpretation in every client.
func validUnicodeEscapes(raw []byte) bool {
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
func checkShape(v any, t reflect.Type, path string) error {
	if t.Kind() == reflect.Pointer {
		if v == nil {
			return nil
		}
		return checkShape(v, t.Elem(), path)
	}
	if v == nil {
		return fmt.Errorf("null %s", path)
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("object %s required", path)
		}
		if len(m) != t.NumField() {
			return fmt.Errorf("missing/unknown fields at %s", path)
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := f.Tag.Get("json")
			x, ok := m[name]
			if !ok {
				return fmt.Errorf("missing %s.%s", path, name)
			}
			if e := checkShape(x, f.Type, path+"."+name); e != nil {
				return e
			}
		}
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return fmt.Errorf("array %s required", path)
		}
		for _, x := range a {
			if e := checkShape(x, t.Elem(), path); e != nil {
				return e
			}
		}
	}
	return nil
}
func (d Document) Validate() error {
	if d.Schema != "wen-strategy/1" || utf8.RuneCountInString(d.Name) < 1 || utf8.RuneCountInString(d.Name) > 80 || utf8.RuneCountInString(d.Description) > 2000 {
		return fmt.Errorf("schema/name/description")
	}
	for _, s := range []string{d.Market.NetworkGenesisHash, d.Market.ProgramID, d.Market.EconomyID, d.Market.AssetMint, d.Market.TermsHash, d.Market.InterfaceVersion} {
		if s == "" || utf8.RuneCountInString(s) > 256 {
			return fmt.Errorf("unbound market")
		}
	}
	if !member(d.Research.Thesis, "trend", "consolidation", "reversal", "volatility", "custom") || !horizon(d.Research.HorizonMinutes) {
		return fmt.Errorf("research thesis/horizon")
	}
	count := len(d.Research.Conditions)
	if e := conditionsValid(d.Research.Conditions); e != nil {
		return e
	}
	if o := d.Research.Outlook; o != nil {
		if e := weightsValid(o.ProbabilitiesBps); e != nil {
			return e
		}
		if !member(o.Source, "user", "model") || (o.MethodVersion == "" || utf8.RuneCountInString(o.MethodVersion) > 256) {
			return fmt.Errorf("outlook source/method")
		}
		if _, e := timestamp(o.AsOf); e != nil {
			return e
		}
	}
	if !member(d.Allocation.Mode, "fixed", "conditional") || len(d.Allocation.Rules) > 8 || d.Allocation.Mode == "fixed" && len(d.Allocation.Rules) > 0 {
		return fmt.Errorf("allocation mode/rules")
	}
	if e := presetValid(d.Allocation.Preset, d.Allocation.WeightsBps); e != nil {
		return e
	}
	if e := d.Limits.Validate(); e != nil {
		return e
	}
	if _, e := CompileAllocation(d.Limits.PerRoundLamports, d.Allocation.WeightsBps); e != nil {
		return e
	}
	for _, r := range d.Allocation.Rules {
		count += len(r.When)
		if len(r.When) == 0 {
			return fmt.Errorf("empty conditional branch")
		}
		if e := conditionsValid(r.When); e != nil {
			return e
		}
		if e := presetValid(r.Preset, r.WeightsBps); e != nil {
			return e
		}
		if _, e := CompileAllocation(d.Limits.PerRoundLamports, r.WeightsBps); e != nil {
			return e
		}
	}
	if count > 32 {
		return fmt.Errorf("too many conditions")
	}
	if e := d.Schedule.Validate(); e != nil {
		return e
	}
	if !member(d.Execution.Mode, "notify", "review", "bounded-auto") || !member(d.Execution.Runner, "browser", "fased") || d.Execution.MandateID != nil && (*d.Execution.MandateID == "" || utf8.RuneCountInString(*d.Execution.MandateID) > 256) {
		return fmt.Errorf("execution fields")
	}
	if d.Execution.Mode == "bounded-auto" && d.Execution.Runner != "fased" {
		return fmt.Errorf("browser cannot run unattended")
	}
	return nil
}
func conditionsValid(cs []Condition) error {
	for _, c := range cs {
		v, e := integer(c.ValueBps)
		if e != nil {
			return e
		}
		if !member(c.Metric, "returnBps", "realizedVolBps") || !horizon(c.LookbackMinutes) || !member(c.Operator, "gte", "lte") || c.Metric == "realizedVolBps" && v.Sign() < 0 {
			return fmt.Errorf("condition")
		}
	}
	return nil
}
func (l Limits) Validate() error {
	vals := []string{l.PerRoundLamports, l.TotalTurnoverLamports, l.DailyTurnoverLamports, l.MaxUnsettledLamports, l.MaxUserFeesLamports, l.MaxUserFeesPerRoundLamports, l.MaxLossLamports}
	ns := make([]uint64, len(vals))
	for i, s := range vals {
		n, e := amount(s)
		if e != nil {
			return e
		}
		if n == 0 && i != 4 && i != 5 {
			return fmt.Errorf("zero principal/loss limit")
		}
		ns[i] = n
	}
	if ns[0] > ns[2] || ns[2] > ns[1] || ns[0] > ns[3] || ns[5] > ns[4] {
		return fmt.Errorf("inconsistent limits")
	}
	return nil
}
