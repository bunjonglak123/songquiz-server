package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/bunjonglak123/songquiz-server/internal/game"
)

var bkk = mustLoc("Asia/Bangkok")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func rec(code string, rounds int, ended time.Time, scores ...int) GameRecord {
	r := GameRecord{ID: NewID(), RoomCode: code, SourceKind: "chart", Rounds: rounds, StartedAt: ended.Add(-5 * time.Minute), EndedAt: ended}
	for i, s := range scores {
		r.Players = append(r.Players, PlayerResult{PlayerID: fmt.Sprintf("%s-p%d", code, i), Name: fmt.Sprintf("%s ผู้เล่น %d", code, i), Score: s, Rank: i + 1, Correct: i})
	}
	return r
}

// suite runs against every Store implementation.
func suite(t *testing.T, mk func(t *testing.T) Store) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, bkk) // Wednesday

	t.Run("TopFiltersIneligible", func(t *testing.T) {
		s := mk(t)
		must(t, s.SaveGame(ctx, rec("SOLO", 10, now.Add(-time.Hour), 9000)))
		must(t, s.SaveGame(ctx, rec("SHORT", 3, now.Add(-time.Hour), 8000, 100)))
		must(t, s.SaveGame(ctx, rec("OK", 5, now.Add(-time.Hour), 3000, 200)))
		top, err := s.Top(ctx, "all", 20, now)
		must(t, err)
		if len(top) != 2 || top[0].Score != 3000 || top[1].Score != 200 {
			t.Fatalf("top %+v", top)
		}
		if top[0].Rounds != 5 || top[0].Name != "OK ผู้เล่น 0" {
			t.Fatalf("entry %+v", top[0])
		}
	})

	t.Run("TopWeek", func(t *testing.T) {
		s := mk(t)
		sunday := time.Date(2026, 11, 15, 23, 59, 0, 0, bkk)
		monday := time.Date(2026, 11, 16, 0, 0, 0, 0, bkk)
		must(t, s.SaveGame(ctx, rec("OLD", 5, sunday, 5000, 1)))
		must(t, s.SaveGame(ctx, rec("NEW", 5, monday, 4000, 1)))
		week, err := s.Top(ctx, "week", 20, now)
		must(t, err)
		if len(week) != 2 || week[0].Score != 4000 {
			t.Fatalf("week %+v", week)
		}
		all, _ := s.Top(ctx, "all", 20, now)
		if len(all) != 4 || all[0].Score != 5000 {
			t.Fatalf("all %+v", all)
		}
	})

	t.Run("TopOrderAndLimit", func(t *testing.T) {
		s := mk(t)
		must(t, s.SaveGame(ctx, rec("A", 5, now.Add(-2*time.Hour), 700, 700)))
		must(t, s.SaveGame(ctx, rec("B", 5, now.Add(-time.Hour), 900, 100)))
		top, err := s.Top(ctx, "all", 3, now)
		must(t, err)
		if len(top) != 3 || top[0].Score != 900 || top[1].Score != 700 || top[2].Score != 700 {
			t.Fatalf("top %+v", top)
		}
	})

	t.Run("BadPeriod", func(t *testing.T) {
		if _, err := mk(t).Top(ctx, "month", 20, now); err != ErrBadPeriod {
			t.Fatalf("err %v", err)
		}
	})
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestMemory(t *testing.T) {
	suite(t, func(t *testing.T) Store { return NewMemory() })
}

func TestPG(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	suite(t, func(t *testing.T) Store {
		s, err := NewPG(ctx, url)
		must(t, err)
		_, err = s.pool.Exec(ctx, "TRUNCATE game_players, games")
		must(t, err)
		t.Cleanup(s.Close)
		return s
	})
	t.Run("MigrateIdempotent", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			s, err := NewPG(ctx, url)
			must(t, err)
			s.Close()
		}
	})
}

func TestWeekStart(t *testing.T) {
	wed := time.Date(2026, 11, 18, 10, 0, 0, 0, bkk)
	want := time.Date(2026, 11, 16, 0, 0, 0, 0, bkk)
	if got := WeekStart(wed); !got.Equal(want) {
		t.Fatalf("got %v", got)
	}
	if got := WeekStart(want); !got.Equal(want) {
		t.Fatalf("monday 00:00 → %v", got)
	}
	sunUTC := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC) // Monday 01:00 in Bangkok
	if got := WeekStart(sunUTC); !got.Equal(time.Date(2026, 11, 23, 0, 0, 0, 0, bkk)) {
		t.Fatalf("utc input → %v", got)
	}
}

func TestRecordFrom(t *testing.T) {
	st := game.GameState{
		TotalRounds: 5,
		Settings:    game.DefaultSettings,
		Players: map[string]game.PlayerState{
			"a": {ID: "a", Name: "เอ", Score: 900},
			"b": {ID: "b", Name: "บี", Score: 900},
			"c": {ID: "c", Name: "ซี", Score: 100},
		},
	}
	end := time.Unix(1_700_000_000, 0)
	r := RecordFrom(game.FinalResult{State: st, Correct: map[string]int{"a": 3, "c": 1}, StartedAt: end.Add(-time.Minute), EndedAt: end}, "ROOM1")
	if r.RoomCode != "ROOM1" || r.Rounds != 5 || r.SourceKind != "chart" || len(r.Players) != 3 || len(r.ID) != 36 {
		t.Fatalf("%+v", r)
	}
	by := map[string]PlayerResult{}
	for _, p := range r.Players {
		by[p.PlayerID] = p
	}
	if by["a"].Rank != 1 || by["b"].Rank != 1 || by["c"].Rank != 3 || by["a"].Correct != 3 || by["b"].Correct != 0 {
		t.Fatalf("players %+v", by)
	}
}
