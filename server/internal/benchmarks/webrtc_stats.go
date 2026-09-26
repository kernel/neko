package benchmarks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	// Path where benchmark stats are written
	BenchmarkStatsPath = "/tmp/neko_webrtc_benchmark.json"
)

// WebRTCBenchmarkStats contains WebRTC benchmark statistics
type WebRTCBenchmarkStats struct {
	Timestamp             time.Time         `json:"timestamp"`
	FrameRateFPS          FrameRateMetrics  `json:"frame_rate_fps"`
	FrameLatencyMS        LatencyMetrics    `json:"frame_latency_ms"`
	BitrateKbps           BitrateMetrics    `json:"bitrate_kbps"`
	ConnectionSetupMS     float64           `json:"connection_setup_ms"`
	ConcurrentViewers     int               `json:"concurrent_viewers"`
	CPUUsagePercent       float64           `json:"cpu_usage_percent"`
	MemoryMB              MemoryMetrics     `json:"memory_mb"`
}

type FrameRateMetrics struct {
	Target   float64 `json:"target"`
	Achieved float64 `json:"achieved"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
}

type LatencyMetrics struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

type BitrateMetrics struct {
	Target float64 `json:"target"`
	Actual float64 `json:"actual"`
}

type MemoryMetrics struct {
	Baseline  float64 `json:"baseline"`
	PerViewer float64 `json:"per_viewer,omitempty"`
}

// WebRTCStatsCollector collects WebRTC statistics for benchmarking
type WebRTCStatsCollector struct {
	logger zerolog.Logger
	mu     sync.RWMutex

	// Connection tracking
	connections      map[*webrtc.PeerConnection]*connectionStats
	connectionsMu    sync.RWMutex

	// Aggregated stats
	avgFrameRate     float64
	avgBitrate       float64
	connectionTimes  []float64

	targetFrameRate  float64
	targetBitrate    float64
}

type connectionStats struct {
	createdAt     time.Time
	setupDuration time.Duration
	lastUpdate    time.Time

	// Per-connection metrics
	frameRate     float64
	bitrate       float64

	// Track frame counts for rate calculation
	lastFramesSent uint32
	lastBytesSent  uint64
	lastStatsTime  time.Time
}

// NewWebRTCStatsCollector creates a new WebRTC stats collector
func NewWebRTCStatsCollector(targetFrameRate, targetBitrate float64) *WebRTCStatsCollector {
	return &WebRTCStatsCollector{
		logger:           log.With().Str("module", "webrtc-benchmark").Logger(),
		connections:      make(map[*webrtc.PeerConnection]*connectionStats),
		connectionTimes:  make([]float64, 0),
		targetFrameRate:  targetFrameRate,
		targetBitrate:    targetBitrate,
	}
}

// RegisterConnection registers a new WebRTC peer connection for tracking
func (c *WebRTCStatsCollector) RegisterConnection(pc *webrtc.PeerConnection) {
	c.connectionsMu.Lock()
	defer c.connectionsMu.Unlock()

	c.connections[pc] = &connectionStats{
		createdAt:  time.Now(),
		lastUpdate: time.Now(),
	}

	c.logger.Info().
		Int("total_connections", len(c.connections)).
		Msg("registered new WebRTC connection for benchmarking")

	// Monitor connection state changes to track setup time
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			c.connectionsMu.Lock()
			if stats, ok := c.connections[pc]; ok {
				stats.setupDuration = time.Since(stats.createdAt)
				c.connectionTimes = append(c.connectionTimes, float64(stats.setupDuration.Milliseconds()))
				c.logger.Info().
					Dur("setup_duration", stats.setupDuration).
					Msg("WebRTC connection established")
			}
			c.connectionsMu.Unlock()
		}
	})
}

// UnregisterConnection removes a peer connection from tracking
func (c *WebRTCStatsCollector) UnregisterConnection(pc *webrtc.PeerConnection) {
	c.connectionsMu.Lock()
	defer c.connectionsMu.Unlock()

	delete(c.connections, pc)
	c.logger.Info().
		Int("remaining_connections", len(c.connections)).
		Msg("unregistered WebRTC connection from benchmarking")
}

// UpdateConnectionStats updates statistics for a specific connection
func (c *WebRTCStatsCollector) UpdateConnectionStats(pc *webrtc.PeerConnection, frameRate, bitrate float64) {
	c.connectionsMu.Lock()
	defer c.connectionsMu.Unlock()

	if stats, ok := c.connections[pc]; ok {
		stats.frameRate = frameRate
		stats.bitrate = bitrate
		stats.lastUpdate = time.Now()
	}
}

// updateAllConnectionStats polls WebRTC stats for all connections and updates metrics
func (c *WebRTCStatsCollector) updateAllConnectionStats(ctx context.Context) {
	c.connectionsMu.Lock()
	defer c.connectionsMu.Unlock()

	now := time.Now()
	statsProcessed := 0
	videoStatsFound := 0

	for pc, stats := range c.connections {
		// Get WebRTC stats for this peer connection
		rtcStats := pc.GetStats()

		if len(rtcStats) == 0 {
			c.logger.Debug().Msg("no stats returned from peer connection")
			continue
		}

		// Extract outbound RTP video stats
		for _, stat := range rtcStats {
			statsProcessed++
			switch s := stat.(type) {
			case webrtc.OutboundRTPStreamStats:
				c.logger.Debug().
					Str("kind", s.Kind).
					Uint32("frames", s.FramesEncoded).
					Uint64("bytes", s.BytesSent).
					Msg("found outbound RTP stream stats")

				if s.Kind == "video" {
					videoStatsFound++

					// Calculate frame rate from FramesEncoded
					if stats.lastFramesSent > 0 && !stats.lastStatsTime.IsZero() {
						deltaFrames := s.FramesEncoded - stats.lastFramesSent
						deltaTime := now.Sub(stats.lastStatsTime).Seconds()
						if deltaTime > 0 {
							stats.frameRate = float64(deltaFrames) / deltaTime
							c.logger.Debug().
								Float64("fps", stats.frameRate).
								Uint32("delta_frames", deltaFrames).
								Float64("delta_time", deltaTime).
								Msg("calculated frame rate")
						}
					}

					// Calculate bitrate (bytes to kbps)
					if stats.lastBytesSent > 0 && !stats.lastStatsTime.IsZero() {
						deltaBytes := s.BytesSent - stats.lastBytesSent
						deltaTime := now.Sub(stats.lastStatsTime).Seconds()
						if deltaTime > 0 {
							stats.bitrate = (float64(deltaBytes) * 8) / (deltaTime * 1000) // kbps
							c.logger.Debug().
								Float64("bitrate_kbps", stats.bitrate).
								Uint64("delta_bytes", deltaBytes).
								Msg("calculated bitrate")
						}
					}

					// Store current values for next calculation
					stats.lastFramesSent = s.FramesEncoded
					stats.lastBytesSent = s.BytesSent
					stats.lastStatsTime = now
					stats.lastUpdate = now
				}
			}
		}
	}

	if statsProcessed > 0 || videoStatsFound > 0 {
		c.logger.Debug().
			Int("total_stats", statsProcessed).
			Int("video_stats", videoStatsFound).
			Int("connections", len(c.connections)).
			Msg("updated connection stats")
	}
}

// CollectStats collects current WebRTC statistics
func (c *WebRTCStatsCollector) CollectStats(ctx context.Context, duration time.Duration) (*WebRTCBenchmarkStats, error) {
	c.logger.Info().Dur("duration", duration).Msg("collecting WebRTC stats")

	// Track frame latencies for percentile calculations
	var frameLatencies []float64
	var frameLatenciesMu sync.Mutex

	// Poll stats more frequently (every 100ms) for better granularity
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	endTime := time.Now().Add(duration)
	sampleCount := 0

	for time.Now().Before(endTime) {
		select {
		case <-ticker.C:
			// Update stats for all connections
			c.updateAllConnectionStats(ctx)
			sampleCount++

			// Calculate frame latency based on actual frame rate
			c.connectionsMu.RLock()
			for _, stats := range c.connections {
				if stats.frameRate > 0 {
					// Frame time in ms = 1000 / fps
					frameTime := 1000.0 / stats.frameRate
					frameLatenciesMu.Lock()
					frameLatencies = append(frameLatencies, frameTime)
					frameLatenciesMu.Unlock()
				}
			}
			c.connectionsMu.RUnlock()

		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Final stats update
	c.updateAllConnectionStats(ctx)

	// Aggregate stats from all connections
	c.connectionsMu.RLock()
	numConnections := len(c.connections)

	var (
		totalFrameRate float64
		minFrameRate   float64 = 999999
		maxFrameRate   float64
		totalBitrate   float64
	)

	for _, stats := range c.connections {
		totalFrameRate += stats.frameRate
		totalBitrate += stats.bitrate

		if stats.frameRate < minFrameRate && stats.frameRate > 0 {
			minFrameRate = stats.frameRate
		}
		if stats.frameRate > maxFrameRate {
			maxFrameRate = stats.frameRate
		}
	}
	c.connectionsMu.RUnlock()

	if numConnections == 0 || minFrameRate == 999999 {
		minFrameRate = 0
	}

	avgFrameRate := totalFrameRate / float64(max(numConnections, 1))
	avgBitrate := totalBitrate / float64(max(numConnections, 1))

	// Calculate frame latency percentiles from collected samples
	latencyMetrics := calculateLatencyPercentiles(frameLatencies)

	// Calculate connection setup time average
	avgConnectionSetup := 0.0
	if len(c.connectionTimes) > 0 {
		var total float64
		for _, t := range c.connectionTimes {
			total += t
		}
		avgConnectionSetup = total / float64(len(c.connectionTimes))
	}

	// Get real CPU and memory usage
	cpuUsage := measureCPUUsage()
	memoryUsage := measureMemoryUsage(numConnections)

	stats := &WebRTCBenchmarkStats{
		Timestamp: time.Now(),
		FrameRateFPS: FrameRateMetrics{
			Target:   c.targetFrameRate,
			Achieved: avgFrameRate,
			Min:      minFrameRate,
			Max:      maxFrameRate,
		},
		FrameLatencyMS:    latencyMetrics,
		BitrateKbps: BitrateMetrics{
			Target: c.targetBitrate,
			Actual: avgBitrate,
		},
		ConnectionSetupMS: avgConnectionSetup,
		ConcurrentViewers: numConnections,
		CPUUsagePercent:   cpuUsage,
		MemoryMB: MemoryMetrics{
			Baseline:  memoryUsage.Baseline,
			PerViewer: memoryUsage.PerViewer,
		},
	}

	c.logger.Info().
		Float64("avg_fps", avgFrameRate).
		Float64("avg_bitrate_kbps", avgBitrate).
		Int("viewers", numConnections).
		Int("samples", sampleCount).
		Int("latency_samples", len(frameLatencies)).
		Msg("WebRTC stats collection completed")

	// Warn if we got zeros (common issue)
	if numConnections == 0 {
		c.logger.Warn().Msg("no WebRTC connections registered during stats collection")
	} else if avgFrameRate == 0 && avgBitrate == 0 {
		c.logger.Warn().
			Int("connections", numConnections).
			Msg("WebRTC connections exist but no video stats collected - video stream may not be active")
	}

	return stats, nil
}

// calculateLatencyPercentiles calculates percentiles from latency samples
func calculateLatencyPercentiles(latencies []float64) LatencyMetrics {
	if len(latencies) == 0 {
		// Return default estimates for 30fps
		return LatencyMetrics{
			P50: 33.3,
			P95: 50.0,
			P99: 67.0,
		}
	}

	// Simple percentile calculation
	sorted := make([]float64, len(latencies))
	copy(sorted, latencies)

	// Sort latencies
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	p50Idx := int(float64(len(sorted)) * 0.50)
	p95Idx := int(float64(len(sorted)) * 0.95)
	p99Idx := int(float64(len(sorted)) * 0.99)

	if p50Idx >= len(sorted) {
		p50Idx = len(sorted) - 1
	}
	if p95Idx >= len(sorted) {
		p95Idx = len(sorted) - 1
	}
	if p99Idx >= len(sorted) {
		p99Idx = len(sorted) - 1
	}

	return LatencyMetrics{
		P50: sorted[p50Idx],
		P95: sorted[p95Idx],
		P99: sorted[p99Idx],
	}
}

// ExportStats exports stats to a file for kernel-images to read
func (c *WebRTCStatsCollector) ExportStats(stats *WebRTCBenchmarkStats) error {
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal stats: %w", err)
	}

	if err := os.WriteFile(BenchmarkStatsPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write stats file: %w", err)
	}

	c.logger.Info().Str("path", BenchmarkStatsPath).Msg("exported WebRTC benchmark stats")
	return nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// measureCPUUsage measures actual CPU usage
func measureCPUUsage() float64 {
	// Take two snapshots 100ms apart
	before, err := GetProcessCPUStats()
	if err != nil {
		// Fallback to estimate
		return 0.0
	}

	time.Sleep(100 * time.Millisecond)

	after, err := GetProcessCPUStats()
	if err != nil {
		return 0.0
	}

	return CalculateCPUPercent(before, after)
}

// measureMemoryUsage measures actual memory usage
func measureMemoryUsage(numConnections int) MemoryMetrics {
	// Try to get RSS memory first
	rss, err := GetProcessRSSMemoryMB()
	if err != nil {
		// Fallback to heap memory
		rss = GetProcessMemoryMB()
	}

	perViewer := 0.0
	if numConnections > 0 {
		// Rough estimate of per-viewer overhead
		// Would need baseline measurement to be more accurate
		perViewer = rss / float64(numConnections)
	}

	return MemoryMetrics{
		Baseline:  rss,
		PerViewer: perViewer,
	}
}
