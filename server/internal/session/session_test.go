package session

import (
	"runtime"
	"testing"
	"testing/synctest"
	"time"

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

// Not empty, so that two of them are never the same pointer.
type fakeWebSocketPeer struct{ _ bool }

func (fakeWebSocketPeer) Send(event string, payload any) {}
func (fakeWebSocketPeer) Ping() error                    { return nil }
func (fakeWebSocketPeer) Destroy(reason string)          {}

// fakeWebRTCPeer records whether it was destroyed. Like the real peer it can
// be destroyed more than once. The embedded interface is nil: these tests
// must not reach any other method.
type fakeWebRTCPeer struct {
	types.WebRTCPeer
	destroyed bool
}

func (p *fakeWebRTCPeer) Destroy() { p.destroyed = true }

// A client can ask for media and vanish before it answers the offer. Its peer
// never connects, so nothing but the session can end it; left alone it keeps
// the capture pipelines it subscribed to running with nobody watching.
func TestWebRTCPeerThatNeverConnectedIsDestroyedWithItsSession(t *testing.T) {
	profile := types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}

	tests := []struct {
		name          string
		connected     bool
		end           func(manager *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error
		wantDestroyed bool
	}{
		{
			name: "websocket closes before the peer connects",
			end: func(_ *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error {
				session.DisconnectWebSocketPeer(websocket, false)
				return nil
			},
			wantDestroyed: true,
		},
		{
			name:      "websocket closes while the peer is streaming",
			connected: true,
			end: func(_ *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error {
				session.DisconnectWebSocketPeer(websocket, false)
				return nil
			},
			wantDestroyed: false,
		},
		{
			name:      "websocket closes while the peer is streaming and was set again",
			connected: true,
			end: func(_ *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error {
				session.SetWebRTCPeer(session.GetWebRTCPeer())
				session.DisconnectWebSocketPeer(websocket, false)
				return nil
			},
			wantDestroyed: false,
		},
		{
			name: "websocket drops and may still reconnect",
			end: func(_ *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error {
				session.DisconnectWebSocketPeer(websocket, true)
				return nil
			},
			wantDestroyed: false,
		},
		{
			name: "session is deleted before the peer connects",
			end: func(manager *SessionManagerCtx, session types.Session, _ types.WebSocketPeer) error {
				return manager.Delete(session.ID())
			},
			wantDestroyed: true,
		},
		{
			name: "session is disconnected before the peer connects",
			end: func(manager *SessionManagerCtx, session types.Session, _ types.WebSocketPeer) error {
				return manager.Disconnect(session.ID())
			},
			wantDestroyed: true,
		},
		{
			name:      "session is deleted while the peer is streaming",
			connected: true,
			end: func(manager *SessionManagerCtx, session types.Session, _ types.WebSocketPeer) error {
				return manager.Delete(session.ID())
			},
			wantDestroyed: true,
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
				t.Errorf("peer destroyed = %t, want %t", peer.destroyed, tt.wantDestroyed)
			}
		})
	}
}

// A websocket that dropped is given time to come back, and a peer that has not
// connected is kept for that long. When the time is up the peer is destroyed,
// unless the client is back on a new websocket by then.
func TestWebRTCPeerThatNeverConnectedIsKeptWhileItsWebSocketMayReconnect(t *testing.T) {
	profile := types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}

	tests := []struct {
		name string
		// how long after the drop the client is back, if it comes back
		reconnectAfter time.Duration
		// how long after the drop the peer is looked at
		after         time.Duration
		wantDestroyed bool
	}{
		{
			name:          "time is not up",
			after:         WS_DELAYED_DURATION - time.Second,
			wantDestroyed: false,
		},
		{
			name:          "time is up",
			after:         WS_DELAYED_DURATION + time.Second,
			wantDestroyed: true,
		},
		{
			name:           "client came back in time",
			reconnectAfter: WS_DELAYED_DURATION - time.Second,
			after:          WS_DELAYED_DURATION + time.Second,
			wantDestroyed:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manager := New(&config.Session{})
				session, _, err := manager.Create("member", profile)
				if err != nil {
					t.Fatalf("create session: %v", err)
				}
				websocket := &fakeWebSocketPeer{}
				session.ConnectWebSocketPeer(websocket)
				// stops the timer of a delayed disconnect
				defer session.DisconnectWebSocketPeer(websocket, false)
				peer := &fakeWebRTCPeer{}
				session.SetWebRTCPeer(peer)

				session.DisconnectWebSocketPeer(websocket, true)
				if tt.reconnectAfter != 0 {
					time.Sleep(tt.reconnectAfter)
					session.ConnectWebSocketPeer(&fakeWebSocketPeer{})
				}
				time.Sleep(tt.after - tt.reconnectAfter)
				synctest.Wait()

				if peer.destroyed != tt.wantDestroyed {
					t.Errorf("peer destroyed = %t, want %t", peer.destroyed, tt.wantDestroyed)
				}
			})
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
		wantDestroyed bool
	}{
		{
			name: "websocket still connected",
			leave: func(*SessionManagerCtx, types.Session, types.WebSocketPeer) error {
				return nil
			},
			wantDestroyed: false,
		},
		{
			name: "websocket dropped and may still reconnect",
			leave: func(_ *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error {
				session.DisconnectWebSocketPeer(websocket, true)
				return nil
			},
			wantDestroyed: false,
		},
		{
			name: "websocket closed",
			leave: func(_ *SessionManagerCtx, session types.Session, websocket types.WebSocketPeer) error {
				session.DisconnectWebSocketPeer(websocket, false)
				return nil
			},
			wantDestroyed: true,
		},
		{
			name: "session deleted",
			leave: func(manager *SessionManagerCtx, session types.Session, _ types.WebSocketPeer) error {
				return manager.Delete(session.ID())
			},
			wantDestroyed: true,
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
				t.Errorf("peer destroyed = %t, want %t", peer.destroyed, tt.wantDestroyed)
			}
		})
	}
}

