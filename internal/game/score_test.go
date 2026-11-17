package game

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestScoreFor(t *testing.T) {
	cases := []struct {
		name            string
		correct         bool
		ms, dur, streak int
		want            int
	}{
		{"wrong", false, 0, 15000, 1, 0},
		{"instant", true, 0, 15000, 1, 1000},
		{"at deadline", true, 15000, 15000, 1, 500},
		{"half time", true, 7500, 15000, 1, 750},
		{"streak 4", true, 0, 15000, 4, 1300},
		{"streak capped", true, 0, 15000, 9, 1300},
		{"negative ms clamps", true, -50, 15000, 1, 1000},
	}
	for _, c := range cases {
		if got := ScoreFor(c.correct, c.ms, c.dur, c.streak); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestRankOfTies(t *testing.T) {
	r := RankOf(map[string]PlayerState{
		"a": {ID: "a", Score: 300}, "b": {ID: "b", Score: 300}, "c": {ID: "c", Score: 100},
	})
	if r["a"] != 1 || r["b"] != 1 || r["c"] != 3 {
		t.Fatalf("ranks %v", r)
	}
}

func TestSafeColor(t *testing.T) {
	if SafeColor("#a1B2c3") != "#a1B2c3" {
		t.Fatal("valid colour dropped")
	}
	for _, bad := range []string{"red", "#fff", "#1234567", "#12345g"} {
		if SafeColor(bad) != "" {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCleanName(t *testing.T) {
	n := CleanName("  " + strings.Repeat("ก", 17) + "  ")
	if utf8.RuneCountInString(n) != 16 {
		t.Fatalf("got %d runes", utf8.RuneCountInString(n))
	}
	if CleanName("  แบงค์ ") != "แบงค์" {
		t.Fatal("not trimmed")
	}
}

func TestSettingsClamp(t *testing.T) {
	lo := GameSettings{Rounds: 1, RoundSeconds: 2, ListenSeconds: -1}.Clamp()
	if lo.Rounds != 3 || lo.RoundSeconds != 5 || lo.ListenSeconds != 0 {
		t.Fatalf("low clamp %+v", lo)
	}
	hi := GameSettings{Rounds: 99, RoundSeconds: 40, ListenSeconds: 9}.Clamp()
	if hi.Rounds != 30 || hi.RoundSeconds != 25 || hi.ListenSeconds != 5 {
		t.Fatalf("high clamp %+v", hi)
	}
	if len(lo.Source) == 0 {
		t.Fatal("empty source should default")
	}
}

func TestSourceKind(t *testing.T) {
	if k := DefaultSettings.SourceKind(); k != "chart" {
		t.Fatalf("got %q", k)
	}
}

func TestGameStateJSON(t *testing.T) {
	b, err := json.Marshal(GameState{Version: 1, HostID: "h", Phase: PhaseLobby, Settings: DefaultSettings, Players: map[string]PlayerState{}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"answered":[]`, `"hostId":"h"`, `"totalRounds":0`, `"phase":"lobby"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	if strings.Contains(s, "epoch") || strings.Contains(s, "deckId") {
		t.Errorf("takeover fields present: %s", s)
	}
}
