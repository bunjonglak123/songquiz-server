// Package ws connects browser WebSockets to rooms.
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/bunjonglak123/songquiz-server/internal/game"
	"github.com/bunjonglak123/songquiz-server/internal/room"
)

const (
	MaxFrame    = 512 << 10
	ReadTimeout = 60 * time.Second // clients ping every 20 s
	sendBuffer  = 64
)

var pidRe = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// conn adapts a websocket to room.Conn: non-blocking sends through a buffered
// channel, drained by one writer goroutine.
type conn struct {
	ws        *websocket.Conn
	out       chan []byte
	quit      chan struct{}
	once      sync.Once
	code      websocket.StatusCode
	reason    string
	closeLock sync.Mutex
}

func newConn(c *websocket.Conn) *conn {
	return &conn{ws: c, out: make(chan []byte, sendBuffer), quit: make(chan struct{}), code: websocket.StatusNormalClosure}
}

func (c *conn) Send(b []byte) bool {
	select {
	case <-c.quit:
		return true // closing; drop silently
	default:
	}
	select {
	case c.out <- b:
		return true
	default:
		return false
	}
}

func (c *conn) Close(code int, reason string) {
	c.once.Do(func() {
		c.closeLock.Lock()
		c.code, c.reason = websocket.StatusCode(code), reason
		c.closeLock.Unlock()
		close(c.quit)
	})
}

func (c *conn) writeLoop() {
	for {
		select {
		case b := <-c.out:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := c.ws.Write(ctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				c.Close(int(websocket.StatusGoingAway), "write failed")
			}
		case <-c.quit:
			c.closeLock.Lock()
			code, reason := c.code, c.reason
			c.closeLock.Unlock()
			_ = c.ws.Close(code, reason)
			return
		}
	}
}

// Handler serves GET /ws/rooms/{code}?pid&name&color&create=1.
func Handler(hub *room.Hub, origins []string, log *slog.Logger, code func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: origins})
		if err != nil {
			return // Accept already wrote the HTTP error (403 for a bad origin)
		}
		c.SetReadLimit(MaxFrame)
		q := r.URL.Query()
		pid := q.Get("pid")
		m := game.Member{ID: pid, Name: game.CleanName(q.Get("name")), Color: game.SafeColor(q.Get("color"))}
		if !pidRe.MatchString(pid) || m.Name == "" {
			_ = c.Close(websocket.StatusPolicyViolation, "bad pid or name")
			return
		}
		create := q.Get("create") == "1"
		roomCode := code(r)

		wc := newConn(c)
		go wc.writeLoop()

		var rm *room.Room
		for attempt := 0; attempt < 2; attempt++ {
			rm, err = hub.Get(roomCode, create)
			if err == nil {
				err = rm.Join(pid, m, wc)
			}
			if !errors.Is(err, room.ErrRoomGone) {
				break
			}
		}
		switch {
		case errors.Is(err, room.ErrRoomNotFound), errors.Is(err, room.ErrRoomGone):
			wc.Close(room.CloseNotFound, "room not found")
			return
		case errors.Is(err, room.ErrRoomFull), errors.Is(err, room.ErrTooManyRooms):
			wc.Close(room.CloseFull, "full")
			return
		case err != nil:
			log.Error("join failed", "err", err)
			wc.Close(int(websocket.StatusInternalError), "join failed")
			return
		}

		defer rm.Leave(pid, wc)
		for {
			ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
			typ, b, err := c.Read(ctx)
			cancel()
			if err != nil {
				wc.Close(int(websocket.StatusGoingAway), "")
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var msg room.Inbound
			if json.Unmarshal(b, &msg) != nil {
				continue
			}
			rm.Handle(pid, msg)
		}
	}
}
