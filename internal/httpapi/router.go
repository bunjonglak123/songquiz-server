// Package httpapi wires the HTTP routes.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bunjonglak123/songquiz-server/internal/clock"
	"github.com/bunjonglak123/songquiz-server/internal/room"
	"github.com/bunjonglak123/songquiz-server/internal/store"
	"github.com/bunjonglak123/songquiz-server/internal/ws"
)

type Leaderboard interface {
	Top(ctx context.Context, period string, limit int, now time.Time) ([]store.Entry, error)
}

type Deps struct {
	Hub            *room.Hub
	Board          Leaderboard
	AllowedOrigins []string // host patterns, e.g. "thai-song-quiz-fawn.vercel.app", "localhost:3000"
	Clock          clock.Clock
	Log            *slog.Logger
}

const cacheFor = 30 * time.Second

func NewRouter(d Deps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	lb := &leaderboard{d: d, cache: map[string]cached{}}
	r.GET("/api/leaderboard", lb.serve)

	wsHandler := ws.Handler(d.Hub, d.AllowedOrigins, d.Log, func(req *http.Request) string {
		return strings.TrimPrefix(req.URL.Path, "/ws/rooms/")
	})
	r.GET("/ws/rooms/:code", func(c *gin.Context) { wsHandler(c.Writer, c.Request) })
	return r
}

type cached struct {
	at      time.Time
	entries []store.Entry
}

type leaderboard struct {
	d     Deps
	mu    sync.Mutex
	cache map[string]cached
}

func (l *leaderboard) serve(c *gin.Context) {
	if o := c.GetHeader("Origin"); o != "" && originAllowed(o, l.d.AllowedOrigins) {
		c.Header("Access-Control-Allow-Origin", o)
		c.Header("Vary", "Origin")
	}
	period := c.DefaultQuery("period", "all")
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if (period != "week" && period != "all") || err != nil || limit < 1 || limit > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "period=week|all, limit=1..50"})
		return
	}
	key := period + ":" + strconv.Itoa(limit)
	now := l.d.Clock.Now()
	l.mu.Lock()
	hit, ok := l.cache[key]
	l.mu.Unlock()
	if ok && now.Sub(hit.at) < cacheFor {
		c.JSON(http.StatusOK, hit.entries)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	entries, err := l.d.Board.Top(ctx, period, limit, now)
	if err != nil {
		l.d.Log.Error("leaderboard", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "โหลดอันดับไม่สำเร็จ"})
		return
	}
	l.mu.Lock()
	l.cache[key] = cached{at: now, entries: entries}
	l.mu.Unlock()
	c.JSON(http.StatusOK, entries)
}

// originAllowed matches the Origin's host against the patterns (path.Match syntax).
func originAllowed(origin string, patterns []string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	for _, p := range patterns {
		if ok, _ := path.Match(strings.ToLower(p), strings.ToLower(u.Host)); ok {
			return true
		}
	}
	return false
}
