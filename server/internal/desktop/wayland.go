package desktop

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	waylandInputPath = "/dev/uinput"

	evSyn = 0x00
	evKey = 0x01
	evRel = 0x02
	evAbs = 0x03

	relX      = 0x00
	relY      = 0x01
	relWheel  = 0x08
	relHWheel = 0x06

	absX = 0x00
	absY = 0x01

	btnLeft   = 0x110
	btnMiddle = 0x112
	btnRight  = 0x111

	uiSetEvbit   = 0x40045564
	uiSetKeybit  = 0x40045565
	uiSetRelbit  = 0x40045566
	uiSetAbsbit  = 0x40045567
	uiDevCreate  = 0x5501
	uiDevDestroy = 0x5502
)

type uinputID struct {
	BusType uint16
	Vendor  uint16
	Product uint16
	Version uint16
}

type uinputUserDev struct {
	Name         [80]byte
	ID           uinputID
	FFEffectsMax uint32
	AbsMax       [64]int32
	AbsMin       [64]int32
	AbsFuzz      [64]int32
	AbsFlat      [64]int32
}

type inputEvent struct {
	Sec   int64
	Usec  int64
	Type  uint16
	Code  uint16
	Value int32
}

type waylandInput struct {
	fd     *os.File
	width  int
	height int

	mu      sync.Mutex
	cursorX int
	cursorY int
	pressed map[uint16]struct{}
}

func (manager *DesktopManagerCtx) getWaylandInput() *waylandInput {
	manager.waylandMu.RLock()
	defer manager.waylandMu.RUnlock()
	return manager.waylandInput
}

func newWaylandInput(width, height int) (*waylandInput, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid Wayland input size: %dx%d", width, height)
	}

	fd, err := os.OpenFile(waylandInputPath, os.O_WRONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", waylandInputPath, err)
	}

	input := &waylandInput{
		fd:      fd,
		width:   width,
		height:  height,
		pressed: make(map[uint16]struct{}),
	}
	if err := input.create(); err != nil {
		_ = fd.Close()
		return nil, err
	}
	return input, nil
}

func (input *waylandInput) ioctl(request, value uintptr) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, input.fd.Fd(), request, value)
	if errno != 0 {
		return errno
	}
	return nil
}

func (input *waylandInput) create() error {
	for _, eventType := range []int{evKey, evRel, evAbs} {
		if err := input.ioctl(uiSetEvbit, uintptr(eventType)); err != nil {
			return fmt.Errorf("enable uinput event type %d: %w", eventType, err)
		}
	}
	for key := 0; key <= 0x1ff; key++ {
		if err := input.ioctl(uiSetKeybit, uintptr(key)); err != nil {
			return fmt.Errorf("enable uinput key %d: %w", key, err)
		}
	}
	for _, rel := range []int{relX, relY, relWheel, relHWheel} {
		if err := input.ioctl(uiSetRelbit, uintptr(rel)); err != nil {
			return fmt.Errorf("enable uinput relative axis %d: %w", rel, err)
		}
	}
	for _, abs := range []int{absX, absY} {
		if err := input.ioctl(uiSetAbsbit, uintptr(abs)); err != nil {
			return fmt.Errorf("enable uinput absolute axis %d: %w", abs, err)
		}
	}

	device := uinputUserDev{
		ID: uinputID{BusType: 0x03, Vendor: 0x1, Product: 0x1, Version: 1},
	}
	copy(device.Name[:], "Neko Wayland input")
	device.AbsMax[absX] = int32(input.width - 1)
	device.AbsMax[absY] = int32(input.height - 1)
	if err := binary.Write(input.fd, binary.LittleEndian, &device); err != nil {
		return fmt.Errorf("configure uinput device: %w", err)
	}
	if err := input.ioctl(uiDevCreate, 0); err != nil {
		return fmt.Errorf("create uinput device: %w", err)
	}
	return nil
}

func (input *waylandInput) close() {
	input.mu.Lock()
	defer input.mu.Unlock()
	if input.fd == nil {
		return
	}
	_ = input.ioctl(uiDevDestroy, 0)
	_ = input.fd.Close()
	input.fd = nil
}

