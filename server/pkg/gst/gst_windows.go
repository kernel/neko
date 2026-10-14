package gst

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/m1k1o/neko/server/pkg/types"
	"github.com/rs/zerolog/log"
)

type pipeline struct {
	src     string
	sample  chan types.Sample
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	started bool
}

func CreatePipeline(src string) (Pipeline, error) {
	if !strings.Contains(src, "windowsdesktop") && !strings.Contains(src, "windowssilence") {
		return nil, fmt.Errorf("unsupported Windows capture pipeline %q", src)
	}

	return &pipeline{
		src:    src,
		sample: make(chan types.Sample),
	}, nil
}

func (p *pipeline) Src() string                             { return p.src }
func (p *pipeline) Sample() chan types.Sample               { return p.sample }
func (p *pipeline) AttachAppsink(string)                    {}
func (p *pipeline) AttachAppsrc(string)                     {}
func (p *pipeline) Push([]byte)                             {}
func (p *pipeline) SetPropInt(string, string, int) bool     { return false }
func (p *pipeline) SetCapsFramerate(string, int, int) bool  { return false }
func (p *pipeline) SetCapsResolution(string, int, int) bool { return false }
func (p *pipeline) EmitVideoKeyframe() bool                 { return false }

func (p *pipeline) Play() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.started = true
	p.wg.Add(1)
	if strings.Contains(p.src, "windowssilence") {
		go p.streamSilence(ctx)
	} else {
		go p.streamDesktop(ctx)
	}
}

func (p *pipeline) Pause() {
	p.stop()
	p.wg.Wait()
}

func (p *pipeline) Destroy() {
	p.stop()
	p.wg.Wait()
	close(p.sample)
}

func (p *pipeline) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.started = false
}

func (p *pipeline) streamSilence(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case timestamp := <-ticker.C:
			p.sample <- types.Sample{
				Data:      []byte{0xf8, 0xff, 0xfe},
				Length:    3,
				Timestamp: timestamp,
				Duration:  20 * time.Millisecond,
			}
		}
	}
}

func (p *pipeline) streamDesktop(ctx context.Context) {
	defer p.wg.Done()

	cmd := exec.CommandContext(ctx, "ffmpeg.exe",
		"-hide_banner", "-loglevel", "warning",
		"-f", "gdigrab", "-framerate", "25", "-draw_mouse", "1", "-i", "desktop",
		"-an", "-c:v", "libvpx", "-deadline", "realtime", "-cpu-used", "8",
		"-b:v", "2500k", "-g", "25", "-pix_fmt", "yuv420p", "-f", "ivf", "pipe:1",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Error().Err(err).Msg("create ffmpeg stdout pipe")
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Error().Err(err).Msg("create ffmpeg stderr pipe")
		return
	}
	if err := cmd.Start(); err != nil {
		log.Error().Err(err).Msg("start Windows desktop capture")
		return
	}

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Warn().Str("module", "capture").Msg(scanner.Text())
		}
	}()

	if err := p.readIVF(ctx, stdout); err != nil && ctx.Err() == nil {
		log.Error().Err(err).Msg("read Windows desktop capture")
	}
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		log.Error().Err(err).Msg("Windows desktop capture exited")
	}
}

func (p *pipeline) readIVF(ctx context.Context, r io.Reader) error {
	header := make([]byte, 32)
	if _, err := io.ReadFull(r, header); err != nil {
		return err
	}
	if string(header[:4]) != "DKIF" {
		return fmt.Errorf("unexpected IVF signature %q", header[:4])
	}

	frameHeader := make([]byte, 12)
	for {
		if _, err := io.ReadFull(r, frameHeader); err != nil {
			return err
		}
		size := binary.LittleEndian.Uint32(frameHeader[:4])
		if size == 0 || size > 16<<20 {
			return fmt.Errorf("invalid IVF frame size %d", size)
		}
		frame := make([]byte, size)
		if _, err := io.ReadFull(r, frame); err != nil {
			return err
		}

		sample := types.Sample{
			Data:      frame,
			Length:    len(frame),
			Timestamp: time.Now(),
			Duration:  40 * time.Millisecond,
			DeltaUnit: frame[0]&1 != 0,
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case p.sample <- sample:
		}
	}
}

func CheckPlugins([]string) error { return nil }
