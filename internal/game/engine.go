package game

import (
	"errors"
	"strings"
	"time"

	"github.com/bunjonglak123/songquiz-server/internal/clock"
)

const (
	RevealMs     = 5000
	StandingsMs  = 4500
	GraceMs      = 2500 // network + audio start slack after the countdown
	EarlyMs      = 600  // beat after the last answer so the tap registers visually
	MaxPlayers   = 30
	MaxQuestions = 30
	// LoadingTimeoutMs: a host who closes the tab mid-fetch must not leave the room stuck.
	LoadingTimeoutMs = 60_000
	// MaxSourceBytes caps settings.source, which is resent in every state frame.
	MaxSourceBytes = 32 << 10
)

const MsgLoadingInterrupted = "การโหลดเพลงถูกขัดจังหวะ กดเริ่มเกมใหม่อีกครั้ง"

type Options struct {
	HostID, HostName, HostColor string
	Clock                       clock.Clock
	OnChange                    func(GameState)
	OnFinal                     func(FinalResult)
}

// FinalResult is handed to OnFinal once per game, for saving.
type FinalResult struct {
	State     GameState
	Correct   map[string]int
	StartedAt time.Time
	EndedAt   time.Time
}

type rawPick struct{ choice, ms int }

// Engine is the authoritative game loop. Not safe for concurrent use: the
// room goroutine owns it, and its clock posts timer callbacks into that goroutine.
type Engine struct {
	o         Options
	st        GameState
	questions []HostQuestion
	picks     map[string]map[string]rawPick
	correct   map[string]int
	timer     clock.Timer
	startedAt time.Time
}

func NewEngine(o Options) *Engine {
	return &Engine{
		o: o,
		st: GameState{
			Version:  1,
			HostID:   o.HostID,
			Phase:    PhaseLobby,
			Settings: DefaultSettings,
			Players:  map[string]PlayerState{o.HostID: newPlayer(o.HostID, o.HostName, o.HostColor)},
			Answered: []string{},
		},
		picks:   map[string]map[string]rawPick{},
		correct: map[string]int{},
	}
}

func (e *Engine) State() GameState { return e.st }

func (e *Engine) commit(mut func(s *GameState)) {
	mut(&e.st)
	e.st.Version++
	if e.o.OnChange != nil {
		e.o.OnChange(e.st)
	}
}

func (e *Engine) arm(ms int, fn func()) {
	e.disarm()
	e.timer = e.o.Clock.AfterFunc(time.Duration(ms)*time.Millisecond, fn)
}

func (e *Engine) disarm() {
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
}

func (e *Engine) Dispose() { e.disarm() }

// SyncMembers takes the live member list: adds newcomers, marks leavers offline.
func (e *Engine) SyncMembers(members []Member) {
	players := copyPlayers(e.st.Players)
	online := map[string]bool{}
	changed := false
	for _, m := range members {
		online[m.ID] = true
		p, ok := players[m.ID]
		name := CleanName(m.Name)
		switch {
		case !ok:
			if len(players) >= MaxPlayers {
				continue
			}
			players[m.ID] = newPlayer(m.ID, m.Name, m.Color)
			changed = true
		case !p.Online || p.Name != name || (SafeColor(m.Color) != "" && p.Color != SafeColor(m.Color)):
			p.Online = true
			p.Name = name
			if c := SafeColor(m.Color); c != "" {
				p.Color = c
			}
			players[m.ID] = p
			changed = true
		}
	}
	for id, p := range players {
		if !online[id] && p.Online {
			p.Online = false
			players[id] = p
			changed = true
		}
	}
	if !changed {
		return
	}
	if e.st.Phase == PhaseLobby {
		for id, p := range players {
			if !p.Online {
				delete(players, id)
			}
		}
	}
	e.commit(func(s *GameState) { s.Players = players })
	e.maybeRevealEarly()
}

func (e *Engine) UpdateSettings(s GameSettings) {
	if e.st.Phase != PhaseLobby || len(s.Source) > MaxSourceBytes {
		return
	}
	s = s.Clamp()
	e.commit(func(st *GameState) { st.Settings = s; st.Error = ""; st.Notice = "" })
}

