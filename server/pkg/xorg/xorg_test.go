package xorg

import "testing"

// refreshRate rounds the way the X server does when it lists the rates of a
// mode (RRVerticalRefresh), which is what XRRSetScreenConfigAndRate matches.
func (m screenModeInfo) refreshRate() int {
	dots := uint64(m.HTotal) * uint64(m.VTotal)
	return int((m.DotClock + dots/2) / dots)
}

func TestNewScreenModeInfo(t *testing.T) {
	// Sizes whose libxcvt timings run slightly below the requested rate
	// (e.g. 1008x384@60 at 59.45 Hz), widths that are not a multiple of 8, and
	// the 1360x768 size that libxcvt widens to 1366.
	sizes := [][2]int{
		{1008, 384}, {976, 384}, {1024, 384}, {1112, 384}, {1200, 384},
		{1008, 368}, {788, 300}, {630, 240}, {379, 480}, {1003, 500},
		{1205, 600}, {1360, 768}, {1366, 768}, {64, 64}, {390, 844},
		{1000, 384}, {1920, 1080}, {3840, 2160},
	}
	rates := []int16{10, 25, 30, 59, 60}

	for _, size := range sizes {
		for _, rate := range rates {
			m := newScreenModeInfo(size[0], size[1], rate)

			if m.Width != size[0] || m.Height != size[1] {
				t.Errorf("%dx%d@%d: mode is %dx%d", size[0], size[1], rate, m.Width, m.Height)
			}
			if got := m.refreshRate(); got != int(rate) {
				t.Errorf("%dx%d@%d: X lists the mode at %d Hz", size[0], size[1], rate, got)
			}
			if m.Width > m.HSyncStart || m.HSyncStart > m.HSyncEnd || m.HSyncEnd > m.HTotal {
				t.Errorf("%dx%d@%d: invalid horizontal timings %+v", size[0], size[1], rate, m)
			}
			if m.Height > m.VSyncStart || m.VSyncStart > m.VSyncEnd || m.VSyncEnd > m.VTotal {
				t.Errorf("%dx%d@%d: invalid vertical timings %+v", size[0], size[1], rate, m)
			}
		}
	}
}
