package capture

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/m1k1o/neko/server/pkg/types"
)

type frameSource interface {
	Start(func([]byte)) error
	Stop()
}

type waylandFrameSource struct {
	recorder string
	width    int
	height   int
	fps      int

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func newWaylandFrameSource(recorder string, screen types.ScreenSize) *waylandFrameSource {
	fps := int(screen.Rate)
	if fps <= 0 {
		fps = 25
	}

	return &waylandFrameSource{
		recorder: recorder,
		width:    screen.Width,
		height:   screen.Height,
		fps:      fps,
	}
}

func (source *waylandFrameSource) frameSize() int {
	return source.width * source.height * 4
}

func (source *waylandFrameSource) args() []string {
	return []string{
		"--no-damage",
		"--no-dmabuf",
		"--framerate", strconv.Itoa(source.fps),
		"--muxer", "rawvideo",
		"--codec", "rawvideo",
		"--pixel-format", "bgr0",
		"--file", "/dev/stdout",
		"--overwrite",
	}
}

func (source *waylandFrameSource) command() *exec.Cmd {
	return exec.Command(source.recorder, source.args()...)
}

func (source *waylandFrameSource) Start(push func([]byte)) error {
	if push == nil {
		return fmt.Errorf("frame push callback is required")
	}
	if source.recorder == "" {
		return fmt.Errorf("Wayland recorder executable is required")
	}
	if source.width <= 0 || source.height <= 0 {
		return fmt.Errorf("invalid Wayland output size: %dx%d", source.width, source.height)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, source.recorder, source.args()...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("create Wayland recorder pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start Wayland recorder: %w", err)
	}

	done := make(chan struct{})
	source.mu.Lock()
	source.cancel = cancel
	source.done = done
	source.mu.Unlock()

	go func() {
		defer close(done)
		defer stdout.Close()

		frame := make([]byte, source.frameSize())
		for {
			if _, err := io.ReadFull(stdout, frame); err != nil {
				if err != io.EOF && err != io.ErrUnexpectedEOF {
					log.Warn().Err(err).Msg("Wayland recorder stopped while reading a frame")
				}
				break
			}

			push(frame)
		}

		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			log.Warn().Err(err).Msg("Wayland recorder exited")
		}
	}()

	return nil
}

func (source *waylandFrameSource) Stop() {
	source.mu.Lock()
	cancel := source.cancel
	done := source.done
	source.cancel = nil
	source.done = nil
	source.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	<-done
}