// SetHost moves host controls to another player. A load the old host started is
// abandoned (only their browser was fetching the songs).
func (e *Engine) SetHost(id string) {
	if id == "" || id == e.st.HostID {
		return
	}
	loading := e.st.Phase == PhaseLoading
	if loading {
		e.disarm()
	}
	e.commit(func(s *GameState) {
		s.HostID = id
		if loading {
			s.Phase = PhaseLobby
			s.Error = MsgLoadingInterrupted
		}
	})
}

// BeginLoading: the host is fetching songs. Allowed from the lobby or the final screen.
func (e *Engine) BeginLoading() bool {
	if e.st.Phase != PhaseLobby && e.st.Phase != PhaseFinal {
		return false
	}
	players := resetScores(e.st.Players, e.st.Phase == PhaseFinal)
	e.commit(func(s *GameState) {
		s.Phase = PhaseLoading
		s.Players = players
		s.Error, s.Notice = "", ""
		s.Reveal, s.Question = nil, nil
	})
	e.arm(LoadingTimeoutMs, func() { e.FailLoading(MsgLoadingInterrupted) })
	return true
}

func (e *Engine) FailLoading(msg string) {
	if e.st.Phase != PhaseLoading {
		return
	}
	e.disarm()
	e.commit(func(s *GameState) { s.Phase = PhaseLobby; s.Error = msg })
}

var ErrBadQuestions = errors.New("ชุดคำถามไม่ถูกต้อง")

func (e *Engine) Start(questions []HostQuestion, warnings []string) error {
	if e.st.Phase != PhaseLoading {
		return errors.New("not loading")
	}
	if len(questions) == 0 || len(questions) > MaxQuestions {
		return ErrBadQuestions
	}
	for _, q := range questions {
		if len(q.Choices) != 4 || q.Answer < 0 || q.Answer >= 4 {
			return ErrBadQuestions
		}
	}
	e.questions = questions
	e.picks = map[string]map[string]rawPick{}
	e.correct = map[string]int{}
	e.startedAt = e.o.Clock.Now()
	e.commit(func(s *GameState) {
		s.TotalRounds = len(questions)
		s.Notice = strings.Join(warnings, " · ")
	})
	e.ask(0)
	return nil
}

func (e *Engine) ask(i int) {
	q := e.questions[i]
	durationMs := e.st.Settings.RoundSeconds * 1000
	lockMs := e.st.Settings.ListenSeconds * 1000
	e.picks[q.ID] = map[string]rawPick{}
	choices := make([]Choice, len(q.Choices))
	for j, c := range q.Choices {
		choices[j] = Choice{Title: c.Title} // titles only until the reveal
	}
	e.commit(func(s *GameState) {
		s.Phase = PhaseQuestion
		s.Round = i
		s.Answered = []string{}
		s.LivePicks = map[string]int{}
		s.Reveal = nil
		s.Question = &PublicQuestion{
			ID: q.ID, Index: i, PreviewURL: q.Track.PreviewURL, StartAt: q.StartAt,
			Choices: choices, LockMs: lockMs, DurationMs: durationMs,
		}
	})
	e.arm(lockMs+durationMs+GraceMs, e.Reveal)
}

func (e *Engine) Answer(a AnswerMsg) {
	s := e.st
	if s.Phase != PhaseQuestion || s.Question == nil || a.QuestionID != s.Question.ID {
		return
	}
	if _, ok := s.Players[a.PlayerID]; !ok {
		return
	}
	picks := e.picks[s.Question.ID]
	if picks == nil {
		return
	}
	if _, done := picks[a.PlayerID]; done {
		return // first answer counts
	}
	if a.Choice < 0 || a.Choice >= len(s.Question.Choices) {
		return
	}
	ms := clamp(a.Ms, 0, s.Question.DurationMs)
	picks[a.PlayerID] = rawPick{a.Choice, ms}
	live := map[string]int{}
	for k, v := range s.LivePicks {
		live[k] = v
	}
	live[a.PlayerID] = a.Choice
	answered := append(append([]string{}, s.Answered...), a.PlayerID)
	e.commit(func(st *GameState) { st.Answered = answered; st.LivePicks = live })
	e.maybeRevealEarly()
}

