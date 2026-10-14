package desktop

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/kataras/go-events"
	"github.com/m1k1o/neko/server/internal/config"
	"github.com/m1k1o/neko/server/pkg/types"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseLeftDown   = 0x0002
	mouseLeftUp     = 0x0004
	mouseRightDown  = 0x0008
	mouseRightUp    = 0x0010
	mouseMiddleDown = 0x0020
	mouseMiddleUp   = 0x0040
	mouseWheel      = 0x0800
	mouseHWheel     = 0x1000

	keyUp      = 0x0002
	keyUnicode = 0x0004

	clipboardTextPlainTarget = "UTF8_STRING"
	clipboardTextHTMLTarget  = "text/html"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procSetCursorPos     = user32.NewProc("SetCursorPos")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procSendInput        = user32.NewProc("SendInput")
	procVkKeyScanW       = user32.NewProc("VkKeyScanW")
)

type point struct {
	X int32
	Y int32
}

type input struct {
	Type uint32
	_    uint32
	Data [32]byte
}

type mouseInput struct {
	Dx        int32
	Dy        int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type keyboardInput struct {
	VirtualKey uint16
	ScanCode   uint16
	Flags      uint32
	Time       uint32
	ExtraInfo  uintptr
}

type keyStroke struct {
	virtualKey uint16
	scanCode   uint16
	unicode    bool
}

type DesktopManagerCtx struct {
	logger     zerolog.Logger
	emmiter    events.EventEmmiter
	config     *config.Desktop
	screenSize types.ScreenSize

	mu             sync.Mutex
	pressedKeys    map[uint32][]keyStroke
	pressedButtons map[uint32]bool
}

func New(config *config.Desktop) *DesktopManagerCtx {
	return &DesktopManagerCtx{
		logger:         log.With().Str("module", "desktop").Logger(),
		emmiter:        events.New(),
		config:         config,
		screenSize:     config.ScreenSize,
		pressedKeys:    make(map[uint32][]keyStroke),
		pressedButtons: make(map[uint32]bool),
	}
}

func (manager *DesktopManagerCtx) Start() {
	width, _, _ := procGetSystemMetrics.Call(0)
	height, _, _ := procGetSystemMetrics.Call(1)
	manager.screenSize = types.ScreenSize{Width: int(width), Height: int(height), Rate: 25}
	manager.logger.Info().Str("screen_size", manager.screenSize.String()).Msg("using Windows desktop")
}

func (manager *DesktopManagerCtx) Shutdown() error {
	manager.ResetKeys()
	return nil
}

func (manager *DesktopManagerCtx) OnBeforeScreenSizeChange(listener func()) {
	manager.emmiter.On("before_screen_size_change", func(...any) { listener() })
}

func (manager *DesktopManagerCtx) OnAfterScreenSizeChange(listener func()) {
	manager.emmiter.On("after_screen_size_change", func(...any) { listener() })
}

func (manager *DesktopManagerCtx) Move(x, y int) {
	procSetCursorPos.Call(uintptr(x), uintptr(y))
}

func (manager *DesktopManagerCtx) GetCursorPosition() (int, int) {
	var p point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return int(p.X), int(p.Y)
}

func (manager *DesktopManagerCtx) Scroll(deltaX, deltaY int, controlKey bool) {
	if controlKey {
		_ = sendKeyboard(keyStroke{virtualKey: 0x11}, false)
		defer sendKeyboard(keyStroke{virtualKey: 0x11}, true)
	}
	if deltaY != 0 {
		_ = sendMouse(mouseWheel, uint32(int32(deltaY)))
	}
	if deltaX != 0 {
		_ = sendMouse(mouseHWheel, uint32(int32(deltaX)))
	}
}

func (manager *DesktopManagerCtx) ButtonDown(code uint32) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.pressedButtons[code] {
		return nil
	}
	down, _, ok := mouseFlags(code)
	if !ok {
		return fmt.Errorf("unknown mouse button %d", code)
	}
	if err := sendMouse(down, 0); err != nil {
		return err
	}
	manager.pressedButtons[code] = true
	return nil
}

