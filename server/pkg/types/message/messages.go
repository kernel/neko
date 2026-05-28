package message

import (
	"github.com/pion/webrtc/v3"

	"github.com/m1k1o/neko/server/pkg/types"
)

/////////////////////////////
// System
/////////////////////////////

type SystemWebRTC struct {
	Videos []string `json:"videos"`
}

type SystemInit struct {
	SessionId         string                 `json:"session_id"`
	ControlHost       ControlHost            `json:"control_host"`
	ScreenSize        types.ScreenSize       `json:"screen_size"`
	Sessions          map[string]SessionData `json:"sessions"`
	Settings          types.Settings         `json:"settings"`
	TouchEvents       bool                   `json:"touch_events"`
	ScreencastEnabled bool                   `json:"screencast_enabled"`
	WebRTC            SystemWebRTC           `json:"webrtc"`
}

type SystemAdmin struct {
	ScreenSizesList []types.ScreenSize `json:"screen_sizes_list"`
	BroadcastStatus BroadcastStatus    `json:"broadcast_status"`
}

type SystemLogs = []SystemLog

type SystemLog struct {
	Level   string         `json:"level"`
	Fields  map[string]any `json:"fields"`
	Message string         `json:"message"`
}

type SystemDisconnect struct {
	Message string `json:"message"`
}

type SystemSettingsUpdate struct {
	ID string `json:"id"`
	types.Settings
}

// SystemPong is sent by the server as a direct response to CLIENT_HEARTBEAT
type SystemPong struct {
	Timestamp int64 `json:"timestamp"` // Unix ms
}

// SystemBenchmarkReady is sent when benchmark collection is complete
type SystemBenchmarkReady struct {
	Timestamp int64 `json:"timestamp"` // Unix ms
}

/////////////////////////////
// Signal
/////////////////////////////

type SignalRequest struct {
	Video types.PeerVideoRequest `json:"video"`
	Audio types.PeerAudioRequest `json:"audio"`

	Auto bool `json:"auto"` // TODO: Remove this
}

type SignalProvide struct {
	SDP        string            `json:"sdp"`
	ICEServers []types.ICEServer `json:"iceservers"`

	Video types.PeerVideo `json:"video"`
	Audio types.PeerAudio `json:"audio"`
}

type SignalCandidate struct {
	webrtc.ICECandidateInit
}

type SignalDescription struct {
	SDP string `json:"sdp"`
}

type SignalVideo struct {
	types.PeerVideoRequest
}

type SignalAudio struct {
	types.PeerAudioRequest
}

/////////////////////////////
// Session
/////////////////////////////

type SessionID struct {
	ID string `json:"id"`
}

type MemberProfile struct {
	ID string `json:"id"`
	types.MemberProfile
}

type SessionState struct {
	ID string `json:"id"`
	types.SessionState
}

type SessionData struct {
	ID      string              `json:"id"`
	Profile types.MemberProfile `json:"profile"`
	State   types.SessionState  `json:"state"`
}

type SessionCursors struct {
	ID      string         `json:"id"`
	Cursors []types.Cursor `json:"cursors"`
}

/////////////////////////////
// Control
/////////////////////////////

type ControlHost struct {
	ID      string `json:"id"`
	HasHost bool   `json:"has_host"`
	HostID  string `json:"host_id,omitempty"`
}

type ControlScroll struct {
	// TOOD: remove this once the client is fixed
	X int `json:"x"`
	Y int `json:"y"`

	DeltaX     int  `json:"delta_x"`
	DeltaY     int  `json:"delta_y"`
	ControlKey bool `json:"control_key"`
}

type ControlPos struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type ControlButton struct {
	*ControlPos
	Code uint32 `json:"code"`
}

type ControlKey struct {
	*ControlPos
	Keysym uint32 `json:"keysym"`
}

type ControlTouch struct {
	*ControlPos
	TouchId  uint32 `json:"touch_id"`
	Pressure uint8  `json:"pressure"`
}

