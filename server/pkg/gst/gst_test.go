package gst

import (
	"strings"
	"testing"
	"time"
)

func TestIsMissingNVENCElementError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		pipelineStr string
		msg         string
		want        bool
	}{
		{
			name:        "missing nvh264enc in gpu pipeline",
			pipelineStr: "ximagesrc ! cudaupload ! nvh264enc name=encoder ! appsink name=appsink",
			msg:         `no element "nvh264enc"`,
			want:        true,
		},
		{
			name:        "alternate plugin wording",
			pipelineStr: "ximagesrc ! cudaupload ! nvh264enc name=encoder ! appsink name=appsink",
			msg:         "No such element or plugin 'nvh264enc'",
			want:        true,
		},
		{
			name:        "unrelated encoder",
			pipelineStr: "ximagesrc ! x264enc name=encoder ! appsink name=appsink",
			msg:         `no element "x264enc"`,
		},
		{
			name:        "other nvh264enc error",
			pipelineStr: "ximagesrc ! cudaupload ! nvh264enc name=encoder ! appsink name=appsink",
			msg:         "could not link cudaupload to nvh264enc",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isMissingNVENCElementError(tt.pipelineStr, tt.msg); got != tt.want {
				t.Fatalf("isMissingNVENCElementError(%q, %q) = %v, want %v", tt.pipelineStr, tt.msg, got, tt.want)
			}
		})
	}
}

func TestCreatePipelineReleasesLockBeforeSlowCUDAProbe(t *testing.T) {
	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	pipelineDone := make(chan error, 1)

	go func() {
		_, err := createPipeline("nvh264enc_missing", func() cudaProbeResult {
			close(probeStarted)
			<-releaseProbe
			return cudaProbeResult{code: cudaErrorOutOfMemory, stage: "creating a CUDA context", name: "CUDA_ERROR_OUT_OF_MEMORY"}
		})
		pipelineDone <- err
	}()

	select {
	case <-probeStarted:
	case err := <-pipelineDone:
		t.Fatalf("pipeline creation returned before running probe: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for CUDA probe")
	}

	lockAcquired := make(chan struct{})
	go func() {
		pipelinesLock.Lock()
		pipelinesLock.Unlock()
		close(lockAcquired)
	}()

	select {
	case <-lockAcquired:
		close(releaseProbe)
	case <-time.After(time.Second):
		close(releaseProbe)
		<-pipelineDone
		<-lockAcquired
		t.Fatal("pipeline lock remained held during CUDA probe")
	}

	if err := <-pipelineDone; err == nil {
		t.Fatal("createPipeline() error = nil, want missing element error")
	}
}

func TestNVENCFailureDetail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		probe cudaProbeResult
		want  string
	}{
		{
			name:  "driver library unavailable",
			probe: cudaProbeResult{code: cudaDriverLibraryUnavailable},
			want:  "CUDA driver library is unavailable",
		},
		{
			name:  "driver symbols unavailable",
			probe: cudaProbeResult{code: cudaDriverSymbolUnavailable},
			want:  "required CUDA driver symbols are unavailable",
		},
		{
			name:  "cuda succeeds",
			probe: cudaProbeResult{code: cudaSuccess},
			want:  "CUDA context probe succeeded",
		},
		{
			name:  "out of memory",
			probe: cudaProbeResult{code: cudaErrorOutOfMemory, stage: "creating a CUDA context", name: "CUDA_ERROR_OUT_OF_MEMORY"},
			want:  "CUDA_ERROR_OUT_OF_MEMORY (2) while creating a CUDA context. GPU memory is exhausted",
		},
		{
			name:  "no device",
			probe: cudaProbeResult{code: cudaErrorNoDevice, stage: "querying CUDA devices", name: "CUDA_ERROR_NO_DEVICE"},
			want:  "CUDA_ERROR_NO_DEVICE (100) while querying CUDA devices; no CUDA-capable GPU is available",
		},
		{
			name:  "other cuda failure",
			probe: cudaProbeResult{code: 999, stage: "initializing CUDA", name: "CUDA_ERROR_UNKNOWN"},
			want:  "CUDA_ERROR_UNKNOWN (999) while initializing CUDA",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := nvencFailureDetail(tt.probe)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("nvencFailureDetail() = %q, want substring %q", got, tt.want)
			}
		})
	}
}
