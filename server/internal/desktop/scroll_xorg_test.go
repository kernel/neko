package desktop

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/m1k1o/neko/server/internal/config"
	"github.com/m1k1o/neko/server/pkg/xinput"
	"github.com/m1k1o/neko/server/pkg/xorg"
)

type scrollButtonEvent struct {
	button uint32
	state  uint32
}

type scrollObserver struct {
	stdin io.WriteCloser
	lines <-chan string
}

func (observer *scrollObserver) readLine(t *testing.T) string {
	t.Helper()
	select {
	case line, ok := <-observer.lines:
		if !ok {
			t.Fatal("Xorg scroll observer exited before replying")
		}
		return line
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Xorg scroll observer")
		return ""
	}
}

func (observer *scrollObserver) collect(t *testing.T) []scrollButtonEvent {
	t.Helper()
	if _, err := io.WriteString(observer.stdin, "collect\n"); err != nil {
		t.Fatalf("request scroll events: %v", err)
	}
	var events []scrollButtonEvent
	for {
		line := observer.readLine(t)
		if line == "DONE" {
			return events
		}
		var event scrollButtonEvent
		if n, err := fmt.Sscanf(line, "BUTTON %d %d", &event.button, &event.state); err != nil || n != 2 {
			t.Fatalf("invalid observer reply %q: %v", line, err)
		}
		events = append(events, event)
	}
}

func startScrollObserver(t *testing.T, display string) *scrollObserver {
	t.Helper()
	flags, err := exec.Command("pkg-config", "--cflags", "--libs", "x11").CombinedOutput()
	if err != nil {
		t.Fatalf("find X11 development files for observer: %v\n%s", err, flags)
	}
	compiler := strings.Fields(os.Getenv("CC"))
	if len(compiler) == 0 {
		compiler = []string{"cc"}
	}
	binary := filepath.Join(t.TempDir(), "scroll_observer")
	args := append(compiler[1:], "-std=c99", "-Wall", "-Wextra", "-Werror", "testdata/scroll_observer.c", "-o", binary)
	args = append(args, strings.Fields(string(flags))...)
	if output, err := exec.Command(compiler[0], args...).CombinedOutput(); err != nil {
		t.Fatalf("compile scroll observer: %v\n%s", err, output)
	}

	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binary, display)
	command.Stderr = os.Stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("start scroll observer: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		cancel()
		_ = command.Wait()
	})
	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	observer := &scrollObserver{stdin: stdin, lines: lines}
	if line := observer.readLine(t); line != "READY" {
		t.Fatalf("unexpected observer startup reply: %q", line)
	}
	return observer
}

type successfulScrollDriver struct {
	xinput.Driver
	calls [][2]int32
}

func (driver *successfulScrollDriver) Scroll(deltaX, deltaY int32) error {
	driver.calls = append(driver.calls, [2]int32{deltaX, deltaY})
	return nil
}

