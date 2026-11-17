package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/bunjonglak123/songquiz-server/internal/clock"
	"github.com/bunjonglak123/songquiz-server/internal/deck"
	"github.com/bunjonglak123/songquiz-server/internal/game"
	"github.com/bunjonglak123/songquiz-server/internal/room"
	"github.com/bunjonglak123/songquiz-server/internal/store"
)

type fakeBoard struct{ calls int }

func (f *fakeBoard) Top(_ context.Context, period string, limit int, _ time.Time) ([]store.Entry, error) {
	f.calls++
	if period != "week" && period != "all" {
		return nil, store.ErrBadPeriod
	}
	return []store.Entry{{Name: "เอ", Score: 1000, Correct: 5, Rounds: 5}}, nil
}

type server struct {
	*httptest.Server
	board *fakeBoard
	wsURL string
}

func newServer(t *testing.T) *server {
	t.Helper()
	cfg := room.DefaultConfig(deck.Key("test-secret"))
	cfg.Grace = 200 * time.Millisecond
	cfg.BatchEvery = 20 * time.Millisecond
	hub := room.NewHub(cfg, clock.Real(), nil)
	t.Cleanup(hub.Close)
	b := &fakeBoard{}
	srv := httptest.NewServer(NewRouter(Deps{
		Hub: hub, Board: b, AllowedOrigins: []string{"app.example"}, Clock: clock.Real(),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}))
	t.Cleanup(srv.Close)
	return &server{Server: srv, board: b, wsURL: "ws" + strings.TrimPrefix(srv.URL, "http")}
}

type client struct {
	t      *testing.T
	c      *websocket.Conn
	frames chan frame
	closed chan websocket.StatusCode
	all    []frame
}

type frame struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func (s *server) dial(t *testing.T, code, pid string, create bool) *client {
	t.Helper()
	u := s.wsURL + "/ws/rooms/" + code + "?pid=" + pid + "&name=" + pid + "&color=%23112233"
	if create {
		u += "&create=1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://app.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(1 << 20)
	cl := &client{t: t, c: c, frames: make(chan frame, 256), closed: make(chan websocket.StatusCode, 1)}
	go func() {
		for {
			_, b, err := c.Read(context.Background())
			if err != nil {
				cl.closed <- websocket.CloseStatus(err)
				close(cl.frames)
				return
			}
			var f frame
			_ = json.Unmarshal(b, &f)
			cl.frames <- f
		}
	}()
	t.Cleanup(func() { c.CloseNow() })
	return cl
}

func (c *client) send(typ string, data any) {
	c.t.Helper()
	b, _ := json.Marshal(map[string]any{"type": typ, "data": data})
	if err := c.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

// waitState reads frames until a state matches pred.
func (c *client) waitState(pred func(game.GameState) bool) game.GameState {
	c.t.Helper()
	deadline := time.After(8 * time.Second)
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				c.t.Fatal("connection closed while waiting for state")
			}
			c.all = append(c.all, f)
			if f.Type != "state" {
				continue
			}
			var s game.GameState
			_ = json.Unmarshal(f.Data, &s)
			if pred(s) {
				return s
			}
		case <-deadline:
			c.t.Fatal("timed out waiting for state")
		}
	}
}

func (c *client) waitType(typ string) frame {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				c.t.Fatal("closed")
			}
			c.all = append(c.all, f)
			if f.Type == typ {
				return f
			}
		case <-deadline:
			c.t.Fatalf("timed out waiting for %s", typ)
		}
	}
}

