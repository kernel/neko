package telemetry

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/m1k1o/neko/server/internal/config"
	intsession "github.com/m1k1o/neko/server/internal/session"
	"github.com/m1k1o/neko/server/pkg/types"
)

func TestPlugin_DisabledIsNoop(t *testing.T) {
	srv, captured := newCapturingServer(t)
	defer srv.Close()

	sm := newSessionManager(t)
	m := NewManager(sm, &Config{Enabled: false, Endpoint: srv.URL})
	require.NoError(t, m.Start())
	defer func() { require.NoError(t, m.Shutdown()) }()

	connect(t, sm, "1")
	time.Sleep(50 * time.Millisecond)

	require.Empty(t, captured.snapshot(), "no events expected when disabled")
}

func TestPlugin_EmitsConnectAndDisconnect(t *testing.T) {
	srv, captured := newCapturingServer(t)
	defer srv.Close()

	sm := newSessionManager(t)
	m := NewManager(sm, &Config{Enabled: true, Endpoint: srv.URL})
	require.NoError(t, m.Start())
	defer func() { require.NoError(t, m.Shutdown()) }()

	s, p := connect(t, sm, "session-1")
	// Hold a connection for a measurable interval so duration_ms is positive.
	time.Sleep(20 * time.Millisecond)
	s.DisconnectWebSocketPeer(p, false)

	require.Eventually(t, func() bool {
		return len(captured.snapshot()) >= 2
	}, 2*time.Second, 10*time.Millisecond, "expected 2 events")

	events := captured.snapshot()
	require.GreaterOrEqual(t, len(events), 2)

	require.Equal(t, "live_view_connect", events[0].Type)
	require.Equal(t, "system", events[0].Category)
	require.Equal(t, "local_process", events[0].Source.Kind)
	require.Equal(t, "neko.session.connected", events[0].Source.Event)
	require.Equal(t, "session-1", events[0].Data["session_id"])
	require.NotContains(t, events[0].Data, "duration_ms")

	require.Equal(t, "live_view_disconnect", events[1].Type)
	require.Equal(t, "system", events[1].Category)
	require.Equal(t, "local_process", events[1].Source.Kind)
	require.Equal(t, "neko.session.disconnected", events[1].Source.Event)
	require.Equal(t, "session-1", events[1].Data["session_id"])
	dur, ok := events[1].Data["duration_ms"].(float64)
	require.True(t, ok, "duration_ms should be a number")
	require.Greater(t, dur, 0.0)
}

func TestPlugin_DropsOnEndpointFailureWithoutBlocking(t *testing.T) {
	// Endpoint that always 500s; plugin should still drain without blocking
	// the session goroutines.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sm := newSessionManager(t)
	m := NewManager(sm, &Config{Enabled: true, Endpoint: srv.URL})
	require.NoError(t, m.Start())
	defer func() { require.NoError(t, m.Shutdown()) }()

	done := make(chan struct{})
	go func() {
		s, p := connect(t, sm, "session-error")
		s.DisconnectWebSocketPeer(p, false)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("session connect/disconnect blocked on telemetry plugin")
	}
}

// capturingServer collects POSTed JSON bodies for assertion.
type capturingServer struct {
	mu       sync.Mutex
	captured []capturedEvent
}

type capturedEvent struct {
	Type     string         `json:"type"`
	Category string         `json:"category"`
	Source   publishSource  `json:"source"`
	Data     map[string]any `json:"data"`
}

func (c *capturingServer) snapshot() []capturedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedEvent, len(c.captured))
	copy(out, c.captured)
	return out
}

func newCapturingServer(t *testing.T) (*httptest.Server, *capturingServer) {
	t.Helper()
	c := &capturingServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var ev capturedEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			t.Errorf("unmarshal body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.captured = append(c.captured, ev)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"seq":1,"event":{}}`))
	}))
	return srv, c
}

type mockWebsocketPeer struct{}

func (mockWebsocketPeer) Send(event string, payload any) {}
func (mockWebsocketPeer) Ping() error                    { return nil }
func (mockWebsocketPeer) Destroy(reason string)          {}

func newSessionManager(t *testing.T) *intsession.SessionManagerCtx {
	t.Helper()
	return intsession.New(&config.Session{})
}

func connect(t *testing.T, sm types.SessionManager, id string) (types.Session, types.WebSocketPeer) {
	t.Helper()
	s, ok := sm.Get(id)
	if !ok {
		var err error
		s, _, err = sm.Create(id, types.MemberProfile{CanLogin: true, CanConnect: true, CanWatch: true})
		require.NoError(t, err)
	}
	p := &mockWebsocketPeer{}
	s.ConnectWebSocketPeer(p)
	return s, p
}

func TestManager_ReportsLiveViewFailure(t *testing.T) {
	srv, captured := newCapturingServer(t)
	defer srv.Close()

	sm := newSessionManager(t)
	m := NewManager(sm, &Config{Enabled: true, Endpoint: srv.URL})
	require.NoError(t, m.Start())
	defer func() { require.NoError(t, m.Shutdown()) }()

	m.reportLiveViewFailure("session-1", codeGPUEncoderUnavailable, `(pipeline error) no element "nvh264enc"`)

	require.Eventually(t, func() bool {
		return len(captured.snapshot()) >= 1
	}, 2*time.Second, 10*time.Millisecond, "expected a failure event")

	ev := captured.snapshot()[0]
	require.Equal(t, "live_view_failed", ev.Type)
	require.Equal(t, "connection", ev.Category)
	require.Equal(t, "local_process", ev.Source.Kind)
	require.Equal(t, "neko.live_view.pipeline_failed", ev.Source.Event)
	require.Equal(t, "session-1", ev.Data["session_id"])
	require.Equal(t, codeGPUEncoderUnavailable, ev.Data["code"])
	require.NotEmpty(t, ev.Data["message"])
}

func TestReportLiveViewFailure_WithoutReporter(t *testing.T) {
	setReporter(nil)

	require.NotPanics(t, func() {
		ReportLiveViewFailure("session-1", errors.New(`(pipeline error) no element "nvh264enc"`))
	})
	require.NotPanics(t, func() {
		ReportLiveViewFailure("", errors.New("pipeline failed"))
	})
	require.NotPanics(t, func() {
		ReportLiveViewFailure("session-1", nil)
	})
}

func TestClassifyPipelineError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"missing encoder element", errors.New(`(pipeline error) no element "nvh264enc"`), codeGPUEncoderUnavailable},
		{"cuda context", errors.New("failed to create cuda context, ret: 0x2"), codeGPUEncoderUnavailable},
		{"plugin load", errors.New("element plugin couldn't be loaded"), codeGPUEncoderUnavailable},
		{"other pipeline failure", errors.New("webrtc stream not found"), codePipelineFailed},
		{"no error", nil, codePipelineFailed},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, classifyPipelineError(c.err))
		})
	}
}
