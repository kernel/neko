package session

import (
	"testing"

	"github.com/m1k1o/neko/server/internal/config"
	"github.com/m1k1o/neko/server/pkg/types"
)

func TestLegacyIsHost(t *testing.T) {
	tests := []struct {
		name            string
		implicitHosting bool
		canHost         bool
		setHost         bool
		want            bool
	}{
		{name: "implicit hosting, can host", implicitHosting: true, canHost: true, want: true},
		{name: "implicit hosting, cannot host", implicitHosting: true, canHost: false, want: false},
		{name: "explicit hosting, host", canHost: true, setHost: true, want: true},
		{name: "explicit hosting, not host", canHost: true, want: false},
		{name: "explicit hosting, cannot host", canHost: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := New(&config.Session{ImplicitHosting: tt.implicitHosting})
			session, _, err := manager.Create("member", types.MemberProfile{CanHost: tt.canHost})
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			if tt.setHost {
				session.SetAsHost()
			}

			if got := session.LegacyIsHost(); got != tt.want {
				t.Errorf("LegacyIsHost() = %v, want %v", got, tt.want)
			}
		})
	}
}

type fakeWebSocketPeer struct{}

func (fakeWebSocketPeer) Send(event string, payload any) {}
func (fakeWebSocketPeer) Ping() error                    { return nil }
func (fakeWebSocketPeer) Destroy(reason string)          {}

// fakeWebRTCPeer counts how often it was destroyed. The embedded interface is
// nil: these tests must not reach any other method.
type fakeWebRTCPeer struct {
	types.WebRTCPeer
	destroyed int
}

func (p *fakeWebRTCPeer) Destroy() { p.destroyed++ }

// A client can ask for media and vanish before it answers the offer. Its peer
// never connects, so nothing but the session can end it; left alone it keeps
// the capture pipelines it subscribed to running with nobody watching.
func TestWebRTCPeerThatNeverConnectedIsDestroyedWithItsSession(t *testing.T) {
	profile := types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}

	tests := []struct {
		name          string
		connected     bool
		end           func(manager *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error
		wantDestroyed int
	}{
		{
			name: "websocket closes before the peer connects",
			end: func(_ *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error {
				session.DisconnectWebSocketPeer(websocket, false)
				return nil
			},
			wantDestroyed: 1,
		},
		{
			name:      "websocket closes while the peer is streaming",
			connected: true,
			end: func(_ *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error {
				session.DisconnectWebSocketPeer(websocket, false)
				return nil
			},
			wantDestroyed: 0,
		},
		{
			name: "websocket drops and may still reconnect",
			end: func(_ *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error {
				session.DisconnectWebSocketPeer(websocket, true)
				return nil
			},
			wantDestroyed: 0,
		},
		{
			name: "session is deleted before the peer connects",
			end: func(manager *SessionManagerCtx, session types.Session, _ types.WebSocketPeer) error {
				return manager.Delete(session.ID())
			},
			wantDestroyed: 1,
		},
		{
			name: "session is disconnected before the peer connects",
			end: func(manager *SessionManagerCtx, session types.Session, _ types.WebSocketPeer) error {
				return manager.Disconnect(session.ID())
			},
			wantDestroyed: 1,
		},
		{
			name:      "session is deleted while the peer is streaming",
			connected: true,
			end: func(manager *SessionManagerCtx, session types.Session, _ types.WebSocketPeer) error {
				return manager.Delete(session.ID())
			},
			wantDestroyed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := New(&config.Session{})
			session, _, err := manager.Create("member", profile)
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			websocket := &fakeWebSocketPeer{}
			session.ConnectWebSocketPeer(websocket)
			// stops the timer of a delayed disconnect
			t.Cleanup(func() { session.DisconnectWebSocketPeer(websocket, false) })
			peer := &fakeWebRTCPeer{}
			session.SetWebRTCPeer(peer)
			if tt.connected {
				session.SetWebRTCConnected(peer, true)
			}

			if err := tt.end(manager, session, websocket); err != nil {
				t.Fatalf("end session: %v", err)
			}

			if peer.destroyed != tt.wantDestroyed {
				t.Errorf("peer destroyed %d times, want %d", peer.destroyed, tt.wantDestroyed)
			}
		})
	}
}

// Creating a peer takes a while, and the websocket that asked for it can be
// gone by the time the peer is attached to the session. Its disconnect could
// not destroy a peer that did not exist yet.
func TestWebRTCPeerSetAfterItsWebSocketLeftIsDestroyed(t *testing.T) {
	profile := types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}

	tests := []struct {
		name          string
		leave         func(manager *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error
		wantDestroyed int
	}{
		{
			name: "websocket still connected",
			leave: func(*SessionManagerCtx, types.Session, types.WebSocketPeer) error {
				return nil
			},
			wantDestroyed: 0,
		},
		{
			name: "websocket dropped and may still reconnect",
			leave: func(_ *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error {
				session.DisconnectWebSocketPeer(websocket, true)
				return nil
			},
			wantDestroyed: 0,
		},
		{
			name: "websocket closed",
			leave: func(_ *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error {
				session.DisconnectWebSocketPeer(websocket, false)
				return nil
			},
			wantDestroyed: 1,
		},
		{
			name: "session deleted",
			leave: func(manager *SessionManagerCtx, session types.Session, _ types.WebSocketPeer) error {
				return manager.Delete(session.ID())
			},
			wantDestroyed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := New(&config.Session{})
			session, _, err := manager.Create("member", profile)
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			websocket := &fakeWebSocketPeer{}
			session.ConnectWebSocketPeer(websocket)
			// stops the timer of a delayed disconnect
			t.Cleanup(func() { session.DisconnectWebSocketPeer(websocket, false) })

			if err := tt.leave(manager, session, websocket); err != nil {
				t.Fatalf("leave: %v", err)
			}
			peer := &fakeWebRTCPeer{}
			session.SetWebRTCPeer(peer)

			if peer.destroyed != tt.wantDestroyed {
				t.Errorf("peer destroyed %d times, want %d", peer.destroyed, tt.wantDestroyed)
			}
		})
	}
}
