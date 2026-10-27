package webrtc

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/rs/zerolog"

	"github.com/m1k1o/neko/server/internal/config"
	"github.com/m1k1o/neko/server/internal/session"
	"github.com/m1k1o/neko/server/pkg/types"
)

type recordingDesktop struct {
	types.DesktopManager
	calls []string
}

func (d *recordingDesktop) Move(x, y int) { d.calls = append(d.calls, "Move") }
func (d *recordingDesktop) KeyDown(code uint32) error {
	d.calls = append(d.calls, "KeyDown")
	return nil
}
func (d *recordingDesktop) ButtonDown(code uint32) error {
	d.calls = append(d.calls, "ButtonDown")
	return nil
}

func legacyPayload(t *testing.T, payload any) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	if err := binary.Write(buffer, binary.LittleEndian, payload); err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	return buffer.Bytes()
}

func TestHandleLegacyInput(t *testing.T) {
	inputs := map[string]any{
		"move":        PayloadMove{PayloadHeader: PayloadHeader{Event: OP_MOVE, Length: 4}, X: 10, Y: 10},
		"key down":    PayloadKey{PayloadHeader: PayloadHeader{Event: OP_KEY_DOWN, Length: 8}, Key: 0x61},
		"button down": PayloadKey{PayloadHeader: PayloadHeader{Event: OP_KEY_DOWN, Length: 8}, Key: 1},
	}

	for _, canHost := range []bool{false, true} {
		for name, input := range inputs {
			t.Run(name, func(t *testing.T) {
				sessions := session.New(&config.Session{ImplicitHosting: true})
				sess, _, err := sessions.Create("member", types.MemberProfile{CanHost: canHost})
				if err != nil {
					t.Fatalf("create session: %v", err)
				}
				desktop := &recordingDesktop{}
				manager := &WebRTCManagerCtx{desktop: desktop}

				if err := manager.handleLegacy(zerolog.Nop(), legacyPayload(t, input), sess); err != nil {
					t.Fatalf("handleLegacy: %v", err)
				}

				if canHost && len(desktop.calls) != 1 {
					t.Errorf("can host: desktop calls = %v, want one", desktop.calls)
				}
				if !canHost && len(desktop.calls) != 0 {
					t.Errorf("cannot host: desktop calls = %v, want none", desktop.calls)
				}
			})
		}
	}
}
