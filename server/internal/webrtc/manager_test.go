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

type nopWebSocketPeer struct{ _ bool }

func (nopWebSocketPeer) Send(event string, payload any) {}
func (nopWebSocketPeer) Ping() error                    { return nil }
func (nopWebSocketPeer) Destroy(reason string)          {}

type leavingClient struct {
	capture   *listenerCountingCapture
	manager   *WebRTCManagerCtx
	sessions  *session.SessionManagerCtx
	session   types.Session
	websocket types.WebSocketPeer
}

func newLeavingClient(t *testing.T) *leavingClient {
	t.Helper()
	capture := &listenerCountingCapture{
		audio: &listenerCountingStream{codec: codec.Opus()},
		video: &listenerCountingStream{codec: codec.VP8()},
	}
	sessions := session.New(&config.Session{})
	// Metrics are registered once per session ID.
	id, err := utils.NewUID(16)
	if err != nil {
		t.Fatalf("create session id: %v", err)
	}
	profile := types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true}
	s, _, err := sessions.Create(id, profile)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	ws := &nopWebSocketPeer{}
	s.ConnectWebSocketPeer(ws)
	return &leavingClient{
		capture:   capture,
		manager:   New(nil, capture, &config.WebRTC{ICETrickle: true}),
		sessions:  sessions,
		session:   s,
		websocket: ws,
	}
}

// What the signal/request handler does for a client that wants video and audio.
func (c *leavingClient) requestMedia() error {
	_, peer, err := c.manager.CreatePeer(c.session)
	if err != nil {
		return err
	}
	if err := peer.SetVideo(types.PeerVideoRequest{Selector: &types.StreamSelector{ID: "test"}}); err != nil {
		return err
	}
	disabled := false
	return peer.SetAudio(types.PeerAudioRequest{Disabled: &disabled})
}

func (c *leavingClient) waitForCleanup(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		audio, video := c.capture.audio.listeners(), c.capture.video.listeners()
		peer := c.session.GetWebRTCPeer()
		if audio == 0 && video == 0 && peer == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("listeners left: audio=%d video=%d; peer left: %t", audio, video, peer != nil)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPeerOfAClientThatLeftStopsListening(t *testing.T) {
	tests := []struct {
		name     string
		requests int
	}{
		{"unanswered offer", 1},
		{"replaced unanswered offer", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newLeavingClient(t)
			for range tt.requests {
				if err := c.requestMedia(); err != nil {
					t.Fatalf("request media: %v", err)
				}
				if audio, video := c.capture.audio.listeners(), c.capture.video.listeners(); audio < 1 || video < 1 {
					t.Fatalf("expected listeners after request: audio=%d video=%d", audio, video)
				}
			}
			c.session.DisconnectWebSocketPeer(c.websocket, false)
			c.waitForCleanup(t)
		})
	}
}

func TestMediaRequestAfterWebSocketDisconnect(t *testing.T) {
	c := newLeavingClient(t)
	c.session.DisconnectWebSocketPeer(c.websocket, false)
	if err := c.requestMedia(); err == nil {
		t.Error("request media succeeded without a websocket")
	}
	c.waitForCleanup(t)
}

// The first listener of a stream starts its capture pipeline, which takes
// seconds, and its peer is busy for that long. A session is logged out under
// a lock that every login needs, so ending it must not wait for the pipeline.
func TestSessionIsDeletedWithoutWaitingForItsPeerToStartAStream(t *testing.T) {
	c := newLeavingClient(t)
	video := c.capture.video
	video.starting, video.start = make(chan struct{}), make(chan struct{})

	requested := make(chan struct{})
	go func() {
		defer close(requested)
		// fails or not, depending on how far it got when the peer closed
		_ = c.requestMedia()
	}()
	select {
	case <-video.starting:
	case <-requested:
		t.Fatal("media request finished without starting the stream")
	}

	deleted := make(chan error, 1)
	go func() { deleted <- c.sessions.Delete(c.session.ID()) }()

	select {
	case err := <-deleted:
		if err != nil {
			t.Errorf("delete session: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("deleting the session waits for the stream to start")
	}

	close(video.start)
	<-requested
	c.waitForCleanup(t)
}
