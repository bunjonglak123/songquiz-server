// Package deck opens question decks sealed by the web app (src/lib/deck.ts).
// Format: deckId.iv.tag.body, base64url without padding, AES-256-GCM with
// deckId as additional data. Only the server and Next.js share the secret.
package deck

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/bunjonglak123/songquiz-server/internal/game"
)

const MaxDeckChars = 400_000

var ErrBadDeck = errors.New("bad deck")

// Key derives the AES key exactly like the web app: sha256("tsq-deck-v1:" + secret).
func Key(secret string) []byte {
	k := sha256.Sum256([]byte("tsq-deck-v1:" + secret))
	return k[:]
}

func Open(key []byte, deck string) (string, []game.HostQuestion, error) {
	if len(deck) > MaxDeckChars {
		return "", nil, ErrBadDeck
	}
	parts := strings.Split(deck, ".")
	if len(parts) != 4 || parts[0] == "" {
		return "", nil, ErrBadDeck
	}
	dec := base64.RawURLEncoding
	iv, err1 := dec.DecodeString(parts[1])
	tag, err2 := dec.DecodeString(parts[2])
	body, err3 := dec.DecodeString(parts[3])
	if err1 != nil || err2 != nil || err3 != nil || len(iv) != 12 {
		return "", nil, ErrBadDeck
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", nil, ErrBadDeck
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", nil, ErrBadDeck
	}
	plain, err := gcm.Open(nil, iv, append(body, tag...), []byte(parts[0]))
	if err != nil {
		return "", nil, ErrBadDeck
	}
	var qs []game.HostQuestion
	if err := json.Unmarshal(plain, &qs); err != nil {
		return "", nil, ErrBadDeck
	}
	return parts[0], qs, nil
}