func (input *waylandInput) emit(eventType, code uint16, value int32) error {
	now := time.Now()
	event := inputEvent{
		Sec:   now.Unix(),
		Usec:  int64(now.Nanosecond()) / 1000,
		Type:  eventType,
		Code:  code,
		Value: value,
	}
	return binary.Write(input.fd, binary.LittleEndian, &event)
}

func (input *waylandInput) sync() error {
	return input.emit(evSyn, 0, 0)
}

func (input *waylandInput) position() (int, int) {
	input.mu.Lock()
	defer input.mu.Unlock()
	return input.cursorX, input.cursorY
}

func (input *waylandInput) move(x, y int) error {
	input.mu.Lock()
	defer input.mu.Unlock()
	if err := input.emit(evAbs, absX, int32(clamp(x, 0, input.width-1))); err != nil {
		return err
	}
	if err := input.emit(evAbs, absY, int32(clamp(y, 0, input.height-1))); err != nil {
		return err
	}
	if err := input.sync(); err != nil {
		return err
	}
	input.cursorX, input.cursorY = x, y
	return nil
}

func (input *waylandInput) button(code uint32, down bool) error {
	linuxCode, ok := mapButton(code)
	if !ok {
		return fmt.Errorf("unsupported mouse button: %d", code)
	}
	input.mu.Lock()
	defer input.mu.Unlock()
	if err := input.emit(evKey, linuxCode, boolValue(down)); err != nil {
		return err
	}
	if err := input.sync(); err != nil {
		return err
	}
	if down {
		input.pressed[linuxCode] = struct{}{}
	} else {
		delete(input.pressed, linuxCode)
	}
	return nil
}

func (input *waylandInput) key(code uint32, down bool) error {
	linuxCode, ok := mapKey(code)
	if !ok {
		return fmt.Errorf("unsupported keyboard key: %d", code)
	}
	input.mu.Lock()
	defer input.mu.Unlock()
	if err := input.emit(evKey, linuxCode, boolValue(down)); err != nil {
		return err
	}
	if err := input.sync(); err != nil {
		return err
	}
	if down {
		input.pressed[linuxCode] = struct{}{}
	} else {
		delete(input.pressed, linuxCode)
	}
	return nil
}

func (input *waylandInput) scroll(deltaX, deltaY int, controlKey bool) error {
	input.mu.Lock()
	defer input.mu.Unlock()

	temporaryControl := controlKey && !input.isPressed(keyLeftCtrl) && !input.isPressed(keyRightCtrl)
	if temporaryControl {
		if err := input.emit(evKey, keyLeftCtrl, 1); err != nil {
			return err
		}
	}
	if deltaY != 0 {
		if err := input.emit(evRel, relWheel, int32(-deltaY)); err != nil {
			return err
		}
	}
	if deltaX != 0 {
		if err := input.emit(evRel, relHWheel, int32(deltaX)); err != nil {
			return err
		}
	}
	if temporaryControl {
		if err := input.emit(evKey, keyLeftCtrl, 0); err != nil {
			return err
		}
	}
	return input.sync()
}

func (input *waylandInput) isPressed(code uint16) bool {
	_, ok := input.pressed[code]
	return ok
}

func (input *waylandInput) resetKeys() error {
	input.mu.Lock()
	defer input.mu.Unlock()
	for code := range input.pressed {
		if err := input.emit(evKey, code, 0); err != nil {
			return err
		}
	}
	input.pressed = make(map[uint16]struct{})
	return input.sync()
}

func mapButton(code uint32) (uint16, bool) {
	switch code {
	case 1:
		return btnLeft, true
	case 2:
		return btnMiddle, true
	case 3:
		return btnRight, true
	default:
		return 0, false
	}
}

