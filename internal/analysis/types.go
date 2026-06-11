package analysis

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nlink-jp/ir-hub/internal/knowledge"
)

// Text is a string that tolerates LLM type drift: a JSON array of
// strings decodes to its newline join, numbers/bools to their
// string form (ai-ir2 needed the same Pydantic coercions).
type Text string

func (t *Text) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*t = Text(s)
		return nil
	}
	var list []any
	if err := json.Unmarshal(b, &list); err == nil {
		parts := make([]string, 0, len(list))
		for _, v := range list {
			parts = append(parts, anyToString(v))
		}
		*t = Text(strings.Join(parts, "\n"))
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*t = Text(anyToString(v))
	return nil
}

// List is a []string that tolerates a bare string (becomes a
// single-element list) and non-string elements (stringified).
type List []string

func (l *List) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if s == "" {
			*l = List{}
		} else {
			*l = List{s}
		}
		return nil
	}
	var list []any
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	out := make(List, 0, len(list))
	for _, v := range list {
		out = append(out, anyToString(v))
	}
	*l = out
	return nil
}

func anyToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

// ---- stage output shapes (described verbatim in the prompts) ----

// TimelineEvent tolerates both {"time","event"} objects and bare
// strings (the whole line lands in Event).
type TimelineEvent struct {
	Time  string `json:"time"`
	Event string `json:"event"`
}

func (e *TimelineEvent) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		e.Event = s
		return nil
	}
	type alias TimelineEvent
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*e = TimelineEvent(a)
	return nil
}

// Summary is the incident summary stage output.
type Summary struct {
	Title           string          `json:"title"`
	Severity        string          `json:"severity"` // critical|high|medium|low|unknown
	AffectedSystems List            `json:"affected_systems"`
	Timeline        []TimelineEvent `json:"timeline"`
	RootCause       Text            `json:"root_cause"`
	Resolution      Text            `json:"resolution"`
	Summary         Text            `json:"summary"`
}

// Action is one participant action.
type Action struct {
	Timestamp Text `json:"timestamp"`
	Purpose   Text `json:"purpose"`
	Method    Text `json:"method"`
	Findings  Text `json:"findings"`
}

// ParticipantActivity groups a participant's actions.
type ParticipantActivity struct {
	UserName string   `json:"user_name"`
	Actions  []Action `json:"actions"`
}

// ActivityAnalysis is the activity stage output.
type ActivityAnalysis struct {
	Participants []ParticipantActivity `json:"participants"`
}

// UnmarshalJSON tolerates a bare array of participants instead of
// the {"participants": [...]} wrapper (same Gemini drift as tactics).
func (a *ActivityAnalysis) UnmarshalJSON(b []byte) error {
	if trimmed := strings.TrimLeft(string(b), " \t\r\n"); strings.HasPrefix(trimmed, "[") {
		return json.Unmarshal(b, &a.Participants)
	}
	type alias ActivityAnalysis
	var x alias
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	a.Participants = x.Participants
	return nil
}

// ParticipantRole is one inferred role.
type ParticipantRole struct {
	UserName     string `json:"user_name"`
	InferredRole string `json:"inferred_role"`
	Confidence   string `json:"confidence"` // high|medium|low
	Evidence     List   `json:"evidence"`
}

// Relationship is one inferred relationship.
type Relationship struct {
	From Text   `json:"from"`
	To   Text   `json:"to"`
	Type string `json:"type"` // reports_to|coordinates_with|escalated_to|informed
}

// RoleAnalysis is the roles stage output.
type RoleAnalysis struct {
	Roles         []ParticipantRole `json:"roles"`
	Relationships []Relationship    `json:"relationships"`
}

// PhaseAssessment is one IR phase evaluation.
type PhaseAssessment struct {
	Name       string `json:"name"`
	Duration   Text   `json:"duration"`
	Assessment Text   `json:"assessment"`
}

// Review is the process-quality stage output.
type Review struct {
	OverallScore        int               `json:"overall_score"` // 1-10
	Phases              []PhaseAssessment `json:"phases"`
	Communication       Text              `json:"communication"`
	RoleClarity         Text              `json:"role_clarity"`
	ToolAppropriateness Text              `json:"tool_appropriateness"`
	Strengths           List              `json:"strengths"`
	Improvements        List              `json:"improvements"`
	Checklist           List              `json:"checklist"`
}

// rawTactic is the LLM-facing tactic shape; normalized into
// knowledge.Tactic by the tactics stage.
type rawTactic struct {
	Title        string `json:"title"`
	Purpose      Text   `json:"purpose"`
	Category     string `json:"category"`
	Tools        List   `json:"tools"`
	Procedure    Text   `json:"procedure"`
	Observations Text   `json:"observations"`
	Tags         List   `json:"tags"`
	Confidence   string `json:"confidence"`
	Evidence     Text   `json:"evidence"`
}

type tacticsResponse struct {
	Tactics []rawTactic `json:"tactics"`
}

// UnmarshalJSON tolerates the model returning a bare array of
// tactics instead of the {"tactics": [...]} wrapper (Gemini drops
// the wrapper intermittently).
func (t *tacticsResponse) UnmarshalJSON(b []byte) error {
	if trimmed := strings.TrimLeft(string(b), " \t\r\n"); strings.HasPrefix(trimmed, "[") {
		return json.Unmarshal(b, &t.Tactics)
	}
	type alias tacticsResponse
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	t.Tactics = a.Tactics
	return nil
}

// Report is the full postmortem result. English-canonical; the
// translated copy used for channel posts is produced by Translate.
type Report struct {
	CaseID           int64              `json:"case_id"`
	Channel          string             `json:"channel"`
	Summary          Summary            `json:"summary"`
	Activity         ActivityAnalysis   `json:"activity"`
	Roles            RoleAnalysis       `json:"roles"`
	Tactics          []knowledge.Tactic `json:"tactics"`
	Review           Review             `json:"review"`
	Participants     []string           `json:"participants"`
	TotalMessages    int                `json:"total_messages"`
	AnalyzedMessages int                `json:"analyzed_messages"`
	Truncated        bool               `json:"truncated"`
	GeneratedAt      string             `json:"generated_at"`
}
