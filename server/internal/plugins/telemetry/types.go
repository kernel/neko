package telemetry

import (
	"regexp"
	"sync"
)

const PluginName = "telemetry"

const (
	// eventLiveViewFailed is published when a live-view capture pipeline cannot
	// start, so the viewer connects and never receives a frame.
	eventLiveViewFailed = "live_view_failed"

	categoryConnection = "connection"

	// Codes match the vocabulary the dashboard already classifies on, so the
	// control plane does not need a second taxonomy.
	codeGPUEncoderUnavailable = "gpu_encoder_unavailable"
	codePipelineFailed        = "live_view_pipeline_failed"
)

// Matches the strings GStreamer and the NVIDIA stack produce when the encoder
// cannot be created, which is what a live view hits when the GPU is already
// holding an encoder session for the replay recorder.
var gpuEncoderFailurePattern = regexp.MustCompile(
	`(?i)cuctxcreate failed|failed to create cuda context|cuda_error_out_of_memory|insufficient memory on nvh264enc|nvh264enc|nvenc|element plugin couldn't be loaded`,
)

func classifyPipelineError(err error) string {
	if err == nil {
		return codePipelineFailed
	}
	if gpuEncoderFailurePattern.MatchString(err.Error()) {
		return codeGPUEncoderUnavailable
	}
	return codePipelineFailed
}

var (
	reporterMu sync.RWMutex
	reporter   func(sessionID, code, message string)
)

func setReporter(fn func(sessionID, code, message string)) {
	reporterMu.Lock()
	defer reporterMu.Unlock()
	reporter = fn
}

func currentReporter() func(sessionID, code, message string) {
	reporterMu.RLock()
	defer reporterMu.RUnlock()
	return reporter
}

// ReportLiveViewFailure records a live-view pipeline failure for the session that
// asked for the stream. The websocket handler owns both the session and the error,
// so it reports through this hook instead of holding a plugin reference. No-op
// when the plugin is disabled or has not started.
func ReportLiveViewFailure(sessionID string, err error) {
	if sessionID == "" || err == nil {
		return
	}
	fn := currentReporter()
	if fn == nil {
		return
	}
	fn(sessionID, classifyPipelineError(err), err.Error())
}
