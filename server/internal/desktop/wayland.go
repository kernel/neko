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
	for key := 0; key <= 0xff; key++ {
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

func (input *waylandInput) scroll(deltaX, deltaY int) error {
	input.mu.Lock()
	defer input.mu.Unlock()
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
	return input.sync()
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

func mapKey(code uint32) (uint16, bool) {
	if code < 8 || code > 263 {
		return 0, false
	}
	return uint16(code - 8), true
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
