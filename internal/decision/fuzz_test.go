package decision

import (
	"math"
	"testing"
	"time"
)

func FuzzJevEvaluateParser(f *testing.F) {
	f.Add([]byte(`{"model":"jev-latest","answers":{"q":{"type":"noul","noul":0.75}}}`))
	f.Add([]byte(`{"model":"jev-latest","answers":{"q":{"type":"choice","choice":"up","probabilities":{"up":0.7,"down":0.3},"confidence":0.7}}}`))
	f.Add([]byte(`not json`))

	f.Fuzz(func(t *testing.T, data []byte) {
		res, err := ParseJevResponse(data)
		if err == nil {
			for _, a := range res.Answers {
				if math.IsNaN(a.Confidence) || math.IsInf(a.Confidence, 0) {
					t.Fatalf("non-finite confidence: %+v", a)
				}
				if a.Noul != nil && (math.IsNaN(*a.Noul) || math.IsInf(*a.Noul, 0)) {
					t.Fatalf("non-finite noul: %+v", a)
				}
			}
		}

		if len(data) > 0 && len(data) < 2048 {
			_, _ = EvaluateFallback(EvaluateRequest{
				State: string(data),
				Questions: map[string]QuestionSpec{
					"q1": {Type: TypeNoul, Instructions: "Urgent market shift"},
					"q2": {Type: TypeChoice, Choices: []string{"bullish", "bearish"}},
					"q3": {Type: TypeScore, Rubric: []string{"low", "mid", "high"}},
				},
			}, time.Now())
		}
	})
}
