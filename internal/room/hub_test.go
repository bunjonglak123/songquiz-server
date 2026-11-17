package room

import (
	"fmt"
	"testing"
	"time"

	"github.com/bunjonglak123/songquiz-server/internal/clock"
	"github.com/bunjonglak123/songquiz-server/internal/deck"
)

func TestGetMissingWithoutCreate(t *testing.T) {
	e := newEnv(t)
	if _, err := e.hub.Get("NOPE", false); err != ErrRoomNotFound {
		t.Fatalf("err %v", err)
	}
	if _, err := e.hub.Get("!!", true); err != ErrRoomNotFound {
		t.Fatalf("empty code: err %v", err)
	}
}

func TestTooManyRooms(t *testing.T) {
	cfg := DefaultConfig(deck.Key("x"))
	cfg.MaxRooms = 2
	h := NewHub(cfg, clock.NewFake(time.Unix(0, 0)), nil)
	defer h.Close()
	for i := 0; i < 2; i++ {
		if _, err := h.Get(fmt.Sprintf("R%d", i), true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Get("R9", true); err != ErrTooManyRooms {
		t.Fatalf("err %v", err)
	}
}

func TestEmptyRoomExpires(t *testing.T) {
	e := newEnv(t)
	r := e.room(true)
	c := e.join(r, "host")
	r.Leave("host", c)
	e.advance(3*time.Second + time.Minute)
	if e.hub.Count() != 1 {
		t.Fatal("expired too early")
	}
	e.advance(time.Minute)
	if e.hub.Count() != 0 {
		t.Fatalf("count %d after 2 min empty", e.hub.Count())
	}
	if err := r.Join("x", gameMember("x"), &fakeConn{}); err != ErrRoomGone {
		t.Fatalf("join on removed room: %v", err)
	}
}

func TestUnjoinedRoomExpires(t *testing.T) {
	e := newEnv(t)
	e.room(true)
	e.advance(2*time.Minute + time.Second)
	if e.hub.Count() != 0 {
		t.Fatal("room nobody joined should expire")
	}
}

func TestNormalizesCode(t *testing.T) {
	e := newEnv(t)
	a, _ := e.hub.Get("ab-c1", true)
	b, err := e.hub.Get("ABC1", false)
	if err != nil || a != b {
		t.Fatal("codes should normalize to the same room")
	}
	if NormalizeCode("abcdefghijk") != "ABCDEFGH" {
		t.Fatal("max 8 chars")
	}
}
