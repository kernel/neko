package capture

import (
	"reflect"
	"testing"

	"github.com/m1k1o/neko/server/pkg/types"
)

func TestWaylandFrameSourceCommand(t *testing.T) {
	source := newWaylandFrameSource("wf-recorder", types.ScreenSize{
		Width:  1920,
		Height: 1080,
		Rate:   25,
	})

	got := source.command().Args
	want := []string{
		"wf-recorder",
		"--no-damage",
		"--no-dmabuf",
		"--framerate", "25",
		"--muxer", "rawvideo",
		"--codec", "rawvideo",
		"--pixel-format", "bgr0",
		"--file", "/dev/stdout",
		"--overwrite",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command args = %#v, want %#v", got, want)
	}
}

func TestWaylandFrameSourceDefaultsFrameRate(t *testing.T) {
	source := newWaylandFrameSource("wf-recorder", types.ScreenSize{
		Width:  10,
		Height: 20,
	})

	if source.fps != 25 {
		t.Fatalf("fps = %d, want 25", source.fps)
	}
	if source.frameSize() != 800 {
		t.Fatalf("frame size = %d, want 800", source.frameSize())
	}
}
