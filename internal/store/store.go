// Package store saves finished games and serves the leaderboard.
package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
	_ "time/tzdata" // Asia/Bangkok inside distroless images

	"github.com/bunjonglak123/songquiz-server/internal/game"
)

type PlayerResult struct {
	PlayerID, Name       string
	Score, Rank, Correct int
}

type GameRecord struct {
	ID, RoomCode, SourceKind string
	Rounds                   int
	StartedAt, EndedAt       time.Time
	Players                  []PlayerResult
}

type Entry struct {
	Name     string    `json:"name"`
	Score    int       `json:"score"`
	Correct  int       `json:"correct"`
	Rounds   int       `json:"rounds"`
	PlayedAt time.Time `json:"playedAt"`
}

type Store interface {
	SaveGame(ctx context.Context, r GameRecord) error
	// Top returns the best single-game scores. period is "week" or "all".
	Top(ctx context.Context, period string, limit int, now time.Time) ([]Entry, error)
	Close()
}

var ErrBadPeriod = errors.New("period must be week or all")

// Leaderboard eligibility: no solo farming, no 3-question sprints.
const (
	MinPlayers = 2
	MinRounds  = 5
)

var bangkok = func() *time.Location {
	l, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		return time.FixedZone("ICT", 7*3600)
	}
	return l
}()

// WeekStart is Monday 00:00 Bangkok time of the week containing now.
func WeekStart(now time.Time) time.Time {
	t := now.In(bangkok)
	days := (int(t.Weekday()) + 6) % 7 // Monday = 0
	y, m, d := t.Date()
	return time.Date(y, m, d-days, 0, 0, 0, 0, bangkok)
}

// since returns the lower bound for a period (zero time = no bound).
func since(period string, now time.Time) (time.Time, error) {
	switch period {
	case "all":
		return time.Time{}, nil
	case "week":
		return WeekStart(now), nil
	}
	return time.Time{}, ErrBadPeriod
}

// NewID returns a random UUIDv4 string.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func RecordFrom(r game.FinalResult, code string) GameRecord {
	ranks := game.RankOf(r.State.Players)
	rec := GameRecord{
		ID: NewID(), RoomCode: code, SourceKind: r.State.Settings.SourceKind(),
		Rounds: r.State.TotalRounds, StartedAt: r.StartedAt, EndedAt: r.EndedAt,
	}
	for id, p := range r.State.Players {
		rec.Players = append(rec.Players, PlayerResult{PlayerID: id, Name: p.Name, Score: p.Score, Rank: ranks[id], Correct: r.Correct[id]})
	}
	sort.Slice(rec.Players, func(i, j int) bool { return rec.Players[i].Rank < rec.Players[j].Rank })
	return rec
}

// Memory is the store used when DATABASE_URL is empty (dev and tests).
type Memory struct {
	mu    sync.Mutex
	games []GameRecord
}

func NewMemory() *Memory { return &Memory{} }

func (m *Memory) SaveGame(_ context.Context, r GameRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.games = append(m.games, r)
	return nil
}

func (m *Memory) Top(_ context.Context, period string, limit int, now time.Time) ([]Entry, error) {
	from, err := since(period, now)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Entry{}
	for _, g := range m.games {
		if len(g.Players) < MinPlayers || g.Rounds < MinRounds || g.EndedAt.Before(from) {
			continue
		}
		for _, p := range g.Players {
			out = append(out, Entry{Name: p.Name, Score: p.Score, Correct: p.Correct, Rounds: g.Rounds, PlayedAt: g.EndedAt})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].PlayedAt.Before(out[j].PlayedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) Close() {}
