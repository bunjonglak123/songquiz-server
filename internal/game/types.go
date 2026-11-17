// Package game holds the song quiz rules: shared types, scoring and the engine.
// JSON field names match src/lib/types.ts in the web app.
package game

import "encoding/json"

type Track struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	RawTitle   string `json:"rawTitle"`
	Artist     string `json:"artist"`
	Album      string `json:"album,omitempty"`
	Artwork    string `json:"artwork,omitempty"`
	PreviewURL string `json:"previewUrl"`
	StoreURL   string `json:"storeUrl,omitempty"`
}

// Choice is one answer option. Artist stays empty until the reveal.
type Choice struct {
	Title  string `json:"title"`
	Artist string `json:"artist,omitempty"`
}

// HostQuestion is a question as the server keeps it, answer included.
type HostQuestion struct {
	ID      string   `json:"id"`
	Track   Track    `json:"track"`
	Choices []Choice `json:"choices"`
	Answer  int      `json:"answer"`
	StartAt float64  `json:"startAt"`
}

type GameSettings struct {
	Source        json.RawMessage `json:"source"`
	Rounds        int             `json:"rounds"`
	RoundSeconds  int             `json:"roundSeconds"`
	ListenSeconds int             `json:"listenSeconds"`
}

type PlayerState struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color,omitempty"`
	Score    int    `json:"score"`
	Streak   int    `json:"streak"`
	Online   bool   `json:"online"`
	LastGain *int   `json:"lastGain,omitempty"`
	PrevRank *int   `json:"prevRank,omitempty"`
}

type PublicQuestion struct {
	ID         string   `json:"id"`
	Index      int      `json:"index"`
	PreviewURL string   `json:"previewUrl"`
	StartAt    float64  `json:"startAt"`
	Choices    []Choice `json:"choices"`
	LockMs     int      `json:"lockMs"`
	DurationMs int      `json:"durationMs"`
}

type Pick struct {
	Choice int `json:"choice"`
	Gain   int `json:"gain"`
	Ms     int `json:"ms"`
}

type Reveal struct {
	QuestionID string          `json:"questionId"`
	Answer     int             `json:"answer"`
	Track      Track           `json:"track"`
	Choices    []Choice        `json:"choices,omitempty"`
	Picks      map[string]Pick `json:"picks"`
}

type Phase string

const (
	PhaseLobby     Phase = "lobby"
	PhaseLoading   Phase = "loading"
	PhaseQuestion  Phase = "question"
	PhaseReveal    Phase = "reveal"
	PhaseStandings Phase = "standings"
	PhaseFinal     Phase = "final"
)

// GameState is what every player receives. It never holds a future answer.
type GameState struct {
	Version     int                    `json:"version"`
	HostID      string                 `json:"hostId"`
	Phase       Phase                  `json:"phase"`
	Settings    GameSettings           `json:"settings"`
	Players     map[string]PlayerState `json:"players"`
	Round       int                    `json:"round"`
	TotalRounds int                    `json:"totalRounds"`
	Question    *PublicQuestion        `json:"question,omitempty"`
	Answered    []string               `json:"answered"`
	LivePicks   map[string]int         `json:"livePicks,omitempty"`
	Reveal      *Reveal                `json:"reveal,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Notice      string                 `json:"notice,omitempty"`
}

// MarshalJSON keeps "answered" an array even when nil.
func (s GameState) MarshalJSON() ([]byte, error) {
	type plain GameState
	if s.Answered == nil {
		s.Answered = []string{}
	}
	return json.Marshal(plain(s))
}

type Member struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type AnswerMsg struct {
	PlayerID   string
	QuestionID string
	Choice     int
	Ms         int
}

var DefaultSettings = GameSettings{
	Source:        json.RawMessage(`{"kind":"chart","size":100}`),
	Rounds:        10,
	RoundSeconds:  15,
	ListenSeconds: 3,
}

// SourceKind reads source.kind, e.g. "chart" or "spotify".
func (s GameSettings) SourceKind() string {
	var v struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(s.Source, &v)
	return v.Kind
}

// Clamp applies the same limits as the web app's /api/questions.
func (s GameSettings) Clamp() GameSettings {
	s.Rounds = clamp(s.Rounds, 3, 30)
	s.RoundSeconds = clamp(s.RoundSeconds, 5, 25)
	s.ListenSeconds = clamp(s.ListenSeconds, 0, 5)
	if len(s.Source) == 0 || !json.Valid(s.Source) {
		s.Source = DefaultSettings.Source
	}
	return s
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
