package room

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/bunjonglak123/songquiz-server/internal/clock"
	"github.com/bunjonglak123/songquiz-server/internal/deck"
	"github.com/bunjonglak123/songquiz-server/internal/game"
)

// Room runs on its own goroutine. Every join, leave, message and timer is a
// function executed in that loop, so the engine and member map need no locks.
type Room struct {
	code   string
	hub    *Hub
	cfg    Config
	clk    clock.Clock
	events chan event
	done   chan struct{}

	// loop-owned
	stopped    bool
	engine     *game.Engine
	members    map[string]*member
	seq        int
	last       game.GameState
	hasLast    bool
	batch      clock.Timer
	emptyTimer clock.Timer
}

type member struct {
	m     game.Member
	conn  Conn // nil while in the reconnect grace period
	seq   int
	grace clock.Timer
}

type event struct {
	fn  func()
	ack chan struct{}
}

func newRoom(code string, h *Hub) *Room {
	r := &Room{
		code: code, hub: h, cfg: h.cfg, clk: h.clk,
		events:  make(chan event),
		done:    make(chan struct{}),
		members: map[string]*member{},
	}
	go r.loop()
	r.do(r.armEmpty)
	return r
}

func (r *Room) loop() {
	for ev := range r.events {
		ev.fn()
		close(ev.ack)
		if r.stopped {
			close(r.done)
			return
		}
	}
}

// do runs fn on the room goroutine and waits for it. False if the room is gone.
func (r *Room) do(fn func()) bool {
	ev := event{fn: fn, ack: make(chan struct{})}
	select {
	case r.events <- ev:
		<-ev.ack
		return true
	case <-r.done:
		return false
	}
}

// loopTimer: timer callbacks run inside the loop and are skipped once stopped,
// even if the underlying timer already fired and was waiting to get in.
type loopTimer struct {
	inner   clock.Timer
	stopped bool
}

func (t *loopTimer) Stop() bool {
	if t.stopped {
		return false
	}
	t.stopped = true
	t.inner.Stop()
	return true
}

type loopClock struct{ r *Room }

func (c loopClock) Now() time.Time { return c.r.clk.Now() }

func (c loopClock) AfterFunc(d time.Duration, f func()) clock.Timer {
	t := &loopTimer{}
	t.inner = c.r.clk.AfterFunc(d, func() {
		c.r.do(func() {
			if !t.stopped {
				t.stopped = true
				f()
			}
		})
	})
	return t
}

// Join adds a player, or swaps in a new socket for a returning one.
func (r *Room) Join(pid string, m game.Member, c Conn) error {
	var err error
	ok := r.do(func() {
		if ex := r.members[pid]; ex != nil {
			if ex.conn != nil && ex.conn != c {
				ex.conn.Close(CloseReplaced, "replaced")
			}
			if ex.grace != nil {
				ex.grace.Stop()
				ex.grace = nil
			}
			ex.conn = c
			ex.m = m
		} else {
			if len(r.members) >= r.cfg.MaxPlayers {
				err = ErrRoomFull
				return
			}
			r.seq++
			r.members[pid] = &member{m: m, conn: c, seq: r.seq}
		}
		if r.emptyTimer != nil {
			r.emptyTimer.Stop()
			r.emptyTimer = nil
		}
		if r.engine == nil {
			r.engine = game.NewEngine(game.Options{
				HostID: pid, HostName: m.Name, HostColor: m.Color,
				Clock:    loopClock{r},
				OnChange: r.onChange,
				OnFinal:  r.onFinal,
			})
		} else {
			r.engine.SyncMembers(r.memberList())
			r.fixHost()
		}
		c.Send(encode("state", r.engine.State()))
	})
	if !ok {
		return ErrRoomGone
	}
	return err
}

// Leave starts the grace period; a rejoin with the same pid inside it is seamless.
func (r *Room) Leave(pid string, c Conn) {
	r.do(func() { r.leave(pid, c) })
}

func (r *Room) leave(pid string, c Conn) {
	mem := r.members[pid]
	if mem == nil || mem.conn != c || mem.conn == nil {
		return
	}
	mem.conn = nil
	mem.grace = loopClock{r}.AfterFunc(r.cfg.Grace, func() {
		if r.members[pid] != mem || mem.conn != nil {
			return
		}
		delete(r.members, pid)
		if r.engine != nil {
			r.engine.SyncMembers(r.memberList())
			r.fixHost()
		}
		if len(r.members) == 0 {
			r.armEmpty()
		}
	})
}

