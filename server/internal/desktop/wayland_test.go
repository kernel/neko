package desktop

import "testing"

func TestMapKeyConvertsX11Keycodes(t *testing.T) {
	for _, test := range []struct {
		name string
		in   uint32
		want uint16
	}{
		{name: "escape", in: 9, want: 1},
		{name: "a", in: 38, want: 30},
		{name: "f12", in: 96, want: 88},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := mapKey(test.in)
			if !ok || got != test.want {
				t.Fatalf("mapKey(%d) = (%d, %v), want (%d, true)", test.in, got, ok, test.want)
			}
		})
	}
}

func TestMapButton(t *testing.T) {
	for _, test := range []struct {
		in   uint32
		want uint16
	}{
		{in: 1, want: btnLeft},
		{in: 2, want: btnMiddle},
		{in: 3, want: btnRight},
	} {
		got, ok := mapButton(test.in)
		if !ok || got != test.want {
			t.Fatalf("mapButton(%d) = (%d, %v), want (%d, true)", test.in, got, ok, test.want)
		}
	}
	if _, ok := mapButton(9); ok {
		t.Fatal("mapButton(9) unexpectedly succeeded")
	}
}
