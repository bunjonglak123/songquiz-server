package game

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// ScoreFor: 500 for a correct answer, up to 500 more for speed, up to 300 streak bonus.
func ScoreFor(correct bool, ms, durationMs, streak int) int {
	if !correct {
		return 0
	}
	t := math.Min(math.Max(float64(ms)/float64(durationMs), 0), 1)
	speed := int(math.Round(500 * (1 - t)))
	bonus := clamp(streak-1, 0, 3) * 100
	return 500 + speed + bonus
}

// RankOf gives 1-based competition ranks by score; ties share a rank.
func RankOf(players map[string]PlayerState) map[string]int {
	list := make([]PlayerState, 0, len(players))
	for _, p := range players {
		list = append(list, p)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Score != list[j].Score {
			return list[i].Score > list[j].Score
		}
		return list[i].ID < list[j].ID
	})
	out := make(map[string]int, len(list))
	for i, p := range list {
		if i > 0 && list[i-1].Score == p.Score {
			out[p.ID] = out[list[i-1].ID]
		} else {
			out[p.ID] = i + 1
		}
	}
	return out
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// SafeColor accepts only #rrggbb (it ends up in a style attribute).
func SafeColor(c string) string {
	if colorRe.MatchString(c) {
		return c
	}
	return ""
}

// CleanName trims spaces and caps the name at 16 characters.
func CleanName(n string) string {
	r := []rune(strings.TrimSpace(n))
	if len(r) > 16 {
		r = r[:16]
	}
	return strings.TrimSpace(string(r))
}