// A client that is watching can ask for media again, which replaces its peer.
// The session goes on counting as watching, because the closing of a replaced
// peer is ignored, but the new peer has not connected.
func TestWebRTCPeerThatReplacedAStreamingOneIsDestroyedUnlessItConnected(t *testing.T) {
	profile := types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}

	tests := []struct {
		name          string
		connected     bool
		wantDestroyed bool
	}{
		{name: "websocket closes before the new peer connects", wantDestroyed: true},
		{name: "websocket closes while the new peer is streaming", connected: true, wantDestroyed: false},
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
			replaced := &fakeWebRTCPeer{}
			session.SetWebRTCPeer(replaced)
			session.SetWebRTCConnected(replaced, true)

			peer := &fakeWebRTCPeer{}
			session.SetWebRTCPeer(peer)
			if tt.connected {
				session.SetWebRTCConnected(peer, true)
			}
			// the replaced peer reports its closing whenever it gets to it
			session.SetWebRTCConnected(replaced, false)

			session.DisconnectWebSocketPeer(websocket, false)

			if !replaced.destroyed {
				t.Error("replaced peer was not destroyed")
			}
			if peer.destroyed != tt.wantDestroyed {
				t.Errorf("peer destroyed = %t, want %t", peer.destroyed, tt.wantDestroyed)
			}
		})
	}
}

// A closing peer is cleared from the session on another goroutine. A session
// that is deleted or disconnected at that moment can still count as watching
// with no peer left to destroy.
func TestSessionThatCountsAsWatchingWithoutAPeerCanBeEnded(t *testing.T) {
	profile := types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}

	tests := []struct {
		name string
		end  func(manager *SessionManagerCtx, id string) error
	}{
		{name: "session is deleted", end: (*SessionManagerCtx).Delete},
		{name: "session is disconnected", end: (*SessionManagerCtx).Disconnect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := New(&config.Session{})
			session, _, err := manager.Create("member", profile)
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			session.(*SessionCtx).state.IsWatching = true

			if err := tt.end(manager, session.ID()); err != nil {
				t.Fatalf("end session: %v", err)
			}
		})
	}
}

// A session that may no longer connect has its peer destroyed a moment after
// its websocket. The websocket takes a peer that has not connected with it,
// so a session that counts as watching can have no peer left by then.
func TestSessionThatCountsAsWatchingWithoutAPeerCanLoseItsLogin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		profile := types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}

		manager := New(&config.Session{})
		session, _, err := manager.Create("member", profile)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		session.(*SessionCtx).state.IsWatching = true

		profile.CanLogin = false
		if err := manager.Update(session.ID(), profile); err != nil {
			t.Fatalf("update session: %v", err)
		}

		// the peer is destroyed from a timer
		time.Sleep(2 * time.Second)
	})
}

// A client can be back on a new websocket before the old one is done leaving,
// because the session's listeners run in between. A peer the new websocket
// asked for is not the old one's to destroy.
func TestWebRTCPeerOfAWebSocketThatTookOverIsKept(t *testing.T) {
	profile := types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}

	manager := New(&config.Session{})
	session, _, err := manager.Create("member", profile)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	websocket := &fakeWebSocketPeer{}
	session.ConnectWebSocketPeer(websocket)

	peer := &fakeWebRTCPeer{}
	manager.OnDisconnected(func(session types.Session) {
		session.ConnectWebSocketPeer(&fakeWebSocketPeer{})
		session.SetWebRTCPeer(peer)
	})

	session.DisconnectWebSocketPeer(websocket, false)

	if peer.destroyed {
		t.Error("peer of the websocket that took over was destroyed")
	}
}

// The new websocket can also connect after the old one was cleared from the
// session, and ask for a peer before the old one looks for a peer to destroy.
func TestWebRTCPeerOfAWebSocketThatConnectedAfterTheOldOneWasClearedIsKept(t *testing.T) {
	profile := types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}

	manager := New(&config.Session{})
	created, _, err := manager.Create("member", profile)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	session := created.(*SessionCtx)
	websocket := &fakeWebSocketPeer{}
	session.ConnectWebSocketPeer(websocket)

	// holds the old websocket where it looks for a peer to destroy
	session.webrtcMu.Lock()
	disconnected := make(chan struct{})
	go func() {
		defer close(disconnected)
		session.DisconnectWebSocketPeer(websocket, false)
	}()
	for cleared := false; !cleared; {
		session.websocketMu.Lock()
		cleared = session.websocketPeer == nil
		session.websocketMu.Unlock()
		runtime.Gosched()
	}

	// the lock is held, so the peer is set as SetWebRTCPeer would set it
	session.ConnectWebSocketPeer(&fakeWebSocketPeer{})
	peer := &fakeWebRTCPeer{}
	session.webrtcPeer, session.webrtcConnected = peer, false
	session.webrtcMu.Unlock()
	<-disconnected

	if peer.destroyed {
		t.Error("peer of the websocket that connected was destroyed")
	}
}
