package game

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bunjonglak123/songquiz-server/internal/clock"
)

type harness struct {
	e      *Engine
	clk    *clock.Fake
	states []GameState
	finals []FinalResult
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{clk: clock.NewFake(time.Unix(1_700_000_000, 0))}
	h.e = NewEngine(Options{
		HostID: "host", HostName: "โฮสต์", HostColor: "#112233", Clock: h.clk,
		OnChange: func(s GameState) { h.states = append(h.states, s) },
		OnFinal:  func(r FinalResult) { h.finals = append(h.finals, r) },
	})
	return h
}

func (h *harness) advance(ms int) { h.clk.Advance(time.Duration(ms) * time.Millisecond) }

func qs(n int) []HostQuestion {
	out := make([]HostQuestion, n)
	for i := range out {
		out[i] = HostQuestion{
			ID: fmt.Sprintf("q%d", i), Answer: 2,
			Track: Track{ID: fmt.Sprintf("t%d", i), Title: "เพลง", Artist: "ศิลปิน", PreviewURL: "https://x/a.m4a"},
		}
		for c := 0; c < 4; c++ {
			out[i].Choices = append(out[i].Choices, Choice{Title: fmt.Sprintf("ตัวเลือก %d", c), Artist: "ศิลปิน"})
		}
	}
	return out
}

// startGame: host + guest in the room, settings applied, game started with n questions.
func startGame(t *testing.T, h *harness, n, roundSeconds, listen int) {
	t.Helper()
	h.e.SyncMembers([]Member{{ID: "host", Name: "โฮสต์"}, {ID: "guest", Name: "เกสต์"}})
	h.e.UpdateSettings(GameSettings{Rounds: 5, RoundSeconds: roundSeconds, ListenSeconds: listen})
	if !h.e.BeginLoading() {
		t.Fatal("BeginLoading refused")
	}
	if err := h.e.Start(qs(n), nil); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) answer(pid string, choice, ms int) {
	h.e.Answer(AnswerMsg{PlayerID: pid, QuestionID: h.e.State().Question.ID, Choice: choice, Ms: ms})
}

func TestFullGame(t *testing.T) {
	h := newHarness(t)
	startGame(t, h, 2, 15, 3)
	s := h.e.State()
	if s.Phase != PhaseQuestion || s.TotalRounds != 2 || s.Question.LockMs != 3000 || s.Question.DurationMs != 15000 {
		t.Fatalf("bad question state %+v", s)
	}
	h.answer("host", 2, 0)
	h.answer("guest", 1, 1000)
	if h.e.State().Phase != PhaseQuestion {
		t.Fatal("revealed before the 600ms beat")
	}
	h.advance(600)
	s = h.e.State()
	if s.Phase != PhaseReveal || s.Players["host"].Score != 1000 || s.Players["guest"].Score != 0 {
		t.Fatalf("after reveal %+v", s.Players)
	}
	if s.Reveal.Answer != 2 || s.Reveal.Picks["guest"].Choice != 1 {
		t.Fatalf("reveal %+v", s.Reveal)
	}
	h.advance(5000)
	if h.e.State().Phase != PhaseStandings {
		t.Fatal("no standings after 5000ms")
	}
	h.advance(4500)
	if h.e.State().Phase != PhaseQuestion || h.e.State().Round != 1 {
		t.Fatal("no second question after standings")
	}
	h.answer("host", 2, 0)
	h.answer("guest", 2, 0)
	h.advance(600 + 5000)
	if h.e.State().Phase != PhaseFinal {
		t.Fatalf("phase %s, want final straight after last reveal", h.e.State().Phase)
	}
	if h.e.State().Players["host"].Score != 2100 {
		t.Fatalf("host score %d, want 1000 + 1100 streak", h.e.State().Players["host"].Score)
	}
	if len(h.finals) != 1 || h.finals[0].Correct["host"] != 2 || h.finals[0].Correct["guest"] != 1 {
		t.Fatalf("finals %+v", h.finals)
	}
	h.advance(60_000)
	if len(h.finals) != 1 {
		t.Fatal("OnFinal fired twice")
	}
}

func TestRejectsBadAnswers(t *testing.T) {
	h := newHarness(t)
	startGame(t, h, 1, 15, 0)
	qid := h.e.State().Question.ID
	h.e.Answer(AnswerMsg{PlayerID: "guest", QuestionID: "stale", Choice: 1})
	h.e.Answer(AnswerMsg{PlayerID: "guest", QuestionID: qid, Choice: 4})
	h.e.Answer(AnswerMsg{PlayerID: "guest", QuestionID: qid, Choice: -1})
	h.e.Answer(AnswerMsg{PlayerID: "nobody", QuestionID: qid, Choice: 1})
	if len(h.e.State().Answered) != 0 {
		t.Fatalf("bad answers accepted: %v", h.e.State().Answered)
	}
	h.e.Answer(AnswerMsg{PlayerID: "guest", QuestionID: qid, Choice: 2, Ms: 999_999})
	h.e.Answer(AnswerMsg{PlayerID: "guest", QuestionID: qid, Choice: 0, Ms: 0})
	h.e.Reveal()
	p := h.e.State().Reveal.Picks["guest"]
	if p.Choice != 2 || p.Ms != 15000 {
		t.Fatalf("pick %+v: first answer should count, ms clamped to 15000", p)
	}
}

