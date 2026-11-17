package clock

import (
	"testing"
	"time"
)

func TestFakeAdvanceRunsDueInOrder(t *testing.T) {
	f := NewFake(time.Unix(0, 0))
	var got []int
	f.AfterFunc(2*time.Second, func() { got = append(got, 2) })
	f.AfterFunc(1*time.Second, func() { got = append(got, 1) })
	f.AfterFunc(5*time.Second, func() { got = append(got, 5) })
	f.Advance(3 * time.Second)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("got %v", got)
	}
	if !f.Now().Equal(time.Unix(3, 0)) {
		t.Fatalf("now %v", f.Now())
	}
}

func TestFakeStopPreventsRun(t *testing.T) {
	f := NewFake(time.Unix(0, 0))
	ran := false
	tm := f.AfterFunc(time.Second, func() { ran = true })
	if !tm.Stop() {
		t.Fatal("Stop should report true for a pending timer")
	}
	f.Advance(2 * time.Second)
	if ran {
		t.Fatal("stopped timer ran")
	}
	if tm.Stop() {
		t.Fatal("second Stop should report false")
	}
}

func TestFakeNestedSchedule(t *testing.T) {
	f := NewFake(time.Unix(0, 0))
	var at []time.Duration
	f.AfterFunc(time.Second, func() {
		at = append(at, f.Now().Sub(time.Unix(0, 0)))
		f.AfterFunc(time.Second, func() { at = append(at, f.Now().Sub(time.Unix(0, 0))) })
	})
	f.Advance(2 * time.Second)
	if len(at) != 2 || at[0] != time.Second || at[1] != 2*time.Second {
		t.Fatalf("got %v", at)
	}
}

func TestRealAfterFunc(t *testing.T) {
	done := make(chan struct{})
	Real().AfterFunc(time.Millisecond, func() { close(done) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("real timer did not fire")
	}
}