func (manager *DesktopManagerCtx) ButtonUp(code uint32) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	_, up, ok := mouseFlags(code)
	if !ok {
		return fmt.Errorf("unknown mouse button %d", code)
	}
	if err := sendMouse(up, 0); err != nil {
		return err
	}
	delete(manager.pressedButtons, code)
	return nil
}

func (manager *DesktopManagerCtx) ButtonPress(code uint32) error {
	if err := manager.ButtonDown(code); err != nil {
		return err
	}
	return manager.ButtonUp(code)
}

func (manager *DesktopManagerCtx) KeyDown(code uint32) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, ok := manager.pressedKeys[code]; ok {
		return nil
	}
	strokes, err := keysymStrokes(code)
	if err != nil {
		return err
	}
	for _, stroke := range strokes {
		if err := sendKeyboard(stroke, false); err != nil {
			return err
		}
	}
	manager.pressedKeys[code] = strokes
	return nil
}

func (manager *DesktopManagerCtx) KeyUp(code uint32) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	strokes, ok := manager.pressedKeys[code]
	if !ok {
		var err error
		strokes, err = keysymStrokes(code)
		if err != nil {
			return err
		}
	}
	for i := len(strokes) - 1; i >= 0; i-- {
		if err := sendKeyboard(strokes[i], true); err != nil {
			return err
		}
	}
	delete(manager.pressedKeys, code)
	return nil
}

func (manager *DesktopManagerCtx) KeyPress(codes ...uint32) error {
	for _, code := range codes {
		if err := manager.KeyDown(code); err != nil {
			return err
		}
	}
	if len(codes) > 1 {
		time.Sleep(10 * time.Millisecond)
	}
	for i := len(codes) - 1; i >= 0; i-- {
		if err := manager.KeyUp(codes[i]); err != nil {
			return err
		}
	}
	return nil
}

func (manager *DesktopManagerCtx) ResetKeys() {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for code, strokes := range manager.pressedKeys {
		for i := len(strokes) - 1; i >= 0; i-- {
			_ = sendKeyboard(strokes[i], true)
		}
		delete(manager.pressedKeys, code)
	}
	for code := range manager.pressedButtons {
		_, up, ok := mouseFlags(code)
		if ok {
			_ = sendMouse(up, 0)
		}
		delete(manager.pressedButtons, code)
	}
}

func (manager *DesktopManagerCtx) ScreenConfigurations() []types.ScreenSize {
	return []types.ScreenSize{manager.screenSize}
}

func (manager *DesktopManagerCtx) SetScreenSize(types.ScreenSize) (types.ScreenSize, error) {
	return manager.screenSize, errors.New("screen resizing is not supported on Windows")
}

func (manager *DesktopManagerCtx) GetScreenSize() types.ScreenSize { return manager.screenSize }

func (manager *DesktopManagerCtx) SetKeyboardMap(types.KeyboardMap) error { return nil }

func (manager *DesktopManagerCtx) GetKeyboardMap() (*types.KeyboardMap, error) {
	return &types.KeyboardMap{Layout: "us"}, nil
}

func (manager *DesktopManagerCtx) SetKeyboardModifiers(mod types.KeyboardModifiers) {
	setModifier := func(code uint32, desired *bool) {
		if desired == nil {
			return
		}
		if *desired {
			_ = manager.KeyDown(code)
		} else {
			_ = manager.KeyUp(code)
		}
	}
	setModifier(0xffe1, mod.Shift)
	setModifier(0xffe5, mod.CapsLock)
	setModifier(0xffe3, mod.Control)
	setModifier(0xffe9, mod.Alt)
	setModifier(0xff7f, mod.NumLock)
	setModifier(0xffeb, mod.Super)
}

func (manager *DesktopManagerCtx) GetKeyboardModifiers() types.KeyboardModifiers {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	set := func(code uint32) *bool {
		_, ok := manager.pressedKeys[code]
		return &ok
	}
	return types.KeyboardModifiers{
		Shift: set(0xffe1), CapsLock: set(0xffe5), Control: set(0xffe3),
		Alt: set(0xffe9), NumLock: set(0xff7f), Super: set(0xffeb),
	}
}

