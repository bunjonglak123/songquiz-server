package room

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bunjonglak123/songquiz-server/internal/clock"
	"github.com/bunjonglak123/songquiz-server/internal/deck"
	"github.com/bunjonglak123/songquiz-server/internal/game"
)

type fakeConn struct {
	mu     sync.Mutex
	frames []string
	closed int
	full   bool
}

func (c *fakeConn) Send(b []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.full {
		return false
	}
	c.frames = append(c.frames, string(b))
	return true
}

func (c *fakeConn) Close(code int, _ string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = code
}

func (c *fakeConn) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.frames)
}

type frame struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func (c *fakeConn) all() []frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]frame, 0, len(c.frames))
	for _, s := range c.frames {
		var f frame
		_ = json.Unmarshal([]byte(s), &f)
		out = append(out, f)
	}
	return out
}

func (c *fakeConn) state(t *testing.T) game.GameState {
	t.Helper()
	fs := c.all()
	for i := len(fs) - 1; i >= 0; i-- {
		if fs[i].Type == "state" {
			var s game.GameState
			if err := json.Unmarshal(fs[i].Data, &s); err != nil {
				t.Fatal(err)
			}
			return s
		}
	}
	t.Fatal("no state frame")
	return game.GameState{}
}

func (c *fakeConn) lastError() string {
	fs := c.all()
	for i := len(fs) - 1; i >= 0; i-- {
		if fs[i].Type == "error" {
			var e struct{ Message string }
			_ = json.Unmarshal(fs[i].Data, &e)
			return e.Message
		}
	}
	return ""
}

type env struct {
	t     *testing.T
	clk   *clock.Fake
	hub   *Hub
	saved chan game.FinalResult
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, clk: clock.NewFake(time.Unix(1_700_000_000, 0)), saved: make(chan game.FinalResult, 4)}
	e.hub = NewHub(DefaultConfig(deck.Key("test-secret")), e.clk, func(r game.FinalResult, code string) { e.saved <- r })
	t.Cleanup(e.hub.Close)
	return e
}

func (e *env) room(create bool) *Room {
	e.t.Helper()
	r, err := e.hub.Get("ROOM1", create)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) join(r *Room, pid string) *fakeConn {
	e.t.Helper()
	c := &fakeConn{}
	if err := r.Join(pid, game.Member{ID: pid, Name: "ผู้เล่น " + pid}, c); err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) advance(d time.Duration) { e.clk.Advance(d) }

func gameMember(id string) game.Member { return game.Member{ID: id, Name: id} }

func send(r *Room, pid, typ string, data any) {
	b, _ := json.Marshal(data)
	r.Handle(pid, Inbound{Type: typ, Data: b})
}

func fixtureDeck(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../deck/testdata/deck.txt")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func startGame(t *testing.T, r *Room) {
	t.Helper()
	send(r, "host", "settings", map[string]any{"source": map[string]any{"kind": "chart", "size": 100}, "rounds": 5, "roundSeconds": 5, "listenSeconds": 0})
	send(r, "host", "loading", nil)
	send(r, "host", "start", map[string]any{"deck": fixtureDeck(t), "warnings": []string{}})
}

func TestCreatorBecomesHostAndGetsState(t *testing.T) {
	e := newEnv(t)
	c := e.join(e.room(true), "host")
	s := c.state(t)
	if s.HostID != "host" || s.Phase != game.PhaseLobby || !s.Players["host"].Online {
		t.Fatalf("%+v", s)
	}
}

func TestJoinGetsCurrentState(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	h := e.join(r, "host")
	g := e.join(r, "guest")
	if len(g.state(t).Players) != 2 || len(h.state(t).Players) != 2 {
		t.Fatal("both should see 2 players")
	}
}

