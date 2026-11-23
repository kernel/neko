package legacy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	oldEvent "github.com/m1k1o/neko/server/internal/http/legacy/event"
	"github.com/m1k1o/neko/server/pkg/types"
	"github.com/m1k1o/neko/server/pkg/types/event"
)

// clientPair returns a session whose client websocket is connected to the
// returned conn, which reads what the session sends to the legacy client.
func clientPair(t *testing.T) (*session, *websocket.Conn) {
	t.Helper()

	serverConn := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		serverConn <- conn
	}))
	t.Cleanup(srv.Close)

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	conn := <-serverConn
	t.Cleanup(func() { conn.Close() })

	return &session{connClient: conn}, client
}

func TestWsToClientForwardsSignalClose(t *testing.T) {
	s, client := clientPair(t)

	msg, err := json.Marshal(types.WebSocketMessage{Event: event.SIGNAL_CLOSE})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.wsToClient(msg); err != nil {
		t.Fatalf("wsToClient: %v", err)
	}

	client.SetReadDeadline(time.Now().Add(5 * time.Second))
	var got map[string]any
	if err := client.ReadJSON(&got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got["event"] != oldEvent.SIGNAL_CLOSE {
		t.Fatalf("client got event %v, want %q", got["event"], oldEvent.SIGNAL_CLOSE)
	}
}
