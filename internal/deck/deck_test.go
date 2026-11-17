package deck

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/deck.txt")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestOpenTSFixture(t *testing.T) {
	id, qs, err := Open(Key("test-secret"), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || len(qs) != 2 || qs[0].ID != "q0" || qs[1].ID != "q1" || qs[0].Answer != 2 {
		t.Fatalf("id=%q qs=%+v", id, qs)
	}
	if qs[0].Choices[3].Title != "ตัวเลือก 0-3" || qs[0].StartAt != 5 {
		t.Fatalf("fields lost: %+v", qs[0])
	}
}

func TestWrongSecret(t *testing.T) {
	if _, _, err := Open(Key("other"), fixture(t)); !errors.Is(err, ErrBadDeck) {
		t.Fatalf("err %v", err)
	}
}

func TestTampered(t *testing.T) {
	d := []byte(fixture(t))
	i := len(d) - 5
	if d[i] == 'A' {
		d[i] = 'B'
	} else {
		d[i] = 'A'
	}
	if _, _, err := Open(Key("test-secret"), string(d)); !errors.Is(err, ErrBadDeck) {
		t.Fatalf("err %v", err)
	}
}

func TestTooLong(t *testing.T) {
	if _, _, err := Open(Key("test-secret"), strings.Repeat("a", MaxDeckChars+1)); !errors.Is(err, ErrBadDeck) {
		t.Fatalf("err %v", err)
	}
}

func TestMalformed(t *testing.T) {
	for _, d := range []string{"", "a.b.c", "a.!!.c.d", "a.b.c.d.e"} {
		if _, _, err := Open(Key("test-secret"), d); !errors.Is(err, ErrBadDeck) {
			t.Errorf("%q: err %v", d, err)
		}
	}
}
