// Package clock lets game timers run on real time in production and on a
// fake, manually advanced clock in tests (no sleeps).
package clock

import (
	"sort"
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

type Timer interface {
	// Stop reports whether it prevented the callback from running.
	Stop() bool
}

type real struct{}

func Real() Clock { return real{} }

func (real) Now() time.Time { return time.Now() }

func (real) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// Fake runs callbacks synchronously inside Advance, in due order.
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	seq    int
	timers []*fakeTimer
}

type fakeTimer struct {
	f    *Fake
	at   time.Time
	seq  int
	fn   func()
	done bool
}

func NewFake(start time.Time) *Fake { return &Fake{now: start} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) AfterFunc(d time.Duration, fn func()) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	t := &fakeTimer{f: f, at: f.now.Add(d), seq: f.seq, fn: fn}
	f.timers = append(f.timers, t)
	return t
}

func (t *fakeTimer) Stop() bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	if t.done {
		return false
	}
	t.done = true
	return true
}

// Advance moves time forward by d, running every timer that comes due,
// including timers scheduled by callbacks within the window.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	end := f.now.Add(d)
	f.mu.Unlock()
	for {
		f.mu.Lock()
		next := f.nextDue(end)
		if next == nil {
			f.now = end
			f.mu.Unlock()
			return
		}
		next.done = true
		f.now = next.at
		f.mu.Unlock()
		next.fn()
	}
}

func (f *Fake) nextDue(end time.Time) *fakeTimer {
	live := f.timers[:0]
	for _, t := range f.timers {
		if !t.done {
			live = append(live, t)
		}
	}
	f.timers = live
	sort.Slice(live, func(i, j int) bool {
		if !live[i].at.Equal(live[j].at) {
			return live[i].at.Before(live[j].at)
		}
		return live[i].seq < live[j].seq
	})
	if len(live) == 0 || live[0].at.After(end) {
		return nil
	}
	return live[0]
}
