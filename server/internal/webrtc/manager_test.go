package webrtc

import (
	"testing"
	"time"

	"github.com/m1k1o/neko/server/internal/config"
	"github.com/m1k1o/neko/server/internal/session"
	"github.com/m1k1o/neko/server/pkg/types"
	"github.com/m1k1o/neko/server/pkg/types/codec"
	"github.com/m1k1o/neko/server/pkg/utils"
)

// listenerCountingCapture has one audio and one video stream. The embedded
// interface is nil: these tests must not reach any other method.
type listenerCountingCapture struct {
	types.CaptureManager
	audio, video *listenerCountingStream
}

func (c *listenerCountingCapture) Audio() types.StreamSinkManager     { return c.audio }
func (c *listenerCountingCapture) Video() types.StreamSelectorManager { return c }

func (c *listenerCountingCapture) IDs() []string         { return []string{c.video.ID()} }
func (c *listenerCountingCapture) Codec() codec.RTPCodec { return c.video.Codec() }
func (c *listenerCountingCapture) GetStream(types.StreamSelector) (types.StreamSinkManager, bool) {
	return c.video, true
}

func (c *listenerCountingCapture) listeners() int {
	return c.audio.listeners() + c.video.listeners()
}

type nopWebSocketPeer struct{ _ bool }

func (nopWebSocketPeer) Send(event string, payload any) {}
func (nopWebSocketPeer) Ping() error                    { return nil }
func (nopWebSocketPeer) Destroy(reason string)          {}

// What the signal/request handler does for a client that wants video and audio.
func requestMedia(manager *WebRTCManagerCtx, session types.Session) error {
	_, peer, err := manager.CreatePeer(session)
	if err != nil {
		return err
	}

	err = peer.SetVideo(types.PeerVideoRequest{Selector: &types.StreamSelector{ID: "test"}})
	if err != nil {
		return err
	}

	disabled := false
	return peer.SetAudio(types.PeerAudioRequest{Disabled: &disabled})
}

// leavingClient is a session with a websocket on a capture of its own, so
// that what one client leaves listening is not counted for the next.
type leavingClient struct {
	capture   *listenerCountingCapture
	manager   *WebRTCManagerCtx
	sessions  *session.SessionManagerCtx
	session   types.Session
	websocket types.WebSocketPeer
}

func (c *leavingClient) request(t *testing.T) {
	t.Helper()
	if err := requestMedia(c.manager, c.session); err != nil {
		t.Fatalf("request media: %v", err)
	}
	if c.capture.listeners() == 0 {
		t.Fatal("nothing listens after the request")
	}
}

// A real peer is closed on its own goroutines: destroying it only starts that.
// These clients ask for media and leave without answering the offer. In the
// end nothing may listen to the capture streams.
func TestPeerOfAClientThatLeftStopsListening(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, c *leavingClient)
	}{
		{
			name: "websocket closes after the offer",
			run: func(t *testing.T, c *leavingClient) {
				c.request(t)
				c.session.DisconnectWebSocketPeer(c.websocket, false)
			},
		},
		{
			name: "session is deleted after the offer",
			run: func(t *testing.T, c *leavingClient) {
				c.request(t)
				if err := c.sessions.Delete(c.session.ID()); err != nil {
					t.Fatalf("delete session: %v", err)
				}
			},
		},
		{
			name: "client asks again and its websocket closes",
			run: func(t *testing.T, c *leavingClient) {
				c.request(t)
				c.request(t)
				c.session.DisconnectWebSocketPeer(c.websocket, false)
			},
		},
		{
			name: "websocket is gone when the peer is set",
			run: func(t *testing.T, c *leavingClient) {
				c.session.DisconnectWebSocketPeer(c.websocket, false)
				// the peer is closed when it is set, which fails the request
				if err := requestMedia(c.manager, c.session); err == nil {
					t.Error("request media succeeded without a websocket")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := &listenerCountingCapture{
				audio: &listenerCountingStream{codec: codec.Opus()},
				video: &listenerCountingStream{codec: codec.VP8()},
			}
			manager := New(nil, capture, &config.WebRTC{ICETrickle: true})
			sessions := session.New(&config.Session{})

			// metrics are registered once per session id
			id, err := utils.NewUID(16)
			if err != nil {
				t.Fatalf("create session id: %v", err)
			}
			profile := types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}
			session, _, err := sessions.Create(id, profile)
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			websocket := &nopWebSocketPeer{}
			session.ConnectWebSocketPeer(websocket)

			tt.run(t, &leavingClient{capture, manager, sessions, session, websocket})

			waitUntilNothingListens(t, capture, session)
		})
	}
}

func waitUntilNothingListens(t *testing.T, capture *listenerCountingCapture, session types.Session) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for capture.listeners() != 0 || session.GetWebRTCPeer() != nil {
		if time.Now().After(deadline) {
			t.Fatalf("%d listeners left, peer left: %t",
				capture.listeners(), session.GetWebRTCPeer() != nil)
		}
		time.Sleep(time.Millisecond)
	}
}

// The first listener of a stream starts its capture pipeline, which takes
// seconds, and its peer is busy for that long. A session is logged out under
// a lock that every login needs, so ending it must not wait for the pipeline.
func TestSessionIsDeletedWithoutWaitingForItsPeerToStartAStream(t *testing.T) {
	capture := &listenerCountingCapture{
		audio: &listenerCountingStream{codec: codec.Opus()},
		video: &listenerCountingStream{
			codec:    codec.VP8(),
			starting: make(chan struct{}),
			start:    make(chan struct{}),
		},
	}
	manager := New(nil, capture, &config.WebRTC{ICETrickle: true})
	sessions := session.New(&config.Session{})

	// metrics are registered once per session id
	id, err := utils.NewUID(16)
	if err != nil {
		t.Fatalf("create session id: %v", err)
	}
	profile := types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}
	session, _, err := sessions.Create(id, profile)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	session.ConnectWebSocketPeer(&nopWebSocketPeer{})

	requested := make(chan struct{})
	go func() {
		defer close(requested)
		// fails or not, depending on how far it got when the peer closed
		_ = requestMedia(manager, session)
	}()
	<-capture.video.starting

	deleted := make(chan error, 1)
	go func() { deleted <- sessions.Delete(id) }()

	select {
	case err := <-deleted:
		if err != nil {
			t.Errorf("delete session: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("deleting the session waits for the stream to start")
	}

	close(capture.video.start)
	<-requested
	waitUntilNothingListens(t, capture, session)
}