func (manager *DesktopManagerCtx) GetCursorImage() *types.CursorImage {
	return &types.CursorImage{
		Width:  1,
		Height: 1,
		Serial: 1,
		Image:  image.NewRGBA(image.Rect(0, 0, 1, 1)),
	}
}
func (manager *DesktopManagerCtx) GetScreenshotImage() *image.RGBA { return nil }

func (manager *DesktopManagerCtx) OnCursorChanged(listener func(uint64)) {
	manager.emmiter.On("cursor-changed", func(payload ...any) { listener(payload[0].(uint64)) })
}
func (manager *DesktopManagerCtx) OnClipboardUpdated(listener func()) {
	manager.emmiter.On("clipboard-updated", func(...any) { listener() })
}
func (manager *DesktopManagerCtx) OnFileChooserDialogOpened(listener func()) {
	manager.emmiter.On("file-chooser-dialog-opened", func(...any) { listener() })
}
func (manager *DesktopManagerCtx) OnFileChooserDialogClosed(listener func()) {
	manager.emmiter.On("file-chooser-dialog-closed", func(...any) { listener() })
}
func (manager *DesktopManagerCtx) OnEventError(listener func(uint8, string, uint8, uint8)) {
	manager.emmiter.On("event-error", func(payload ...any) {
		listener(payload[0].(uint8), payload[1].(string), payload[2].(uint8), payload[3].(uint8))
	})
}

func (manager *DesktopManagerCtx) HasTouchSupport() bool { return false }
func (manager *DesktopManagerCtx) TouchBegin(uint32, int, int, uint8) error {
	return errors.New("touch input is not supported on Windows")
}
func (manager *DesktopManagerCtx) TouchUpdate(uint32, int, int, uint8) error {
	return errors.New("touch input is not supported on Windows")
}
func (manager *DesktopManagerCtx) TouchEnd(uint32, int, int, uint8) error {
	return errors.New("touch input is not supported on Windows")
}

func (manager *DesktopManagerCtx) ClipboardGetText() (*types.ClipboardText, error) {
	data, err := manager.ClipboardGetBinary(clipboardTextPlainTarget)
	if err != nil {
		return nil, err
	}
	return &types.ClipboardText{Text: string(data)}, nil
}

func (manager *DesktopManagerCtx) ClipboardSetText(data types.ClipboardText) error {
	if data.HTML != "" {
		return manager.ClipboardSetBinary(clipboardTextHTMLTarget, []byte(data.HTML))
	}
	return manager.ClipboardSetBinary(clipboardTextPlainTarget, []byte(data.Text))
}

func (manager *DesktopManagerCtx) ClipboardGetBinary(mime string) ([]byte, error) {
	if mime != clipboardTextPlainTarget && mime != "text/plain" {
		return nil, fmt.Errorf("unsupported clipboard target %q", mime)
	}
	cmd := hiddenPowerShell("Get-Clipboard -Raw -Format Text")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("get clipboard: %s", strings.TrimSpace(stderr.String()))
	}
	return output, nil
}

func (manager *DesktopManagerCtx) ClipboardSetBinary(mime string, data []byte) error {
	if mime != clipboardTextPlainTarget && mime != "text/plain" && mime != clipboardTextHTMLTarget {
		return fmt.Errorf("unsupported clipboard target %q", mime)
	}
	cmd := hiddenPowerShell("Set-Clipboard -Value ([Console]::In.ReadToEnd())")
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("set clipboard: %s", strings.TrimSpace(stderr.String()))
	}
	manager.emmiter.Emit("clipboard-updated")
	return nil
}

func (manager *DesktopManagerCtx) ClipboardGetTargets() ([]string, error) {
	return []string{"text/plain"}, nil
}

func (manager *DesktopManagerCtx) DropFiles(int, int, []string) bool { return false }
func (manager *DesktopManagerCtx) IsUploadDropEnabled() bool         { return false }
func (manager *DesktopManagerCtx) HandleFileChooserDialog(string) error {
	return errors.New("file chooser handling is not supported on Windows")
}
func (manager *DesktopManagerCtx) CloseFileChooserDialog()          {}
func (manager *DesktopManagerCtx) IsFileChooserDialogEnabled() bool { return false }
func (manager *DesktopManagerCtx) IsFileChooserDialogOpened() bool  { return false }

