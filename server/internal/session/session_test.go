package session

import (
	"runtime"
	"sync"
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

func newTestSession(t *testing.T) *SessionCtx {
	t.Helper()
	manager := New(&config.Session{})
	session, _, err := manager.Create("member", types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return session.(*SessionCtx)
}

func newConnectedTestSession(t *testing.T) (*SessionCtx, *fakeWebSocketPeer) {
	t.Helper()
	session := newTestSession(t)
	websocket := &fakeWebSocketPeer{}
	session.ConnectWebSocketPeer(websocket)
	return session, websocket
}

func TestSettingSameWebRTCPeerPreservesItsConnection(t *testing.T) {
	session, websocket := newConnectedTestSession(t)
	peer := &fakeWebRTCPeer{}
	session.SetWebRTCPeer(peer)
	session.SetWebRTCConnected(peer, true)
	session.SetWebRTCPeer(peer)
	session.DisconnectWebSocketPeer(websocket, false)
	if peer.destroyed {
		t.Fatal("setting the same peer made it lose its connection")
	}
}

func TestPendingWebRTCPeerWaitsForReconnectDeadline(t *testing.T) {
	for _, tt := range []struct {
		name          string
		after         time.Duration
		wantDestroyed bool
	}{
		{"before expiry", WS_DELAYED_DURATION - time.Nanosecond, false},
		{"at expiry", WS_DELAYED_DURATION, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				session, websocket := newConnectedTestSession(t)
				peer := &fakeWebRTCPeer{}
				session.SetWebRTCPeer(peer)
				session.DisconnectWebSocketPeer(websocket, true)
				defer session.DisconnectWebSocketPeer(websocket, false)

				time.Sleep(tt.after)
				synctest.Wait()
				if peer.destroyed != tt.wantDestroyed {
					t.Errorf("peer destroyed = %t, want %t", peer.destroyed, tt.wantDestroyed)
				}
			})
		})
	}
}

func TestPeerSetDuringReconnectGraceSurvivesReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, websocket := newConnectedTestSession(t)
		session.DisconnectWebSocketPeer(websocket, true)
		peer := &fakeWebRTCPeer{}
		session.SetWebRTCPeer(peer)
		session.ConnectWebSocketPeer(&fakeWebSocketPeer{})

		time.Sleep(WS_DELAYED_DURATION)
		synctest.Wait()
		if peer.destroyed {
			t.Fatal("old websocket's timer destroyed the peer after reconnect")
		}
	})
}

// A client that is watching can ask for media again, which replaces its peer.
// The session goes on counting as watching, because the closing of a replaced
// peer is ignored, but the new peer has not connected.
func TestWebRTCPeerThatReplacedAStreamingOneIsDestroyedUnlessItConnected(t *testing.T) {
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
			session, websocket := newConnectedTestSession(t)
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

func TestConcurrentWebRTCPeerReplacement(t *testing.T) {
	session, _ := newConnectedTestSession(t)
	peers := []*fakeWebRTCPeer{{}, {}, {}}
	session.SetWebRTCPeer(peers[0])

	var wg sync.WaitGroup
	for _, peer := range peers[1:] {
		wg.Go(func() { session.SetWebRTCPeer(peer) })
	}
	wg.Wait()

	current := session.GetWebRTCPeer()
	if current != peers[1] && current != peers[2] {
		t.Fatal("neither replacement is the current peer")
	}
	for i, peer := range peers {
		if want := peer != current; peer.destroyed != want {
			t.Errorf("peer %d destroyed = %t, want %t", i, peer.destroyed, want)
		}
	}
}

func TestSessionEndCleansUpWebRTCPeer(t *testing.T) {
	tests := []struct {
		name string
		end  func(*SessionManagerCtx, string) error
	}{
		{"delete", (*SessionManagerCtx).Delete},
		{"disconnect", (*SessionManagerCtx).Disconnect},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/streaming without a websocket", func(t *testing.T) {
			session, websocket := newConnectedTestSession(t)
			peer := &fakeWebRTCPeer{}
			session.SetWebRTCPeer(peer)
			session.SetWebRTCConnected(peer, true)
			session.DisconnectWebSocketPeer(websocket, false)
			if peer.destroyed {
				t.Fatal("websocket disconnect destroyed a streaming peer")
			}

			if err := tt.end(session.manager, session.ID()); err != nil {
				t.Fatal(err)
			}
			if !peer.destroyed {
				t.Fatal("ending the session left its streaming peer alive")
			}
		})
		t.Run(tt.name+"/peer already gone", func(t *testing.T) {
			session, _ := newConnectedTestSession(t)
			session.state.IsWatching = true // can outlive a replaced peer
			if err := tt.end(session.manager, session.ID()); err != nil {
				t.Fatal(err)
			}

			// A media request can finish setting its peer after the session ends.
			late := &fakeWebRTCPeer{}
			session.SetWebRTCPeer(late)
			if !late.destroyed {
				t.Fatal("peer set after the session ended was not destroyed")
			}
		})
	}
}

// A session that may no longer connect has its peer destroyed a moment after
// its websocket. The websocket takes a peer that has not connected with it,
// so a session that counts as watching can have no peer left by then.
func TestSessionThatCountsAsWatchingWithoutAPeerCanLoseItsLogin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session := newTestSession(t)
		session.state.IsWatching = true

		profile := session.Profile()
		profile.CanLogin = false
		if err := session.manager.Update(session.ID(), profile); err != nil {
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
	session, websocket := newConnectedTestSession(t)

	peer := &fakeWebRTCPeer{}
	session.manager.OnDisconnected(func(session types.Session) {
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
	session, websocket := newConnectedTestSession(t)

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
