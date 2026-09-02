package gst

import (
	"strings"
	"testing"
)

func TestAnnotatePipelineError(t *testing.T) {
	t.Parallel()

	const hint = "live view could not initialize NVENC/CUDA"

	tests := []struct {
		name        string
		pipelineStr string
		msg         string
		wantHint    bool
	}{
		{
			name:        "adds hint for missing nvh264enc in gpu pipeline",
			pipelineStr: "ximagesrc ! cudaupload ! nvh264enc name=encoder ! appsink name=appsink",
			msg:         `no element "nvh264enc"`,
			wantHint:    true,
		},
		{
			name:        "adds hint for plugin wording",
			pipelineStr: "ximagesrc ! cudaupload ! nvh264enc name=encoder ! appsink name=appsink",
			msg:         "No such element or plugin 'nvh264enc'",
			wantHint:    true,
		},
		{
			name:        "leaves unrelated encoder errors alone",
			pipelineStr: "ximagesrc ! x264enc name=encoder ! appsink name=appsink",
			msg:         `no element "x264enc"`,
			wantHint:    false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := annotatePipelineError(tt.pipelineStr, tt.msg)
			hasHint := got != tt.msg

			if hasHint != tt.wantHint {
				t.Fatalf("annotatePipelineError(%q, %q) hint=%v want %v; got %q", tt.pipelineStr, tt.msg, hasHint, tt.wantHint, got)
			}

			if tt.wantHint && !strings.Contains(got, hint) {
				t.Fatalf("annotatePipelineError(%q, %q) = %q, want substring %q", tt.pipelineStr, tt.msg, got, hint)
			}
		})
	}
}
