package app

import "testing"

func TestTheWindowShrinksOnlyOnAScreenSmallerThanIt(t *testing.T) {
	for _, c := range []struct {
		name         string
		w, h, sw, sh int
		fw, fh       int
		smaller      bool
	}{
		{"a big screen leaves it be", 1280, 860, 2560, 1440, 1280, 860, false},
		{"a 1280×800 VM", 1280, 860, 1280, 800, 1152, 720, true},
		{"a 1366×768 laptop", 1280, 860, 1366, 768, 1229, 691, true},
		{"no screen size known", 1280, 860, 0, 0, 1280, 860, false},
	} {
		fw, fh, smaller := fitToScreen(c.w, c.h, c.sw, c.sh)
		if fw != c.fw || fh != c.fh || smaller != c.smaller {
			t.Errorf("%s: got %d×%d %v, want %d×%d %v", c.name, fw, fh, smaller, c.fw, c.fh, c.smaller)
		}
	}
}
