package benchmark

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/m1k1o/neko/server/internal/benchmarks"
	"github.com/m1k1o/neko/server/pkg/types"
	"github.com/m1k1o/neko/server/pkg/utils"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type BenchmarkHandlerCtx struct {
	logger    zerolog.Logger
	collector *benchmarks.WebRTCStatsCollector
}

func New(collector *benchmarks.WebRTCStatsCollector) *BenchmarkHandlerCtx {
	return &BenchmarkHandlerCtx{
		logger:    log.With().Str("module", "benchmark-api").Logger(),
		collector: collector,
	}
}

func (h *BenchmarkHandlerCtx) Route(r types.Router) {
	// Internal benchmark endpoints (unauthenticated)
	r.Post("/start", h.StartBenchmark)
}

// StartBenchmarkRequest represents the benchmark start request
type StartBenchmarkRequest struct {
	Duration int `json:"duration"` // Duration in seconds
}

// StartBenchmarkResponse represents the benchmark start response
type StartBenchmarkResponse struct {
	Status   string `json:"status"`
	Duration int    `json:"duration"`
}

// StartBenchmark handles POST /internal/benchmark/start
func (h *BenchmarkHandlerCtx) StartBenchmark(w http.ResponseWriter, r *http.Request) error {
	// Parse duration from query parameter
	durationParam := r.URL.Query().Get("duration")
	duration := 10 // default 10 seconds

	if durationParam != "" {
		if d, err := strconv.Atoi(durationParam); err == nil && d > 0 && d <= 60 {
			duration = d
		}
	}

	h.logger.Info().
		Int("duration", duration).
		Msg("starting WebRTC benchmark")

	// Run benchmark collection in background
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(duration+5)*time.Second)
		defer cancel()

		stats, err := h.collector.CollectStats(ctx, time.Duration(duration)*time.Second)
		if err != nil {
			h.logger.Error().Err(err).Msg("benchmark collection failed")
			return
		}

		// Export stats to file for kernel-images to read
		if err := h.collector.ExportStats(stats); err != nil {
			h.logger.Error().Err(err).Msg("failed to export benchmark stats")
			return
		}

		h.logger.Info().
			Float64("avg_fps", stats.FrameRateFPS.Achieved).
			Int("viewers", stats.ConcurrentViewers).
			Msg("benchmark completed and exported")
	}()

	// Return immediate response
	response := StartBenchmarkResponse{
		Status:   "started",
		Duration: duration,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		return utils.HttpInternalServerError().WithInternalErr(err)
	}

	return nil
}
