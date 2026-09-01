package xorg

// scrollNotchUnits is the number of scroll units that make up one wheel notch.
// The xf86-input-neko driver registers its scroll valuators with this
// increment (SCROLL_INCREMENT), so a delta bound for the driver means the same
// thing when the XTest fallback has to replay it. See pkg/xinput.
const scrollNotchUnits = 120

// Plain and Control-held scrolling accumulate separately. Sub-notch motion
// left over from a page scroll must not discharge as a notch while Control is
// held, because the browser applies that as a zoom step rather than scrolling.
// Both are guarded by mu, like the debounce maps in xorg.go.
var (
	scrollResidual     scrollAccumulator
	scrollResidualCtrl scrollAccumulator
)

// scrollResidualFor returns the accumulator owning scrolls with or without
// Control held.
func scrollResidualFor(controlKey bool) *scrollAccumulator {
	if controlKey {
		return &scrollResidualCtrl
	}
	return &scrollResidual
}

// resetScrollResiduals discards sub-notch motion pending on either accumulator.
func resetScrollResiduals() {
	scrollResidual.reset()
	scrollResidualCtrl.reset()
}

// scrollAccumulator turns scroll deltas into whole wheel notches, carrying the
// sub-notch remainder between calls. XTest can only emit discrete wheel button
// clicks, so a delta has to be divided into notches before it is replayed, and
// carrying the remainder keeps slow scrolling from being rounded away.
type scrollAccumulator struct {
	x, y int // pending units, always within (-scrollNotchUnits, scrollNotchUnits)
}

// add accumulates a delta in scroll units and returns the whole notches now
// due on each axis. Notches are truncated toward zero, so the remainder keeps
// the sign of the pending motion and a reversal cancels it before emitting a
// notch in the new direction.
func (a *scrollAccumulator) add(deltaX, deltaY int) (notchesX, notchesY int) {
	a.x += deltaX
	a.y += deltaY

	notchesX, a.x = a.x/scrollNotchUnits, a.x%scrollNotchUnits
	notchesY, a.y = a.y/scrollNotchUnits, a.y%scrollNotchUnits
	return notchesX, notchesY
}

// reset discards any pending sub-notch motion.
func (a *scrollAccumulator) reset() {
	a.x, a.y = 0, 0
}