const (
	keyEsc        = 1
	key1          = 2
	key2          = 3
	key3          = 4
	key4          = 5
	key5          = 6
	key6          = 7
	key7          = 8
	key8          = 9
	key9          = 10
	key0          = 11
	keyMinus      = 12
	keyEqual      = 13
	keyBackspace  = 14
	keyTab        = 15
	keyQ          = 16
	keyW          = 17
	keyE          = 18
	keyR          = 19
	keyT          = 20
	keyY          = 21
	keyU          = 22
	keyI          = 23
	keyO          = 24
	keyP          = 25
	keyLeftBrace  = 26
	keyRightBrace = 27
	keyEnter      = 28
	keyLeftCtrl   = 29
	keyA          = 30
	keyS          = 31
	keyD          = 32
	keyF          = 33
	keyG          = 34
	keyH          = 35
	keyJ          = 36
	keyK          = 37
	keyL          = 38
	keySemicolon  = 39
	keyApostrophe = 40
	keyGrave      = 41
	keyLeftShift  = 42
	keyBackslash  = 43
	keyZ          = 44
	keyX          = 45
	keyC          = 46
	keyV          = 47
	keyB          = 48
	keyN          = 49
	keyM          = 50
	keyComma      = 51
	keyDot        = 52
	keySlash      = 53
	keyRightShift = 54
	keyLeftAlt    = 56
	keySpace      = 57
	keyCapsLock   = 58
	keyF1         = 59
	keyF10        = 68
	keyRightCtrl  = 97
	keyRightAlt   = 100
	keyHome       = 102
	keyUp         = 103
	keyPageUp     = 104
	keyLeft       = 105
	keyRight      = 106
	keyEnd        = 107
	keyDown       = 108
	keyPageDown   = 109
	keyInsert     = 110
	keyDelete     = 111
)

func mapKey(keysym uint32) (uint16, bool) {
	if keysym >= 'a' && keysym <= 'z' {
		return keyA + uint16(keysym-'a'), true
	}
	if keysym >= 'A' && keysym <= 'Z' {
		return keyA + uint16(keysym-'A'), true
	}
	if keysym >= '1' && keysym <= '9' {
		return key1 + uint16(keysym-'1'), true
	}
	if keysym == '0' {
		return key0, true
	}

	switch keysym {
	case 0xff1b:
		return keyEsc, true
	case 0xff08:
		return keyBackspace, true
	case 0xff09:
		return keyTab, true
	case 0xff0d:
		return keyEnter, true
	case 0xffff:
		return keyDelete, true
	case 0xff63:
		return keyInsert, true
	case 0xff50:
		return keyHome, true
	case 0xff51:
		return keyLeft, true
	case 0xff52:
		return keyUp, true
	case 0xff53:
		return keyRight, true
	case 0xff54:
		return keyDown, true
	case 0xff55:
		return keyPageUp, true
	case 0xff56:
		return keyPageDown, true
	case 0xff57:
		return keyEnd, true
	case 0xffe1:
		return keyLeftShift, true
	case 0xffe2:
		return keyRightShift, true
	case 0xffe3:
		return keyLeftCtrl, true
	case 0xffe4:
		return keyRightCtrl, true
	case 0xffe9:
		return keyLeftAlt, true
	case 0xffea:
		return keyRightAlt, true
	}
	if keysym >= 0xffbe && keysym <= 0xffc7 {
		return keyF1 + uint16(keysym-0xffbe), true
	}
	if keysym == 0xffc8 {
		return 87, true
	}
	if keysym == 0xffc9 {
		return 88, true
	}

	switch keysym {
	case '-', '_':
		return keyMinus, true
	case '=', '+':
		return keyEqual, true
	case '[', '{':
		return keyLeftBrace, true
	case ']', '}':
		return keyRightBrace, true
	case '\\', '|':
		return keyBackslash, true
	case ';', ':':
		return keySemicolon, true
	case '\'', '"':
		return keyApostrophe, true
	case '`', '~':
		return keyGrave, true
	case ',', '<':
		return keyComma, true
	case '.', '>':
		return keyDot, true
	case '/', '?':
		return keySlash, true
	case ' ':
		return keySpace, true
	case 0xffe5:
		return keyCapsLock, true
	}
	return 0, false
}

func boolValue(value bool) int32 {
	if value {
		return 1
	}
	return 0
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
