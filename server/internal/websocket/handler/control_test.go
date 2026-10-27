package handler

import (
	"errors"
	"testing"

	"github.com/m1k1o/neko/server/internal/config"
	"github.com/m1k1o/neko/server/internal/session"
	"github.com/m1k1o/neko/server/pkg/types"
	"github.com/m1k1o/neko/server/pkg/types/message"
)

// recordingDesktop records the desktop calls a handler makes. Methods a test
// does not expect are left to the embedded nil interface and panic if called.
type recordingDesktop struct {
	types.DesktopManager
	calls []string
}

func (d *recordingDesktop) Move(x, y int) { d.calls = append(d.calls, "Move") }
func (d *recordingDesktop) KeyPress(codes ...uint32) error {
	d.calls = append(d.calls, "KeyPress")
	return nil
}
func (d *recordingDesktop) ResetKeys() { d.calls = append(d.calls, "ResetKeys") }
func (d *recordingDesktop) SetKeyboardMap(types.KeyboardMap) error {
	d.calls = append(d.calls, "SetKeyboardMap")
	return nil
}
func (d *recordingDesktop) SetKeyboardModifiers(types.KeyboardModifiers) {
	d.calls = append(d.calls, "SetKeyboardModifiers")
}
func (d *recordingDesktop) ClipboardSetText(types.ClipboardText) error {
	d.calls = append(d.calls, "ClipboardSetText")
	return nil
}

type recordingWebRTC struct {
	types.WebRTCManager
}

func (recordingWebRTC) SetCursorPosition(x, y int) {}

var (
	hostProfile = types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true, CanHost: true, CanAccessClipboard: true}
	viewProfile = types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}
)

func newTestHandler(t *testing.T, profile types.MemberProfile) (*MessageHandlerCtx, *recordingDesktop, types.Session) {
	t.Helper()
	sessions := session.New(&config.Session{ImplicitHosting: true})
	sess, _, err := sessions.Create("member", profile)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	desktop := &recordingDesktop{}
	return New(sessions, desktop, nil, recordingWebRTC{}), desktop, sess
}

func TestNonHostSessionsCannotControl(t *testing.T) {
	actions := map[string]func(h *MessageHandlerCtx, s types.Session) error{
		"control request": func(h *MessageHandlerCtx, s types.Session) error { return h.controlRequest(s) },
		"control release": func(h *MessageHandlerCtx, s types.Session) error { return h.controlRelease(s) },
		"move": func(h *MessageHandlerCtx, s types.Session) error {
			return h.controlMove(s, &message.ControlPos{X: 1, Y: 1})
		},
		"key press": func(h *MessageHandlerCtx, s types.Session) error {
			return h.controlKeyPress(s, &message.ControlKey{Keysym: 0x61})
		},
		"clipboard set": func(h *MessageHandlerCtx, s types.Session) error {
			return h.clipboardSet(s, &message.ClipboardData{Text: "text"})
		},
		"keyboard map": func(h *MessageHandlerCtx, s types.Session) error {
			return h.keyboardMap(s, &message.KeyboardMap{KeyboardMap: types.KeyboardMap{Layout: "de"}})
		},
		"keyboard modifiers": func(h *MessageHandlerCtx, s types.Session) error {
			capsLock := true
			return h.keyboardModifiers(s, &message.KeyboardModifiers{KeyboardModifiers: types.KeyboardModifiers{CapsLock: &capsLock}})
		},
	}

	for name, action := range actions {
		t.Run(name, func(t *testing.T) {
			h, desktop, sess := newTestHandler(t, viewProfile)
			if err := action(h, sess); err == nil {
				t.Fatal("expected the action to be refused")
			}
			if len(desktop.calls) != 0 {
				t.Errorf("desktop calls = %v, want none", desktop.calls)
			}
			if sess.IsHost() {
				t.Error("session became host")
			}
		})
	}
}

func TestHostSessionsCanControl(t *testing.T) {
	tests := []struct {
		name   string
		action func(h *MessageHandlerCtx, s types.Session) error
		want   string
	}{
		{"move", func(h *MessageHandlerCtx, s types.Session) error {
			return h.controlMove(s, &message.ControlPos{X: 1, Y: 1})
		}, "Move"},
		{"key press", func(h *MessageHandlerCtx, s types.Session) error {
			return h.controlKeyPress(s, &message.ControlKey{Keysym: 0x61})
		}, "KeyPress"},
		{"clipboard set", func(h *MessageHandlerCtx, s types.Session) error {
			return h.clipboardSet(s, &message.ClipboardData{Text: "text"})
		}, "ClipboardSetText"},
		{"keyboard map", func(h *MessageHandlerCtx, s types.Session) error {
			return h.keyboardMap(s, &message.KeyboardMap{KeyboardMap: types.KeyboardMap{Layout: "de"}})
		}, "SetKeyboardMap"},
		{"keyboard modifiers", func(h *MessageHandlerCtx, s types.Session) error {
			capsLock := true
			return h.keyboardModifiers(s, &message.KeyboardModifiers{KeyboardModifiers: types.KeyboardModifiers{CapsLock: &capsLock}})
		}, "SetKeyboardModifiers"},
		{"control release", func(h *MessageHandlerCtx, s types.Session) error { return h.controlRelease(s) }, "ResetKeys"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, desktop, sess := newTestHandler(t, hostProfile)
			if err := tt.action(h, sess); err != nil {
				t.Fatalf("action failed: %v", err)
			}
			if len(desktop.calls) == 0 || desktop.calls[len(desktop.calls)-1] != tt.want {
				t.Errorf("desktop calls = %v, want last call %s", desktop.calls, tt.want)
			}
		})
	}
}

func TestControlRequestSetsHostForHostProfile(t *testing.T) {
	h, _, sess := newTestHandler(t, hostProfile)
	if err := h.controlRequest(sess); err != nil {
		t.Fatalf("control request: %v", err)
	}
	if !sess.IsHost() {
		t.Error("session is not host")
	}
	if err := h.controlRequest(sess); !errors.Is(err, ErrIsAlreadyTheHost) {
		t.Errorf("second request err = %v, want %v", err, ErrIsAlreadyTheHost)
	}
}
