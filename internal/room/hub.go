package room

import (
	"strings"
	"sync"
	"time"

	"github.com/bunjonglak123/songquiz-server/internal/clock"
	"github.com/bunjonglak123/songquiz-server/internal/game"
)

// Saver stores a finished game. Called on its own goroutine.
type Saver func(res game.FinalResult, roomCode string)

type Config struct {
	MaxRooms, MaxPlayers        int
	Grace, EmptyTTL, BatchEvery time.Duration
	DeckKey                     []byte
}

func DefaultConfig(deckKey []byte) Config {
	return Config{
		MaxRooms: 500, MaxPlayers: game.MaxPlayers,
		Grace: 3 * time.Second, EmptyTTL: 2 * time.Minute, BatchEvery: 300 * time.Millisecond,
		DeckKey: deckKey,
	}
}

type Hub struct {
	cfg  Config
	clk  clock.Clock
	save Saver

	mu    sync.Mutex
	rooms map[string]*Room
}

func NewHub(cfg Config, clk clock.Clock, save Saver) *Hub {
	return &Hub{cfg: cfg, clk: clk, save: save, rooms: map[string]*Room{}}
}

// NormalizeCode keeps A–Z and 0–9, upper-cased, at most 8 characters.
func NormalizeCode(s string) string {
	var b strings.Builder
	for _, c := range strings.ToUpper(s) {
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
			if b.Len() == 8 {
				break
			}
		}
	}
	return b.String()
}

func (h *Hub) Get(code string, create bool) (*Room, error) {
	code = NormalizeCode(code)
	if code == "" {
		return nil, ErrRoomNotFound
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.rooms[code]; r != nil {
		return r, nil
	}
	if !create {
		return nil, ErrRoomNotFound
	}
	if len(h.rooms) >= h.cfg.MaxRooms {
		return nil, ErrTooManyRooms
	}
	r := newRoom(code, h)
	h.rooms[code] = r
	return r, nil
}

func (h *Hub) remove(code string, r *Room) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[code] == r {
		delete(h.rooms, code)
	}
}

func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.rooms)
}

// Close shuts every room down (server exit).
func (h *Hub) Close() {
	h.mu.Lock()
	rooms := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.rooms = map[string]*Room{}
	h.mu.Unlock()
	for _, r := range rooms {
		r.do(r.shutdown)
	}
}