func TestFullGameTwoPlayers(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	h := e.join(r, "host")
	g := e.join(r, "guest")
	startGame(t, r)
	for round := 0; round < 2; round++ {
		s := g.state(t)
		if s.Phase != game.PhaseQuestion || s.Round != round {
			t.Fatalf("round %d: %+v", round, s)
		}
		send(r, "host", "answer", map[string]any{"questionId": s.Question.ID, "choice": 2, "ms": 100})
		send(r, "guest", "answer", map[string]any{"questionId": s.Question.ID, "choice": 1, "ms": 100})
		e.advance(600 * time.Millisecond)
		if h.state(t).Phase != game.PhaseReveal {
			t.Fatal("no reveal")
		}
		e.advance(10 * time.Second)
	}
	if g.state(t).Phase != game.PhaseFinal {
		t.Fatalf("phase %s", g.state(t).Phase)
	}
	select {
	case res := <-e.saved:
		if len(res.State.Players) != 2 || res.Correct["host"] != 2 {
			t.Fatalf("saved %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("saver not called")
	}
}

func TestRefreshWithinGraceKeepsEverything(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	h := e.join(r, "host")
	g := e.join(r, "guest")
	startGame(t, r)
	q := g.state(t).Question.ID
	send(r, "host", "answer", map[string]any{"questionId": q, "choice": 2, "ms": 0})
	before := g.count()
	r.Leave("host", h)
	e.advance(2 * time.Second)
	h2 := e.join(r, "host")
	s := h2.state(t)
	if s.HostID != "host" || !s.Players["host"].Online || s.Phase != game.PhaseQuestion || s.Question.ID != q {
		t.Fatalf("after refresh %+v", s)
	}
	for _, f := range g.all()[before:] {
		if f.Type == "state" && strings.Contains(string(f.Data), `"online":false`) {
			t.Fatal("guest saw the host go offline during the grace period")
		}
	}
	e.advance(5 * time.Second)
	if !g.state(t).Players["host"].Online {
		t.Fatal("host marked offline after rejoining")
	}
}

func TestHostGoneHandsControls(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	h := e.join(r, "host")
	g := e.join(r, "guest")
	e.join(r, "third")
	startGame(t, r)
	r.Leave("host", h)
	e.advance(3 * time.Second)
	if s := g.state(t); s.HostID != "guest" || s.Players["host"].Online {
		t.Fatalf("controls should go to the earliest joiner: %+v", s)
	}
	e.advance(60 * time.Second)
	if g.state(t).Phase != game.PhaseFinal {
		t.Fatal("game should keep running to the end without the host")
	}
}

func TestHostLeavesWhileLoading(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	h := e.join(r, "host")
	g := e.join(r, "guest")
	send(r, "host", "loading", nil)
	r.Leave("host", h)
	e.advance(3 * time.Second)
	s := g.state(t)
	if s.HostID != "guest" || s.Phase != game.PhaseLobby {
		t.Fatalf("new host should land in the lobby, not stuck loading: %+v", s)
	}
	send(r, "guest", "loading", nil)
	if g.state(t).Phase != game.PhaseLoading {
		t.Fatal("new host can't start the game")
	}
}

func TestNonHostHostCommandsRejected(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	e.join(r, "host")
	g := e.join(r, "guest")
	send(r, "guest", "loading", nil)
	if g.lastError() != "เฉพาะหัวห้องเท่านั้น" {
		t.Fatalf("error %q", g.lastError())
	}
	if g.state(t).Phase != game.PhaseLobby {
		t.Fatal("guest started loading")
	}
}

func TestReplacedConnectionClosed4409(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	old := e.join(r, "host")
	e.join(r, "host")
	if old.closed != CloseReplaced {
		t.Fatalf("closed %d", old.closed)
	}
	r.Leave("host", old) // late leave from the old socket must not affect the new one
	e.advance(5 * time.Second)
	if _, err := e.hub.Get("ROOM1", false); err != nil {
		t.Fatal("room vanished")
	}
}

func TestBadDeckReturnsToLobby(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	h := e.join(r, "host")
	send(r, "host", "loading", nil)
	send(r, "host", "start", map[string]any{"deck": "x.y.z.w"})
	s := h.state(t)
	if s.Phase != game.PhaseLobby || s.Error != "เปิดชุดคำถามไม่สำเร็จ" {
		t.Fatalf("%+v", s)
	}
	startGame(t, r)
	if h.state(t).Phase != game.PhaseQuestion {
		t.Fatal("room unusable after a bad deck")
	}
}

func TestLoadFailed(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	h := e.join(r, "host")
	send(r, "host", "loading", nil)
	send(r, "host", "loadFailed", map[string]any{"message": "ไม่พบเพลง"})
	if s := h.state(t); s.Phase != game.PhaseLobby || s.Error != "ไม่พบเพลง" {
		t.Fatalf("%+v", s)
	}
}

func TestBatchingCoalescesAnswers(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	h := e.join(r, "host")
	for _, p := range []string{"a", "b", "c"} {
		e.join(r, p)
	}
	startGame(t, r)
	q := h.state(t).Question.ID
	before := h.count()
	for _, p := range []string{"a", "b", "c"} {
		send(r, p, "answer", map[string]any{"questionId": q, "choice": 0, "ms": 0})
	}
	if h.count() != before {
		t.Fatal("answer counts should wait for the batch")
	}
	e.advance(300 * time.Millisecond)
	if h.count() != before+1 {
		t.Fatalf("got %d frames, want 1 batched", h.count()-before)
	}
	if len(h.state(t).Answered) != 3 {
		t.Fatal("batched state should include all 3 answers")
	}
}

func TestRoomFull(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	for i := 0; i < 30; i++ {
		e.join(r, fmt.Sprintf("p%02d", i))
	}
	if err := r.Join("late", game.Member{ID: "late", Name: "late"}, &fakeConn{}); err != ErrRoomFull {
		t.Fatalf("err %v", err)
	}
}

func TestSlowConnDropped(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	e.join(r, "host")
	g := e.join(r, "guest")
	g.mu.Lock()
	g.full = true
	g.mu.Unlock()
	send(r, "host", "loading", nil) // urgent broadcast hits the full buffer
	if g.closed != CloseSlow {
		t.Fatalf("closed %d", g.closed)
	}
	e.advance(3 * time.Second)
	r2, _ := e.hub.Get("ROOM1", false)
	h := e.join(r2, "watcher")
	if _, ok := h.state(t).Players["guest"]; ok && h.state(t).Players["guest"].Online {
		t.Fatal("slow guest still online")
	}
}

func TestPingPong(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	h := e.join(r, "host")
	send(r, "host", "ping", nil)
	fs := h.all()
	if fs[len(fs)-1].Type != "pong" {
		t.Fatal("no pong")
	}
}
