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

// A real peer is closed on its own goroutines: destroying it only starts that.
// These clients ask for media and leave without answering the offer. In the
// end nothing may listen to the capture streams.
func TestPeerOfAClientThatLeftStopsListening(t *testing.T) {
	capture := &listenerCountingCapture{
		audio: &listenerCountingStream{codec: codec.Opus()},
		video: &listenerCountingStream{codec: codec.VP8()},
	}
	manager := New(nil, capture, &config.WebRTC{ICETrickle: true})
	sessions := session.New(&config.Session{})

	request := func(t *testing.T, session types.Session) {
		t.Helper()
		if err := requestMedia(manager, session); err != nil {
			t.Fatalf("request media: %v", err)
		}
		if capture.listeners() == 0 {
			t.Fatal("nothing listens after the request")
		}
	}

	tests := []struct {
		name string
		run  func(t *testing.T, session types.Session, websocket types.WebSocketPeer)
	}{
		{
			name: "websocket closes after the offer",
			run: func(t *testing.T, session types.Session, websocket types.WebSocketPeer) {
				request(t, session)
				session.DisconnectWebSocketPeer(websocket, false)
			},
		},
		{
			name: "session is deleted after the offer",
			run: func(t *testing.T, session types.Session, _ types.WebSocketPeer) {
				request(t, session)
				if err := sessions.Delete(session.ID()); err != nil {
					t.Fatalf("delete session: %v", err)
				}
			},
		},
		{
			name: "client asks again and its websocket closes",
			run: func(t *testing.T, session types.Session, websocket types.WebSocketPeer) {
				request(t, session)
				request(t, session)
				session.DisconnectWebSocketPeer(websocket, false)
			},
		},
		{
			name: "websocket closes while the peer is created",
			run: func(t *testing.T, session types.Session, websocket types.WebSocketPeer) {
				session.DisconnectWebSocketPeer(websocket, false)
				// the peer is closed when it is set, which fails the request
				if err := requestMedia(manager, session); err == nil {
					t.Error("request media succeeded without a websocket")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			tt.run(t, session, websocket)

			deadline := time.Now().Add(5 * time.Second)
			for capture.listeners() != 0 || session.GetWebRTCPeer() != nil {
				if time.Now().After(deadline) {
					t.Fatalf("%d listeners left, peer left: %t",
						capture.listeners(), session.GetWebRTCPeer() != nil)
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}
