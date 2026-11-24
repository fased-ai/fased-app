package decision

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	TypeNoul   = "noul"
	TypeChoice = "choice"
	TypeScore  = "score"

	DefaultEndpoint = "https://api.typesafe.ai/v1/systemone"
	DefaultModel    = "jev-latest"

	maxStateBytes    = 32768
	maxQuestionCount = 16
	maxChoicesCount  = 255
	maxRubricLevels  = 10
)

var safeSlugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// QuestionSpec defines a typed System-1 classification question.
type QuestionSpec struct {
	Type         string   `json:"type"`
	Instructions string   `json:"instructions,omitempty"`
	Choices      []string `json:"choices,omitempty"`
	Rubric       []string `json:"rubric,omitempty"`
}

// EvaluateRequest inputs a dense serialized state plus 1..16 typed questions.
type EvaluateRequest struct {
	State     string                  `json:"state"`
	Model     string                  `json:"model,omitempty"`
	Questions map[string]QuestionSpec `json:"questions"`
}

// Answer captures a typed System-1 evaluation output with confidence/probabilities.
type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         *int               `json:"score,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        string             `json:"legend,omitempty"`
	Confidence    float64            `json:"confidence"`
}

// Usage mirrors token usage accounting from TypeSafe Jev.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// EvaluateResponse returns typed System-1 decisions and provenance metadata.
type EvaluateResponse struct {
	Provider  string            `json:"provider"`
	Model     string            `json:"model"`
	Answers   map[string]Answer `json:"answers"`
	Usage     Usage             `json:"usage"`
	LatencyMs int64             `json:"latencyMs"`
	Fallback  bool              `json:"fallback"`
}

// Status exposes non-secret configuration state for UI & diagnostics.
type Status struct {
	Configured   bool   `json:"configured"`
	Model        string `json:"model"`
	Endpoint     string `json:"endpoint"`
	FallbackMode string `json:"fallbackMode"`
}

// ConfigUpdate mutates encrypted Jev settings without returning secrets.
type ConfigUpdate struct {
	APIKey   string `json:"apiKey,omitempty"`
	ClearKey bool   `json:"clearKey,omitempty"`
	Model    string `json:"model,omitempty"`
}

// ValidateModel normalizes a Jev model identifier.
func ValidateModel(raw string) (string, error) {
	m := strings.TrimSpace(raw)
	if m == "" {
		return DefaultModel, nil
	}
	if !safeSlugPattern.MatchString(m) {
		return "", fmt.Errorf("invalid Jev model identifier %q", raw)
	}
	return m, nil
}

// ValidateRequest enforces strict Jev API bounds on state size, model slug,
// and question specifications.
func ValidateRequest(req *EvaluateRequest) error {
	if req == nil {
		return errors.New("evaluate request required")
	}
	req.State = strings.TrimSpace(req.State)
	if req.State == "" || len(req.State) > maxStateBytes {
		return fmt.Errorf("state must be 1-%d bytes", maxStateBytes)
	}
	model, err := ValidateModel(req.Model)
	if err != nil {
		return err
	}
	req.Model = model

	if len(req.Questions) == 0 || len(req.Questions) > maxQuestionCount {
		return fmt.Errorf("questions must contain 1-%d entries", maxQuestionCount)
	}

	cleaned := make(map[string]QuestionSpec, len(req.Questions))
	for k, spec := range req.Questions {
		key := strings.TrimSpace(k)
		if !safeSlugPattern.MatchString(key) {
			return fmt.Errorf("invalid question key %q", k)
		}
		spec.Type = strings.ToLower(strings.TrimSpace(spec.Type))
		spec.Instructions = strings.TrimSpace(spec.Instructions)
		if len(spec.Instructions) > 2048 {
			return fmt.Errorf("question %q instructions exceed 2048 characters", key)
		}

		switch spec.Type {
		case TypeNoul:
			if spec.Instructions == "" {
				return fmt.Errorf("noul question %q requires instructions", key)
			}
		case TypeChoice:
			if len(spec.Choices) < 2 || len(spec.Choices) > maxChoicesCount {
				return fmt.Errorf("choice question %q requires 2-%d choices", key, maxChoicesCount)
			}
			normChoices := make([]string, 0, len(spec.Choices))
			seen := make(map[string]bool, len(spec.Choices))
			for _, c := range spec.Choices {
				trimmed := strings.TrimSpace(c)
				if trimmed == "" || len(trimmed) > 128 {
					return fmt.Errorf("choice label in %q must be 1-128 characters", key)
				}
				if seen[trimmed] {
					return fmt.Errorf("duplicate choice %q in %q", trimmed, key)
				}
				seen[trimmed] = true
				normChoices = append(normChoices, trimmed)
			}
			spec.Choices = normChoices
		case TypeScore:
			if len(spec.Rubric) < 2 || len(spec.Rubric) > maxRubricLevels {
				return fmt.Errorf("score question %q requires 2-%d rubric levels", key, maxRubricLevels)
			}
			normRubric := make([]string, 0, len(spec.Rubric))
			for _, r := range spec.Rubric {
				trimmed := strings.TrimSpace(r)
				if trimmed == "" || len(trimmed) > 128 {
					return fmt.Errorf("rubric level in %q must be 1-128 characters", key)
				}
				normRubric = append(normRubric, trimmed)
			}
			spec.Rubric = normRubric
		default:
			return fmt.Errorf("unsupported question type %q for %q", spec.Type, key)
		}
		cleaned[key] = spec
	}
	req.Questions = cleaned
	return nil
}