// Handle processes one client frame from pid (the socket's own id, never the payload's).
func (r *Room) Handle(pid string, msg Inbound) {
	r.do(func() {
		mem := r.members[pid]
		if mem == nil || mem.conn == nil || r.engine == nil {
			return
		}
		e := r.engine
		switch msg.Type {
		case "ping":
			mem.conn.Send(encode("pong", nil))
			return
		case "answer":
			var a struct {
				QuestionID string  `json:"questionId"`
				Choice     float64 `json:"choice"`
				Ms         float64 `json:"ms"`
			}
			if json.Unmarshal(msg.Data, &a) == nil {
				e.Answer(game.AnswerMsg{PlayerID: pid, QuestionID: a.QuestionID, Choice: int(a.Choice), Ms: int(a.Ms)})
			}
			return
		case "settings", "loading", "start", "loadFailed", "next", "reveal", "lobby":
		default:
			return
		}
		if e.State().HostID != pid {
			mem.conn.Send(encode("error", map[string]string{"message": msgNotHost}))
			return
		}
		switch msg.Type {
		case "settings":
			var s game.GameSettings
			if json.Unmarshal(msg.Data, &s) == nil {
				e.UpdateSettings(s)
			}
		case "loading":
			e.BeginLoading()
		case "start":
			var d struct {
				Deck     string   `json:"deck"`
				Warnings []string `json:"warnings"`
			}
			_ = json.Unmarshal(msg.Data, &d)
			if e.State().Phase != game.PhaseLoading && !e.BeginLoading() {
				return
			}
			_, qs, err := deck.Open(r.cfg.DeckKey, d.Deck)
			if err != nil {
				e.FailLoading(msgBadDeck)
				return
			}
			if len(d.Warnings) > 10 {
				d.Warnings = d.Warnings[:10]
			}
			if err := e.Start(qs, d.Warnings); err != nil {
				e.FailLoading(err.Error())
			}
		case "loadFailed":
			var d struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(msg.Data, &d)
			m := []rune(strings.TrimSpace(d.Message))
			if len(m) > 200 {
				m = m[:200]
			}
			if len(m) == 0 {
				m = []rune(msgLoadFail)
			}
			e.FailLoading(string(m))
		case "next":
			e.Next()
		case "reveal":
			e.Reveal()
		case "lobby":
			e.BackToLobby()
		}
	})
}

func (r *Room) memberList() []game.Member {
	out := make([]game.Member, 0, len(r.members))
	for _, m := range r.members {
		out = append(out, m.m)
	}
	return out
}

// fixHost hands controls to the earliest joiner when the host has left.
func (r *Room) fixHost() {
	if r.engine == nil {
		return
	}
	if _, here := r.members[r.engine.State().HostID]; here {
		return
	}
	var best *member
	for _, m := range r.members {
		if best == nil || m.seq < best.seq {
			best = m
		}
	}
	if best != nil {
		r.engine.SetHost(best.m.ID)
	}
}

func urgent(prev, next game.GameState) bool {
	qid := func(s game.GameState) string {
		if s.Question == nil {
			return ""
		}
		return s.Question.ID
	}
	return prev.Phase != next.Phase || prev.Round != next.Round || qid(prev) != qid(next) ||
		prev.HostID != next.HostID || prev.Error != next.Error
}

func (r *Room) onChange(s game.GameState) {
	if !r.hasLast || urgent(r.last, s) {
		r.flush()
		return
	}
	if r.batch == nil {
		r.batch = loopClock{r}.AfterFunc(r.cfg.BatchEvery, func() {
			r.batch = nil
			r.flush()
		})
	}
}

func (r *Room) flush() {
	if r.batch != nil {
		r.batch.Stop()
		r.batch = nil
	}
	s := r.engine.State()
	r.last, r.hasLast = s, true
	frame := encode("state", s)
	var slow []string
	for pid, m := range r.members {
		if m.conn != nil && !m.conn.Send(frame) {
			slow = append(slow, pid)
		}
	}
	sort.Strings(slow)
	for _, pid := range slow {
		c := r.members[pid].conn
		c.Close(CloseSlow, "too slow")
		r.leave(pid, c)
	}
}

func (r *Room) onFinal(res game.FinalResult) {
	if r.hub.save != nil {
		go r.hub.save(res, r.code)
	}
}

func (r *Room) armEmpty() {
	if r.emptyTimer != nil {
		r.emptyTimer.Stop()
	}
	r.emptyTimer = loopClock{r}.AfterFunc(r.cfg.EmptyTTL, func() {
		if len(r.members) == 0 {
			r.hub.remove(r.code, r)
			r.shutdown()
		}
	})
}

// shutdown runs inside the loop; the loop exits after the current event.
func (r *Room) shutdown() {
	if r.engine != nil {
		r.engine.Dispose()
	}
	for _, m := range r.members {
		if m.conn != nil {
			m.conn.Close(1001, "server shutting down")
		}
	}
	r.stopped = true
}
