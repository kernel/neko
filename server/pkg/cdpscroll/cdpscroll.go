package cdpscroll

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Client sends pixel-precise mouseWheel events to Chromium via CDP.
// It auto-discovers the browser WebSocket URL from the devtools HTTP endpoint.
type Client struct {
	logger    zerolog.Logger
	baseURL   string // e.g. "http://127.0.0.1:9223"
	mu        sync.Mutex
	conn      *websocket.Conn
	msgID     atomic.Int64
	sessionID string
}

// New creates a CDP scroll client. baseURL is the Chromium devtools HTTP
// endpoint (e.g. "http://127.0.0.1:9223").
func New(baseURL string) *Client {
	return &Client{
		logger:  log.With().Str("module", "cdpscroll").Logger(),
		baseURL: baseURL,
	}
}

type cdpMessage struct {
	ID        int64           `json:"id"`
	Method    string          `json:"method,omitempty"`
	Params    map[string]any  `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) discoverWSURL(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/json/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("discover CDP URL: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(body, &version); err != nil {
		return "", fmt.Errorf("parse /json/version: %w", err)
	}
	if version.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("empty webSocketDebuggerUrl in /json/version")
	}
	return version.WebSocketDebuggerURL, nil
}

func (c *Client) connect(ctx context.Context) error {
	if c.conn != nil {
		return nil
	}

	wsURL, err := c.discoverWSURL(ctx)
	if err != nil {
		return err
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 2 * time.Second,
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		return fmt.Errorf("cdp dial: %w", err)
	}
	c.conn = conn

	if err := c.attachToPage(ctx); err != nil {
		c.conn.Close()
		c.conn = nil
		return err
	}

	c.logger.Info().Str("url", wsURL).Msg("CDP scroll connected")
	return nil
}

func (c *Client) send(ctx context.Context, method string, params map[string]any, sessionID string) (json.RawMessage, error) {
	id := c.msgID.Add(1)
	msg := cdpMessage{
		ID:        id,
		Method:    method,
		Params:    params,
		SessionID: sessionID,
	}

	if err := c.conn.WriteJSON(msg); err != nil {
		return nil, fmt.Errorf("cdp write %s: %w", method, err)
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(3 * time.Second)
	}
	c.conn.SetReadDeadline(deadline)

	for {
		var resp cdpMessage
		if err := c.conn.ReadJSON(&resp); err != nil {
			return nil, fmt.Errorf("cdp read %s: %w", method, err)
		}
		if resp.ID == id {
			if resp.Error != nil {
				return nil, fmt.Errorf("cdp %s error: %s", method, resp.Error.Message)
			}
			return resp.Result, nil
		}
	}
}

func (c *Client) attachToPage(ctx context.Context) error {
	result, err := c.send(ctx, "Target.getTargets", nil, "")
	if err != nil {
		return err
	}

	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(result, &targets); err != nil {
		return fmt.Errorf("parse targets: %w", err)
	}

	var pageTargetID string
	for _, t := range targets.TargetInfos {
		if t.Type == "page" {
			pageTargetID = t.TargetID
			break
		}
	}
	if pageTargetID == "" {
		return fmt.Errorf("no page target found")
	}

	result, err = c.send(ctx, "Target.attachToTarget", map[string]any{
		"targetId": pageTargetID,
		"flatten":  true,
	}, "")
	if err != nil {
		return err
	}

	var attach struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result, &attach); err != nil {
		return fmt.Errorf("parse attach: %w", err)
	}
	c.sessionID = attach.SessionID
	return nil
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
		c.sessionID = ""
	}
}

// DispatchScroll sends a pixel-precise mouseWheel event via CDP.
// modifiers is a bitmask: Alt=1, Ctrl=2, Meta=4, Shift=8.
func (c *Client) DispatchScroll(x, y int, deltaX, deltaY float64, modifiers int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := c.connect(ctx); err != nil {
		return err
	}

	params := map[string]any{
		"type":   "mouseWheel",
		"x":      x,
		"y":      y,
		"deltaX": deltaX,
		"deltaY": deltaY,
	}
	if modifiers != 0 {
		params["modifiers"] = modifiers
	}

	_, err := c.send(ctx, "Input.dispatchMouseEvent", params, c.sessionID)
	if err != nil {
		c.conn.Close()
		c.conn = nil
		c.sessionID = ""
		return err
	}
	return nil
}