func TestLeaverMidQuestionDoesNotBlockEarlyReveal(t *testing.T) {
	h := newHarness(t)
	startGame(t, h, 2, 15, 0)
	h.answer("host", 2, 0)
	h.e.SyncMembers([]Member{{ID: "host", Name: "โฮสต์"}})
	h.advance(600)
	if h.e.State().Phase != PhaseReveal {
		t.Fatal("offline guest blocked early reveal")
	}
	if _, ok := h.e.State().Players["guest"]; !ok {
		t.Fatal("mid-game leaver should stay listed (offline)")
	}
}

func TestLobbyLeaversRemoved(t *testing.T) {
	h := newHarness(t)
	h.e.SyncMembers([]Member{{ID: "host", Name: "โฮสต์"}, {ID: "guest", Name: "เกสต์"}})
	h.e.SyncMembers([]Member{{ID: "host", Name: "โฮสต์"}})
	if _, ok := h.e.State().Players["guest"]; ok {
		t.Fatal("lobby leaver kept")
	}
}

func TestEarlyRevealIgnoresOfflineHost(t *testing.T) {
	h := newHarness(t)
	startGame(t, h, 2, 15, 0)
	h.e.SyncMembers([]Member{{ID: "guest", Name: "เกสต์"}})
	if h.e.State().Players["host"].Online {
		t.Fatal("host should go offline like any player")
	}
	h.answer("guest", 2, 0)
	h.advance(600)
	if h.e.State().Phase != PhaseReveal {
		t.Fatal("offline host blocked early reveal")
	}
}

func TestTimeoutIsListenPlusAnswerPlusGrace(t *testing.T) {
	h := newHarness(t)
	startGame(t, h, 2, 5, 0)
	if h.e.State().Question.LockMs != 0 {
		t.Fatal("listen 0 should mean no lock")
	}
	h.advance(7499)
	if h.e.State().Phase != PhaseQuestion {
		t.Fatal("revealed early")
	}
	h.advance(1)
	if h.e.State().Phase != PhaseReveal {
		t.Fatal("no reveal at 5000 + 2500ms")
	}
}

func TestPlayAgainResetsScoresAndDropsOffline(t *testing.T) {
	h := newHarness(t)
	h.e.SyncMembers([]Member{{ID: "host", Name: "โฮสต์"}, {ID: "guest", Name: "เกสต์"}, {ID: "third", Name: "สาม"}})
	h.e.UpdateSettings(GameSettings{Rounds: 3, RoundSeconds: 5, ListenSeconds: 0})
	h.e.BeginLoading()
	_ = h.e.Start(qs(1), nil)
	h.answer("host", 2, 0)
	h.e.SyncMembers([]Member{{ID: "host", Name: "โฮสต์"}, {ID: "guest", Name: "เกสต์"}})
	h.e.Reveal()
	h.e.Next()
	if h.e.State().Phase != PhaseFinal {
		t.Fatal("not final")
	}
	if !h.e.BeginLoading() {
		t.Fatal("play again refused")
	}
	s := h.e.State()
	if s.Players["host"].Score != 0 || s.Players["host"].Streak != 0 {
		t.Fatal("scores not reset")
	}
	if _, ok := s.Players["third"]; ok {
		t.Fatal("offline player kept on play again")
	}
}

func TestSkipRevealAndStandings(t *testing.T) {
	h := newHarness(t)
	startGame(t, h, 2, 15, 0)
	h.e.Reveal()
	h.e.Next()
	if h.e.State().Phase != PhaseStandings {
		t.Fatal("skip reveal")
	}
	h.e.Next()
	if h.e.State().Phase != PhaseQuestion || h.e.State().Round != 1 {
		t.Fatal("skip standings")
	}
}

func TestColorsFromMembers(t *testing.T) {
	h := newHarness(t)
	h.e.SyncMembers([]Member{{ID: "host", Name: "โฮสต์"}, {ID: "a", Name: "A", Color: "#abcdef"}, {ID: "b", Name: "B", Color: "url(x)"}})
	s := h.e.State()
	if s.Players["a"].Color != "#abcdef" || s.Players["b"].Color != "" || s.Players["host"].Color != "#112233" {
		t.Fatalf("colours %+v", s.Players)
	}
}

