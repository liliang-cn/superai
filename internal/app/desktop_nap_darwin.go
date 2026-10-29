//go:build darwin && cgo

package app

/*
#cgo darwin LDFLAGS: -framework Foundation
void slBeginActivity(void);
void slEndActivity(void);
*/
import "C"

// holdOffAppNap keeps macOS from napping this process while it runs hidden.
func holdOffAppNap(on bool) {
	if on {
		C.slBeginActivity()
	} else {
		C.slEndActivity()
	}
}