func (e *Engine) maybeRevealEarly() {
	s := e.st
	if s.Phase != PhaseQuestion {
		return
	}
	answered := map[string]bool{}
	for _, id := range s.Answered {
		answered[id] = true
	}
	n := 0
	for _, p := range s.Players {
		if !p.Online {
			continue
		}
		n++
		if !answered[p.ID] {
			return
		}
	}
	if n > 0 {
		e.arm(EarlyMs, e.Reveal)
	}
}

func (e *Engine) Reveal() {
	s := e.st
	if s.Phase != PhaseQuestion || s.Question == nil {
		return
	}
	e.disarm()
	q := e.questions[s.Round]
	picks := e.picks[s.Question.ID]
	before := RankOf(s.Players)
	players := make(map[string]PlayerState, len(s.Players))
	out := map[string]Pick{}
	for id, p := range s.Players {
		pk, ok := picks[id]
		correct := ok && pk.choice == q.Answer
		if correct {
			p.Streak++
			e.correct[id]++
		} else {
			p.Streak = 0
		}
		gain := 0
		if ok {
			gain = ScoreFor(correct, pk.ms, s.Question.DurationMs, p.Streak)
			out[id] = Pick{Choice: pk.choice, Ms: pk.ms, Gain: gain}
		}
		p.Score += gain
		g, r := gain, before[id]
		p.LastGain, p.PrevRank = &g, &r
		players[id] = p
	}
	e.commit(func(st *GameState) {
		st.Phase = PhaseReveal
		st.LivePicks = nil
		st.Players = players
		st.Reveal = &Reveal{QuestionID: s.Question.ID, Answer: q.Answer, Track: q.Track, Choices: q.Choices, Picks: out}
	})
	e.arm(RevealMs, e.Next)
}

// Next: reveal → standings → next question; the last reveal goes straight to the final results.
func (e *Engine) Next() {
	phase := e.st.Phase
	if phase != PhaseReveal && phase != PhaseStandings {
		return
	}
	e.disarm()
	i := e.st.Round + 1
	switch {
	case i >= len(e.questions):
		e.commit(func(s *GameState) { s.Phase = PhaseFinal; s.Question = nil })
		if e.o.OnFinal != nil {
			correct := make(map[string]int, len(e.correct))
			for k, v := range e.correct {
				correct[k] = v
			}
			e.o.OnFinal(FinalResult{State: e.st, Correct: correct, StartedAt: e.startedAt, EndedAt: e.o.Clock.Now()})
		}
	case phase == PhaseReveal:
		e.commit(func(s *GameState) { s.Phase = PhaseStandings })
		e.arm(StandingsMs, e.Next)
	default:
		e.ask(i)
	}
}

func (e *Engine) BackToLobby() {
	e.disarm()
	e.questions = nil
	e.picks = map[string]map[string]rawPick{}
	players := resetScores(e.st.Players, true)
	e.commit(func(s *GameState) {
		s.Phase = PhaseLobby
		s.Players = players
		s.Round, s.TotalRounds = 0, 0
		s.Question, s.Reveal = nil, nil
		s.Answered = []string{}
		s.LivePicks = nil
		s.Notice, s.Error = "", ""
	})
}

func newPlayer(id, name, color string) PlayerState {
	return PlayerState{ID: id, Name: CleanName(name), Color: SafeColor(color), Online: true}
}

func copyPlayers(in map[string]PlayerState) map[string]PlayerState {
	out := make(map[string]PlayerState, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func resetScores(players map[string]PlayerState, dropOffline bool) map[string]PlayerState {
	out := map[string]PlayerState{}
	for id, p := range players {
		if dropOffline && !p.Online {
			continue
		}
		p.Score, p.Streak, p.LastGain, p.PrevRank = 0, 0, nil, nil
		out[id] = p
	}
	return out
}
