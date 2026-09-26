package handler

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/m1k1o/neko/server/pkg/types"
	"github.com/m1k1o/neko/server/pkg/types/message"
)

const (
	BenchmarkStatsPath = "/tmp/neko_webrtc_benchmark.json"
)

var (
	benchmarkStatsMu      sync.RWMutex
	lastBenchmarkStats    *message.BenchmarkWebRTCStats
	lastBenchmarkStatsTime time.Time
)

// benchmarkWebRTCStats receives WebRTC stats from the client and writes them to a file
// for the kernel-images server to read.
func (h *MessageHandlerCtx) benchmarkWebRTCStats(session types.Session, payload *message.BenchmarkWebRTCStats) error {
	h.logger.Debug().
		Str("session_id", session.ID()).
		Msg("received WebRTC benchmark stats from client")

	// Store stats in memory (for fallback/debug)
	benchmarkStatsMu.Lock()
	lastBenchmarkStats = payload
	lastBenchmarkStatsTime = time.Now()
	benchmarkStatsMu.Unlock()

	// Write stats to file for kernel-images to read
	if err := writeBenchmarkStatsToFile(payload); err != nil {
		h.logger.Error().
			Err(err).
			Msg("failed to write benchmark stats to file")
		return err
	}

	h.logger.Debug().
		Str("path", BenchmarkStatsPath).
		Msg("wrote WebRTC benchmark stats to file")

	return nil
}

// writeBenchmarkStatsToFile writes the benchmark stats to a JSON file
func writeBenchmarkStatsToFile(stats *message.BenchmarkWebRTCStats) error {
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(BenchmarkStatsPath, data, 0644)
}

// GetLastBenchmarkStats returns the last received benchmark stats (for debugging/fallback)
func GetLastBenchmarkStats() (*message.BenchmarkWebRTCStats, time.Time) {
	benchmarkStatsMu.RLock()
	defer benchmarkStatsMu.RUnlock()
	return lastBenchmarkStats, lastBenchmarkStatsTime
}
