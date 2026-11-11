package webrtc

import (
	"errors"
	"testing"

	"github.com/pion/webrtc/v3"
	"github.com/rs/zerolog"

	"github.com/m1k1o/neko/server/pkg/types"
	"github.com/m1k1o/neko/server/pkg/types/codec"
)

// listenerCountingStream counts its listeners. The embedded interface is nil:
// these tests must not reach any other method.
type listenerCountingStream struct {
	types.StreamSinkManager
	added, removed int
}

func (s *listenerCountingStream) AddListener(types.SampleListener) error {
	s.added++
	return nil
}

func (s *listenerCountingStream) RemoveListener(types.SampleListener) error {
	s.removed++
	return nil
}

func newTestTrack(t *testing.T) *Track {
	t.Helper()
	connection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create peer connection: %v", err)
	}
	t.Cleanup(func() { connection.Close() })

	track, err := NewTrack(zerolog.Nop(), codec.VP8(), connection)
	if err != nil {
		t.Fatalf("create track: %v", err)
	}
	return track
}

func TestTrackStopsListeningOnShutdown(t *testing.T) {
	track := newTestTrack(t)
	stream := &listenerCountingStream{}

	if _, err := track.SetStream(stream); err != nil {
		t.Fatalf("set stream: %v", err)
	}
	track.Shutdown()

	if stream.added != 1 || stream.removed != 1 {
		t.Errorf("listener added %d times and removed %d times, want 1 and 1", stream.added, stream.removed)
	}
}

// A peer can be destroyed between its creation and the request that selects
// its video. The track is shut down by then; listening to a stream after that
// would keep the pipeline running for a track nobody removes.
func TestTrackDoesNotListenAfterShutdown(t *testing.T) {
	track := newTestTrack(t)
	stream := &listenerCountingStream{}

	track.Shutdown()

	if _, err := track.SetStream(stream); !errors.Is(err, ErrTrackClosed) {
		t.Errorf("set stream after shutdown returned %v, want %v", err, ErrTrackClosed)
	}
	// unpausing is what starts audio
	track.SetPaused(true)
	track.SetPaused(false)

	if stream.added != 0 {
		t.Errorf("listener added %d times after shutdown, want 0", stream.added)
	}
}