/////////////////////////////
// Screen
/////////////////////////////

type ScreenSize struct {
	types.ScreenSize
}

type ScreenSizeUpdate struct {
	ID string `json:"id"`
	types.ScreenSize
}

/////////////////////////////
// Clipboard
/////////////////////////////

type ClipboardData struct {
	Text string `json:"text"`
}

/////////////////////////////
// Keyboard
/////////////////////////////

type KeyboardMap struct {
	types.KeyboardMap
}

type KeyboardModifiers struct {
	types.KeyboardModifiers
}

/////////////////////////////
// Broadcast
/////////////////////////////

type BroadcastStatus struct {
	IsActive bool   `json:"is_active"`
	URL      string `json:"url,omitempty"`
}

/////////////////////////////
// Send (opaque comunication channel)
/////////////////////////////

type SendUnicast struct {
	Sender   string `json:"sender"`
	Receiver string `json:"receiver"`
	Subject  string `json:"subject"`
	Body     any    `json:"body"`
}

type SendBroadcast struct {
	Sender  string `json:"sender"`
	Subject string `json:"subject"`
	Body    any    `json:"body"`
}

/////////////////////////////
// Benchmark
/////////////////////////////

type BenchmarkWebRTCStats struct {
	Timestamp          string                          `json:"timestamp"`
	ConnectionState    string                          `json:"connection_state"`
	IceConnectionState string                          `json:"ice_connection_state"`
	FrameRateFPS       BenchmarkFrameRateMetrics       `json:"frame_rate_fps"`
	FrameLatencyMS     BenchmarkLatencyMetrics         `json:"frame_latency_ms"`
	BitrateKbps        BenchmarkBitrateMetrics         `json:"bitrate_kbps"`
	Packets            BenchmarkPacketMetrics          `json:"packets"`
	Frames             BenchmarkFrameMetrics           `json:"frames"`
	JitterMS           BenchmarkJitterMetrics          `json:"jitter_ms"`
	Network            BenchmarkNetworkMetrics         `json:"network"`
	Codecs             BenchmarkCodecMetrics           `json:"codecs"`
	Resolution         BenchmarkResolutionMetrics      `json:"resolution"`
	ConcurrentViewers  int                             `json:"concurrent_viewers"`
}

type BenchmarkFrameRateMetrics struct {
	Target   float64 `json:"target"`
	Achieved float64 `json:"achieved"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
}

type BenchmarkLatencyMetrics struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

type BenchmarkBitrateMetrics struct {
	Video float64 `json:"video"`
	Audio float64 `json:"audio"`
	Total float64 `json:"total"`
}

type BenchmarkPacketMetrics struct {
	VideoReceived int64   `json:"video_received"`
	VideoLost     int64   `json:"video_lost"`
	AudioReceived int64   `json:"audio_received"`
	AudioLost     int64   `json:"audio_lost"`
	LossPercent   float64 `json:"loss_percent"`
}

type BenchmarkFrameMetrics struct {
	Received        int64 `json:"received"`
	Dropped         int64 `json:"dropped"`
	Decoded         int64 `json:"decoded"`
	Corrupted       int64 `json:"corrupted"`
	KeyFramesDecoded int64 `json:"key_frames_decoded"`
}

type BenchmarkJitterMetrics struct {
	Video float64 `json:"video"`
	Audio float64 `json:"audio"`
}

type BenchmarkNetworkMetrics struct {
	RTTMS                      float64 `json:"rtt_ms"`
	AvailableOutgoingBitrateKbps float64 `json:"available_outgoing_bitrate_kbps"`
	BytesReceived              int64   `json:"bytes_received"`
	BytesSent                  int64   `json:"bytes_sent"`
}

type BenchmarkCodecMetrics struct {
	Video string `json:"video"`
	Audio string `json:"audio"`
}

type BenchmarkResolutionMetrics struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}
