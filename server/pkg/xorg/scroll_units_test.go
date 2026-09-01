package xorg

import "testing"

func TestScrollAccumulatorAdd(t *testing.T) {
	// each step feeds delta on the vertical axis and checks the notches
	// returned and the residual left behind for the next call
	type step struct {
		delta, notches, residual int
	}

	tests := []struct {
		name  string
		steps []step
	}{
		{"whole notch", []step{{120, 1, 0}}},
		{"half notches accumulate across calls", []step{{60, 0, 60}, {60, 1, 0}}},
		{"negative delta truncates toward zero", []step{{-180, -1, -60}}},
		{"reversal cancels the residual first", []step{{90, 0, 90}, {-120, 0, -30}, {-90, -1, 0}}},
		{"coalesced gesture emits one click per notch", []step{{1500, 12, 60}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var acc scrollAccumulator
			for i, s := range tt.steps {
				notchesX, notchesY := acc.add(0, s.delta)
				if notchesX != 0 || acc.x != 0 {
					t.Fatalf("step %d: horizontal axis moved: %d notches, residual %d", i, notchesX, acc.x)
				}
				if notchesY != s.notches || acc.y != s.residual {
					t.Fatalf("step %d: add(0, %d) = %d notches, residual %d; want %d notches, residual %d",
						i, s.delta, notchesY, acc.y, s.notches, s.residual)
				}
			}
		})
	}
}

func TestScrollAccumulatorAxesAreIndependent(t *testing.T) {
	var acc scrollAccumulator

	notchesX, notchesY := acc.add(130, -250)
	if notchesX != 1 || notchesY != -2 {
		t.Fatalf("add(130, -250) = (%d, %d), want (1, -2)", notchesX, notchesY)
	}
	if acc.x != 10 || acc.y != -10 {
		t.Fatalf("residual = (%d, %d), want (10, -10)", acc.x, acc.y)
	}
}

func TestScrollAccumulatorReset(t *testing.T) {
	var acc scrollAccumulator
	acc.add(100, -100)
	acc.reset()

	if acc.x != 0 || acc.y != 0 {
		t.Fatalf("residual after reset = (%d, %d), want (0, 0)", acc.x, acc.y)
	}

	// the discarded motion must not contribute to the next notch
	if notchesX, notchesY := acc.add(20, -20); notchesX != 0 || notchesY != 0 {
		t.Fatalf("add(20, -20) after reset = (%d, %d), want (0, 0)", notchesX, notchesY)
	}
}

func TestScrollResidualsDoNotCrossModifiers(t *testing.T) {
	resetScrollResiduals()
	defer resetScrollResiduals()

	// a page scroll that has not yet reached a whole notch
	if _, notchesY := scrollResidualFor(false).add(0, 110); notchesY != 0 {
		t.Fatalf("plain add(0, 110) = %d notches, want 0", notchesY)
	}

	// a Control-held scroll must not discharge it, which the browser would
	// apply as a zoom step instead of scrolling the page
	if _, notchesY := scrollResidualFor(true).add(0, 30); notchesY != 0 {
		t.Fatalf("control add(0, 30) = %d notches, want 0", notchesY)
	}

	// the page scroll is still pending and completes on its own
	if _, notchesY := scrollResidualFor(false).add(0, 10); notchesY != 1 {
		t.Fatalf("plain add(0, 10) = %d notches, want 1", notchesY)
	}
}

func TestResetScrollResidualsClearsBoth(t *testing.T) {
	resetScrollResiduals()
	defer resetScrollResiduals()

	scrollResidualFor(false).add(0, 100)
	scrollResidualFor(true).add(0, 100)
	resetScrollResiduals()

	if _, notchesY := scrollResidualFor(false).add(0, 20); notchesY != 0 {
		t.Fatalf("plain add(0, 20) after reset = %d notches, want 0", notchesY)
	}
	if _, notchesY := scrollResidualFor(true).add(0, 20); notchesY != 0 {
		t.Fatalf("control add(0, 20) after reset = %d notches, want 0", notchesY)
	}
}
