package room

import (
	"encoding/json"
	"errors"
)

// Conn is one player's socket as the room sees it.
type Conn interface {
	// Send queues a frame without blocking; false means the client is too slow.
	Send(frame []byte) bool
	Close(code int, reason string)
}

// Inbound is a client frame: {"type": "...", "data": ...}.
type Inbound struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// WebSocket close codes the web client understands.
const (
	CloseNotFound = 4404
	CloseReplaced = 4409
	CloseFull     = 4429
	CloseSlow     = 4408
)

var (
	ErrRoomNotFound = errors.New("room not found")
	ErrTooManyRooms = errors.New("too many rooms")
	ErrRoomFull     = errors.New("room full")
	ErrRoomGone     = errors.New("room closed")
)

const (
	msgNotHost  = "เฉพาะหัวห้องเท่านั้น"
	msgBadDeck  = "เปิดชุดคำถามไม่สำเร็จ"
	msgLoadFail = "โหลดเพลงไม่สำเร็จ"
)

func encode(typ string, data any) []byte {
	b, _ := json.Marshal(struct {
		Type string `json:"type"`
		Data any    `json:"data,omitempty"`
	}{typ, data})
	return b
}