func fixtureDeck(t *testing.T) string {
	b, err := os.ReadFile("../deck/testdata/deck.txt")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func phase(p game.Phase) func(game.GameState) bool {
	return func(s game.GameState) bool { return s.Phase == p }
}

func TestHealthz(t *testing.T) {
	s := newServer(t)
	res, err := http.Get(s.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || strings.TrimSpace(string(b)) != `{"ok":true}` {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestJoinMissingRoom4404(t *testing.T) {
	s := newServer(t)
	c := s.dial(t, "NOPE1", "pid-aaaaaaaa", false)
	select {
	case code := <-c.closed:
		if code != room.CloseNotFound {
			t.Fatalf("close %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not closed")
	}
}

// playToFinal runs the 2-question fixture game; host skips reveal and standings.
func playToFinal(t *testing.T, host, guest *client) {
	t.Helper()
	host.send("settings", map[string]any{"source": map[string]any{"kind": "chart", "size": 100}, "rounds": 5, "roundSeconds": 5, "listenSeconds": 0})
	host.send("loading", nil)
	guest.waitState(phase(game.PhaseLoading))
	host.send("start", map[string]any{"deck": fixtureDeck(t), "warnings": []string{}})
	for round := 0; round < 2; round++ {
		r := round
		s := guest.waitState(func(s game.GameState) bool { return s.Phase == game.PhaseQuestion && s.Round == r })
		host.send("answer", map[string]any{"questionId": s.Question.ID, "choice": 2, "ms": 100})
		guest.send("answer", map[string]any{"questionId": s.Question.ID, "choice": 1, "ms": 100})
		host.waitState(phase(game.PhaseReveal))
		host.send("next", nil)
		if r == 0 {
			host.waitState(phase(game.PhaseStandings))
			host.send("next", nil)
		}
	}
}

func TestTwoPlayerGameOverWS(t *testing.T) {
	s := newServer(t)
	host := s.dial(t, "GAME1", "pid-host0001", true)
	host.waitState(phase(game.PhaseLobby))
	guest := s.dial(t, "game1", "pid-guest001", false)
	host.waitState(func(s game.GameState) bool { return len(s.Players) == 2 })
	playToFinal(t, host, guest)
	f := guest.waitState(phase(game.PhaseFinal))
	if f.Players["pid-host0001"].Score <= f.Players["pid-guest001"].Score {
		t.Fatalf("scores %+v", f.Players)
	}
}

func TestAnswersNeverLeakBeforeReveal(t *testing.T) {
	s := newServer(t)
	host := s.dial(t, "LEAK1", "pid-host0001", true)
	host.waitState(phase(game.PhaseLobby))
	guest := s.dial(t, "LEAK1", "pid-guest001", false)
	host.waitState(func(s game.GameState) bool { return len(s.Players) == 2 })
	playToFinal(t, host, guest)
	guest.waitState(phase(game.PhaseFinal))
	for _, c := range []*client{host, guest} {
		for _, f := range c.all {
			if f.Type == "state" && strings.Contains(string(f.Data), `"phase":"question"`) {
				if strings.Contains(string(f.Data), `"answer"`) || strings.Contains(string(f.Data), `"artist"`) {
					t.Fatalf("leak in question frame: %s", f.Data)
				}
			}
		}
	}
}

func TestNonHostCommandsRejected(t *testing.T) {
	s := newServer(t)
	host := s.dial(t, "AUTH1", "pid-host0001", true)
	host.waitState(phase(game.PhaseLobby))
	guest := s.dial(t, "AUTH1", "pid-guest001", false)
	guest.waitState(func(s game.GameState) bool { return len(s.Players) == 2 })
	guest.send("start", map[string]any{"deck": fixtureDeck(t)})
	f := guest.waitType("error")
	if !strings.Contains(string(f.Data), "เฉพาะหัวห้องเท่านั้น") {
		t.Fatalf("error %s", f.Data)
	}
	host.send("loading", nil)
	host.send("start", map[string]any{"deck": fixtureDeck(t)})
	q := guest.waitState(phase(game.PhaseQuestion))
	guest.send("answer", map[string]any{"playerId": "pid-host0001", "questionId": q.Question.ID, "choice": 3, "ms": 1})
	st := host.waitState(func(s game.GameState) bool { return len(s.Answered) == 1 })
	if st.Answered[0] != "pid-guest001" || st.LivePicks["pid-guest001"] != 3 {
		t.Fatalf("answer recorded under the wrong player: %+v / %+v", st.Answered, st.LivePicks)
	}
}

func TestBadOriginRejected(t *testing.T) {
	s := newServer(t)
	_, res, err := websocket.Dial(context.Background(), s.wsURL+"/ws/rooms/X1?pid=pid-aaaaaaaa&name=a&create=1",
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://evil.example"}}})
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("err=%v res=%v", err, res)
	}
}

func TestBadPidRejected(t *testing.T) {
	s := newServer(t)
	for _, q := range []string{"pid=x&name=a", "pid=pid-aaaaaaaa&name=%20%20", "pid=pid%3Cscript%3E00&name=a"} {
		c, _, err := websocket.Dial(context.Background(), s.wsURL+"/ws/rooms/X1?create=1&"+q,
			&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://app.example"}}})
		if err != nil {
			continue // refused before upgrade is fine too
		}
		_, _, err = c.Read(context.Background())
		if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
			t.Errorf("%s: %v", q, err)
		}
	}
}

func TestLeaderboardValidationAndCORS(t *testing.T) {
	s := newServer(t)
	req, _ := http.NewRequest("GET", s.URL+"/api/leaderboard?period=week&limit=5", nil)
	req.Header.Set("Origin", "https://app.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var got []store.Entry
	_ = json.NewDecoder(res.Body).Decode(&got)
	if res.StatusCode != 200 || len(got) != 1 || got[0].Name != "เอ" {
		t.Fatalf("%d %+v", res.StatusCode, got)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatalf("CORS %q", res.Header.Get("Access-Control-Allow-Origin"))
	}
	_, _ = http.Get(s.URL + "/api/leaderboard?period=week&limit=5")
	if s.board.calls != 1 {
		t.Fatalf("cache miss: %d calls", s.board.calls)
	}
	for _, q := range []string{"period=month", "limit=0", "limit=51", "limit=abc"} {
		r, _ := http.Get(s.URL + "/api/leaderboard?" + q)
		if r.StatusCode != 400 {
			t.Errorf("%s → %d", q, r.StatusCode)
		}
	}
	req, _ = http.NewRequest("GET", s.URL+"/api/leaderboard", nil)
	req.Header.Set("Origin", "https://evil.example")
	res, _ = http.DefaultClient.Do(req)
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS allowed a foreign origin")
	}
}