func TestMaxPlayers(t *testing.T) {
	h := newHarness(t)
	ms := []Member{{ID: "host", Name: "โฮสต์"}}
	for i := 0; i < 30; i++ {
		ms = append(ms, Member{ID: fmt.Sprintf("p%02d", i), Name: "x"})
	}
	h.e.SyncMembers(ms)
	if n := len(h.e.State().Players); n != MaxPlayers {
		t.Fatalf("%d players, want %d", n, MaxPlayers)
	}
}

func TestStartValidation(t *testing.T) {
	bad := map[string][]HostQuestion{"none": {}}
	three := qs(1)
	three[0].Choices = three[0].Choices[:3]
	bad["three choices"] = three
	ans := qs(1)
	ans[0].Answer = 4
	bad["answer out of range"] = ans
	bad["too many"] = qs(31)
	for name, q := range bad {
		h := newHarness(t)
		h.e.BeginLoading()
		if err := h.e.Start(q, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if h.e.State().Phase != PhaseLoading {
			t.Errorf("%s: phase changed to %s", name, h.e.State().Phase)
		}
	}
	h := newHarness(t)
	if err := h.e.Start(qs(1), nil); err == nil {
		t.Error("Start outside loading accepted")
	}
}

func TestFailLoading(t *testing.T) {
	h := newHarness(t)
	h.e.BeginLoading()
	h.e.FailLoading("โหลดเพลงไม่สำเร็จ")
	s := h.e.State()
	if s.Phase != PhaseLobby || s.Error != "โหลดเพลงไม่สำเร็จ" {
		t.Fatalf("%+v", s)
	}
}

func TestPublicQuestionHasNoArtistsOrAnswer(t *testing.T) {
	h := newHarness(t)
	startGame(t, h, 2, 15, 3)
	b, _ := json.Marshal(h.e.State())
	if strings.Contains(string(b), `"answer"`) || strings.Contains(string(b), `"artist"`) {
		t.Fatalf("leak: %s", b)
	}
}

func TestSettingsOnlyInLobby(t *testing.T) {
	h := newHarness(t)
	h.e.UpdateSettings(GameSettings{Rounds: 99, RoundSeconds: 5, ListenSeconds: 0})
	if h.e.State().Settings.Rounds != 30 {
		t.Fatal("settings not clamped")
	}
	startGame(t, h, 1, 15, 0)
	h.e.UpdateSettings(GameSettings{Rounds: 7, RoundSeconds: 5, ListenSeconds: 0})
	if h.e.State().Settings.Rounds == 7 {
		t.Fatal("settings changed mid-game")
	}
}

func TestLoadingTimesOut(t *testing.T) {
	h := newHarness(t)
	h.e.BeginLoading()
	h.advance(LoadingTimeoutMs - 1)
	if h.e.State().Phase != PhaseLoading {
		t.Fatal("gave up too early")
	}
	h.advance(1)
	if s := h.e.State(); s.Phase != PhaseLobby || s.Error != MsgLoadingInterrupted {
		t.Fatalf("stuck loading: %+v", s)
	}
}

func TestLoadingTimeoutClearedByStart(t *testing.T) {
	h := newHarness(t)
	startGame(t, h, 2, 25, 5)
	h.advance(LoadingTimeoutMs)
	if h.e.State().Phase == PhaseLobby {
		t.Fatal("loading timeout fired after the game started")
	}
}

func TestOversizedSourceIgnored(t *testing.T) {
	h := newHarness(t)
	big := GameSettings{Source: json.RawMessage(`{"kind":"custom","name":"` + strings.Repeat("x", MaxSourceBytes) + `"}`), Rounds: 7, RoundSeconds: 5}
	h.e.UpdateSettings(big)
	if s := h.e.State().Settings; s.Rounds == 7 || len(s.Source) > MaxSourceBytes {
		t.Fatalf("oversized settings accepted (%d bytes)", len(s.Source))
	}
}

func TestSetHost(t *testing.T) {
	h := newHarness(t)
	v := h.e.State().Version
	h.e.SetHost("guest")
	if h.e.State().HostID != "guest" || h.e.State().Version != v+1 {
		t.Fatal("SetHost did not commit")
	}
}

func TestBackToLobby(t *testing.T) {
	h := newHarness(t)
	startGame(t, h, 2, 15, 0)
	h.e.BackToLobby()
	s := h.e.State()
	if s.Phase != PhaseLobby || s.Question != nil || s.TotalRounds != 0 {
		t.Fatalf("%+v", s)
	}
	h.advance(60_000)
	if h.e.State().Phase != PhaseLobby {
		t.Fatal("old timer still running after BackToLobby")
	}
}