func hiddenPowerShell(script string) *exec.Cmd {
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}

func mouseFlags(code uint32) (uint32, uint32, bool) {
	switch code {
	case 1:
		return mouseLeftDown, mouseLeftUp, true
	case 2:
		return mouseMiddleDown, mouseMiddleUp, true
	case 3:
		return mouseRightDown, mouseRightUp, true
	default:
		return 0, 0, false
	}
}

func sendMouse(flags, data uint32) error {
	in := input{Type: inputMouse}
	*(*mouseInput)(unsafe.Pointer(&in.Data[0])) = mouseInput{MouseData: data, Flags: flags}
	return sendInput(&in)
}

func sendKeyboard(stroke keyStroke, up bool) error {
	in := input{Type: inputKeyboard}
	flags := uint32(0)
	if stroke.unicode {
		flags |= keyUnicode
	}
	if up {
		flags |= keyUp
	}
	*(*keyboardInput)(unsafe.Pointer(&in.Data[0])) = keyboardInput{
		VirtualKey: stroke.virtualKey,
		ScanCode:   stroke.scanCode,
		Flags:      flags,
	}
	return sendInput(&in)
}

func sendInput(in *input) error {
	result, _, callErr := procSendInput.Call(1, uintptr(unsafe.Pointer(in)), unsafe.Sizeof(*in))
	if result == 0 {
		return fmt.Errorf("SendInput: %w", callErr)
	}
	return nil
}

func keysymStrokes(code uint32) ([]keyStroke, error) {
	if code&0xff000000 == 0x01000000 {
		return unicodeStrokes(rune(code & 0x00ffffff)), nil
	}
	if code >= 'a' && code <= 'z' {
		return []keyStroke{{virtualKey: uint16(code - 'a' + 'A')}}, nil
	}
	if code >= 'A' && code <= 'Z' || code >= '0' && code <= '9' {
		return []keyStroke{{virtualKey: uint16(code)}}, nil
	}
	if code >= 0x20 && code <= 0xff {
		mapped, _, _ := procVkKeyScanW.Call(uintptr(code))
		if uint16(mapped) != 0xffff {
			var strokes []keyStroke
			modifiers := uint8(mapped >> 8)
			if modifiers&1 != 0 {
				strokes = append(strokes, keyStroke{virtualKey: 0x10})
			}
			if modifiers&2 != 0 {
				strokes = append(strokes, keyStroke{virtualKey: 0x11})
			}
			if modifiers&4 != 0 {
				strokes = append(strokes, keyStroke{virtualKey: 0x12})
			}
			return append(strokes, keyStroke{virtualKey: uint16(mapped) & 0xff}), nil
		}
		return unicodeStrokes(rune(code)), nil
	}
	if code >= 0xffbe && code <= 0xffc9 {
		return []keyStroke{{virtualKey: uint16(0x70 + code - 0xffbe)}}, nil
	}

	keys := map[uint32]uint16{
		0xff08: 0x08, 0xff09: 0x09, 0xff0d: 0x0d, 0xff1b: 0x1b,
		0xff50: 0x24, 0xff51: 0x25, 0xff52: 0x26, 0xff53: 0x27, 0xff54: 0x28,
		0xff55: 0x21, 0xff56: 0x22, 0xff57: 0x23, 0xff63: 0x2d, 0xffff: 0x2e,
		0xffe1: 0x10, 0xffe2: 0x10, 0xffe3: 0x11, 0xffe4: 0x11,
		0xffe9: 0x12, 0xffea: 0x12, 0xffeb: 0x5b, 0xffec: 0x5c,
		0xffe5: 0x14, 0xff7f: 0x90,
	}
	if key, ok := keys[code]; ok {
		return []keyStroke{{virtualKey: key}}, nil
	}
	return nil, fmt.Errorf("unsupported keysym %#x", code)
}

func unicodeStrokes(r rune) []keyStroke {
	units := utf16.Encode([]rune{r})
	strokes := make([]keyStroke, len(units))
	for i, unit := range units {
		strokes[i] = keyStroke{scanCode: unit, unicode: true}
	}
	return strokes
}