// TestScrollXorgIntegration injects real input into an explicitly selected,
// disposable X server. It must never default to the user's desktop display.
// Run with NEKO_XORG_TEST_DISPLAY=:99 go test ./internal/desktop -run TestScrollXorgIntegration.
// The display needs XTEST; cc, pkg-config and X11 development files are needed
// to build the observer. Ordinary go test runs skip this integration test.
func TestScrollXorgIntegration(t *testing.T) {
	display := os.Getenv("NEKO_XORG_TEST_DISPLAY")
	if display == "" {
		t.Skip("set NEKO_XORG_TEST_DISPLAY to an isolated X server to test actual scroll events")
	}
	observer := startScrollObserver(t, display)
	if xorg.DisplayOpen(display) {
		t.Fatalf("open test display %q", display)
	}
	t.Cleanup(xorg.DisplayClose)
	t.Cleanup(func() {
		xorg.ResetKeys()
		xorg.SetKeyboardModifier(xorg.KbdModControl, false)
	})

	manager := &DesktopManagerCtx{
		config: &config.Desktop{UseInputDriver: true},
		input:  xinput.NewDummy(), // Scroll returns a deterministic driver error.
		logger: zerolog.Nop(),
	}
	reset := func(t *testing.T) {
		t.Helper()
		xorg.ResetKeys()
		xorg.SetKeyboardModifier(xorg.KbdModControl, false)
		manager.config.UseInputDriver = true
		manager.input = xinput.NewDummy()
		observer.collect(t)
	}
	expect := func(t *testing.T, control bool, buttons ...uint32) {
		t.Helper()
		events := observer.collect(t)
		if len(events) != len(buttons) {
			t.Fatalf("got wheel events %v; want buttons %v (Control=%t)", events, buttons, control)
		}
		for i, event := range events {
			if event.button != buttons[i] || (event.state&uint32(xorg.KbdModControl) != 0) != control {
				t.Fatalf("event %d = %+v; want button %d, Control=%t", i, event, buttons[i], control)
			}
		}
	}
	assertControlReleased := func(t *testing.T) {
		t.Helper()
		if xorg.GetKeyboardModifiers()&xorg.KbdModControl != 0 {
			t.Fatal("temporary Control modifier remains held after scroll")
		}
	}

	t.Run("fallback converts one notch", func(t *testing.T) {
		reset(t)
		manager.Scroll(0, 120, false)
		expect(t, false, 5)
	})
	t.Run("zero and fractional deltas accumulate", func(t *testing.T) {
		reset(t)
		manager.Scroll(0, 0, false)
		manager.Scroll(0, 60, false)
		expect(t, false)
		manager.Scroll(0, 60, false)
		expect(t, false, 5)
	})
	t.Run("sign and axes are independent", func(t *testing.T) {
		reset(t)
		manager.Scroll(130, -250, false)
		expect(t, false, 4, 4, 7)
		manager.Scroll(110, -110, false)
		expect(t, false, 4, 7)
		manager.Scroll(-120, 120, false)
		expect(t, false, 5, 6)
	})
	t.Run("direction reversal cancels pending units", func(t *testing.T) {
		reset(t)
		manager.Scroll(0, 90, false)
		manager.Scroll(0, -120, false)
		expect(t, false)
		manager.Scroll(0, -90, false)
		expect(t, false, 4)
	})
	t.Run("coalesced gesture retains remainder", func(t *testing.T) {
		reset(t)
		manager.Scroll(0, 1500, false)
		expect(t, false, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5)
		manager.Scroll(0, 60, false)
		expect(t, false, 5)
	})
	t.Run("explicit Control keeps separate residual", func(t *testing.T) {
		reset(t)
		manager.Scroll(0, 110, false)
		manager.Scroll(0, 30, true)
		expect(t, false)
		assertControlReleased(t)
		manager.Scroll(0, 10, false)
		expect(t, false, 5)
		manager.Scroll(0, 90, true)
		expect(t, true, 5)
		assertControlReleased(t)
	})
	t.Run("physical Control keeps separate residual", func(t *testing.T) {
		reset(t)
		manager.Scroll(0, 110, false)
		const controlLeft = 0xffe3 // XK_Control_L, sent separately by legacy clients.
		if err := manager.KeyDown(controlLeft); err != nil {
			t.Fatal(err)
		}
		manager.Scroll(0, 30, false)
		expect(t, false)
		if err := manager.KeyUp(controlLeft); err != nil {
			t.Fatal(err)
		}
		manager.Scroll(0, 10, false)
		expect(t, false, 5)
		if err := manager.KeyDown(controlLeft); err != nil {
			t.Fatal(err)
		}
		manager.Scroll(0, 90, false)
		expect(t, true, 5)
		if err := manager.KeyUp(controlLeft); err != nil {
			t.Fatal(err)
		}
		assertControlReleased(t)
	})
	t.Run("reset discards both residuals", func(t *testing.T) {
		reset(t)
		manager.Scroll(0, 100, false)
		manager.Scroll(0, 100, true)
		manager.ResetKeys()
		manager.Scroll(0, 20, false)
		manager.Scroll(0, 20, true)
		expect(t, false)
		manager.Scroll(0, 100, false)
		expect(t, false, 5)
		manager.Scroll(0, 100, true)
		expect(t, true, 5)
		assertControlReleased(t)
	})
	t.Run("driver success preserves raw scroll units", func(t *testing.T) {
		reset(t)
		driver := &successfulScrollDriver{Driver: xinput.NewDummy()}
		manager.input = driver
		manager.Scroll(-240, 1500, false)
		manager.Scroll(120, -120, true)
		expect(t, false)
		assertControlReleased(t)
		want := [][2]int32{{-240, 1500}, {120, -120}}
		if len(driver.calls) != len(want) {
			t.Fatalf("driver calls = %v; want %v", driver.calls, want)
		}
		for i := range want {
			if driver.calls[i] != want[i] {
				t.Fatalf("driver call %d = %v; want %v", i, driver.calls[i], want[i])
			}
		}
		// Successful driver deltas must not seed the fallback's accumulator.
		manager.input = xinput.NewDummy()
		manager.Scroll(0, 60, false)
		expect(t, false)
	})
	t.Run("driver disabled preserves click-count semantics", func(t *testing.T) {
		reset(t)
		manager.config.UseInputDriver = false
		manager.Scroll(-1, 2, false)
		expect(t, false, 5, 5, 6)
		manager.Scroll(0, -1, true)
		expect(t, true, 4)
		assertControlReleased(t)
	})
}
