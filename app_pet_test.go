package main

import (
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superai-desktop/backend"
)

// A driver that remembers what it was told, so a test can ask what the window
// would have seen.
type recordingDriver struct{ events []backend.AvatarEvent }

func (d *recordingDriver) Emit(ev backend.AvatarEvent) { d.events = append(d.events, ev) }

func (d *recordingDriver) last(kind string) (backend.AvatarEvent, bool) {
	for i := len(d.events) - 1; i >= 0; i-- {
		if d.events[i].Type == kind {
			return d.events[i], true
		}
	}
	return backend.AvatarEvent{}, false
}

func petTestApp() (*App, *recordingDriver) {
	d := &recordingDriver{}
	return &App{avatar: d}, d
}

// The window is the only thing that knows what is on it. Everything the model
// is told about the page comes from this call.
func TestTheStageIsWhatTheWindowReported(t *testing.T) {
	a, _ := petTestApp()
	a.PetStage("chat", []map[string]string{
		{"name": "send", "label": "the send button"},
		{"name": "composer", "label": "the box you type into"},
		{"name": "send", "label": "a duplicate"},
		{"name": "  ", "label": "nameless"},
	})
	stage, fresh := a.pet.current()
	if !fresh {
		t.Fatal("a report just made is already stale")
	}
	if stage.View != "chat" {
		t.Errorf("view = %q", stage.View)
	}
	if len(stage.Spots) != 2 {
		t.Fatalf("got %d spots, want the two distinct named ones: %+v", len(stage.Spots), stage.Spots)
	}
	// Sorted, so the same page always describes itself the same way and a
	// cached turn is not invalidated by DOM order.
	if stage.Spots[0].Name != "composer" || stage.Spots[1].Name != "send" {
		t.Errorf("spots are not in a stable order: %+v", stage.Spots)
	}
}

// A window that closed is not a quiet window. Answering with a page nobody is
// looking at is how a turn ends up saying "see, over there" to an empty room.
func TestAStaleStageReadsAsNobodyLooking(t *testing.T) {
	a, _ := petTestApp()
	a.PetStage("stats", []map[string]string{{"name": "reactor"}})
	a.pet.mu.Lock()
	a.pet.stage.At = time.Now().Add(-petStageTTL - time.Second)
	a.pet.mu.Unlock()
	if _, fresh := a.pet.current(); fresh {
		t.Error("a report older than the TTL is still being believed")
	}
}

// The test button is the only way to see the character walk without a model,
// so it has to exercise the walk and not just the face.
func TestTheAvatarTestWalksItSomewhereReal(t *testing.T) {
	a, d := petTestApp()
	a.PetStage("chat", []map[string]string{{"name": "composer", "label": "the box you type into"}})
	a.EmitAvatarTest("happy")

	point, ok := d.last(backend.AvatarTypePoint)
	if !ok {
		t.Fatal("the test event never told the character to go anywhere")
	}
	if point.Spot != "composer" {
		t.Errorf("sent to %q, which is not on the reported page", point.Spot)
	}

	// A round thing wins, wherever it sits in the list: circling it shows the
	// walk following a live rectangle, which standing beside a button cannot.
	a3, d3 := petTestApp()
	a3.PetStage("stats", []map[string]string{
		{"name": "activity", "label": "the stream"},
		{"name": "brain", "label": "the graph", "surface": "sphere"},
	})
	a3.EmitAvatarTest("happy")
	if ev, _ := d3.last(backend.AvatarTypePoint); ev.Spot != "brain" {
		t.Errorf("the test walked to %q rather than the sphere", ev.Spot)
	}

	// And with no window reporting, it must not invent a destination.
	a2, d2 := petTestApp()
	a2.EmitAvatarTest("happy")
	if _, ok := d2.last(backend.AvatarTypePoint); ok {
		t.Error("it was sent somewhere with no window reporting a page")
	}
}

// The reply the model reads has to match what the person will see, including
// the case where the name was not on the page.
func TestPetGoSaysWhatWillActuallyHappen(t *testing.T) {
	a, d := petTestApp()
	a.PetStage("chat", []map[string]string{{"name": "send", "label": "the send button"}})

	got := a.petGo("send", "over here", "excited")
	if !strings.Contains(got, "send") || !strings.Contains(got, "chat") {
		t.Errorf("the reply does not name where it went: %q", got)
	}
	if ev, ok := d.last(backend.AvatarTypeEmotion); !ok || ev.Emotion != "excited" {
		t.Errorf("the face was not set: %+v", d.events)
	}

	if got := a.petGo("nowhere", "", ""); !strings.Contains(got, "no \"nowhere\"") {
		t.Errorf("an unknown place was reported as a success: %q", got)
	}

	// A paragraph in a speech bubble is a reply in the wrong place.
	long := strings.Repeat("字", 400)
	a.petGo("send", long, "")
	ev, _ := d.last(backend.AvatarTypePoint)
	if n := len([]rune(ev.Text)); n > 120 {
		t.Errorf("a %d-rune line went into a speech bubble", n)
	}
}

// With nothing reporting, pet_go must do nothing rather than emit into the void.
func TestPetGoDoesNothingWithNoWindow(t *testing.T) {
	a, d := petTestApp()
	if got := a.petGo("send", "hi", ""); !strings.Contains(got, "Nothing happened") {
		t.Errorf("got %q, want a refusal", got)
	}
	if len(d.events) != 0 {
		t.Errorf("it emitted %d events with nobody listening", len(d.events))
	}
}
