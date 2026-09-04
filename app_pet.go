package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai-desktop/backend"
)

// The character, and the model's hand on it.
//
// The pixel animal has always wandered at random: it knew the agent's state —
// thinking, working, waiting — and nothing whatever about the page it walks on.
// So it could look busy but never look at anything, and there was no way for a
// turn to say "over here" while it was explaining where "here" was.
//
// Two halves make that possible, and they are deliberately separate. The
// window reports what is on it (PetStage); the model reads that back through
// pet_where and sends the character somewhere with pet_go. Neither half
// guesses: the model is told the landmarks that actually exist rather than a
// list compiled when this file was written, and the window resolves a name to
// a rectangle at the moment it walks, so a panel that moved is still found.
//
// The window is the only authority on what is on screen, which is why the
// stage goes stale rather than persisting: a name reported ten minutes ago by
// a window that has since closed is worse than no answer.

// petStageTTL is how long a report stands for.
//
// The window re-reports on every view change and on resize, plus a heartbeat,
// so a stage older than this means nobody is looking — the app was closed, the
// tab was left, or the character was sent away. Saying so is the point: a turn
// that sends the animal to a window nobody has open should be told, not
// silently succeed.
const petStageTTL = 90 * time.Second

// petSpot is one landmark the character can be sent to.
//
// Surface, when set, says the shape is something it can walk *on* rather than
// stand beside — "sphere" is the reactor's knowledge graph, which it circles
// the way an ant circles a marble.
type petSpot struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Surface string `json:"surface,omitempty"`
}

// petStage is what the window last said was on it.
type petStage struct {
	View  string    `json:"view"`
	Spots []petSpot `json:"spots"`
	At    time.Time `json:"-"`
}

type petState struct {
	mu    sync.RWMutex
	stage petStage
}

func (p *petState) set(view string, spots []petSpot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stage = petStage{View: view, Spots: spots, At: time.Now()}
}

// current returns the stage and whether it is still worth believing.
func (p *petState) current() (petStage, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.stage, !p.stage.At.IsZero() && time.Since(p.stage.At) < petStageTTL
}

// PetStage is the window telling the app what the character is standing on:
// which view is open, and what on it has a name worth walking to.
//
// Called on mount, on every view change and after a resize. Cheap on purpose —
// it stores and returns, so calling it often is the correct thing to do.
func (a *App) PetStage(view string, spots []map[string]string) {
	list := make([]petSpot, 0, len(spots))
	seen := map[string]bool{}
	for _, s := range spots {
		name := strings.TrimSpace(s["name"])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		list = append(list, petSpot{
			Name:    name,
			Label:   strings.TrimSpace(s["label"]),
			Surface: strings.TrimSpace(s["surface"]),
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	a.pet.set(strings.TrimSpace(view), list)
}

// registerPetTools gives the model the two calls it needs to use the character.
//
// They live here rather than in backend/service.go because they act on the
// avatar driver, which is the App's — the service builds an agent, it does not
// own a window.
//
// Takes the service rather than reading a.svc: it is called from the build,
// which already holds a.mu.
func (a *App) registerPetTools(svc *backend.Service) {
	if svc == nil {
		return
	}
	inner := svc.Agent()
	if inner == nil {
		return
	}

	look := agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true}
	inner.AddToolWithMetadata("pet_where",
		"What the person is looking at right now: which page of the SuperAI window is open, "+
			"and the named places on it the pixel character can be sent to. Call this before pet_go — "+
			"the places differ per page, and the window may not be open at all.",
		map[string]any{"type": "object", "properties": map[string]any{}},
		func(ctx context.Context, _ map[string]any) (any, error) {
			stage, fresh := a.pet.current()
			if !fresh {
				return "The SuperAI window is not showing the character right now " +
					"(closed, or it was sent away), so there is nowhere to point at.", nil
			}
			out := map[string]any{"page": stage.View, "spots": stage.Spots}
			b, err := json.Marshal(out)
			if err != nil {
				return "", err
			}
			return string(b), nil
		}, look)

	// Not read-only — it moves something the person can see — but nothing it
	// does survives the window, so it does not meet the approval gate either.
	act := agent.ToolMetadata{ConcurrencySafe: true}
	inner.AddToolWithMetadata("pet_go",
		"Send the pixel character somewhere on the SuperAI window, optionally with a line to say "+
			"and a face to wear. Use it to point at what you are talking about — the button you are "+
			"describing, the panel where the answer landed. Get the names from pet_where; an unknown "+
			"name simply makes it wander.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"spot": map[string]any{
					"type":        "string",
					"description": "A name from pet_where. Leave empty to let it wander off again.",
				},
				"say": map[string]any{
					"type":        "string",
					"description": "A short line it says on arrival. One sentence; it is a speech bubble, not a message.",
				},
				"emotion": map[string]any{
					"type": "string",
					"enum": []string{"neutral", "happy", "sad", "thinking", "excited",
						"sleepy", "confused", "love", "angry", "surprised"},
				},
			},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			return a.petGo(str(args["spot"]), str(args["say"]), str(args["emotion"])), nil
		}, act)
}

// showpiece is the spot the avatar test button sends it to: something round if
// the page has one, because circling a sphere shows the whole mechanism —
// stream, stage report, and a walk that follows a live rectangle — where
// standing beside a button only shows the first two.
func (st petStage) showpiece() (petSpot, bool) {
	for _, s := range st.Spots {
		if s.Surface != "" {
			return s, true
		}
	}
	if len(st.Spots) > 0 {
		return st.Spots[0], true
	}
	return petSpot{}, false
}

// petGo is pet_go's body, and what EmitAvatarTest and the tests reach for.
//
// The string it returns is what the model reads back, so it has to describe
// what the person will actually see — including the case where the name was
// not on the page. A turn that reports pointing at something the person never
// saw it point at is worse than one that reports missing.
func (a *App) petGo(spot, say, emotion string) string {
	spot = strings.TrimSpace(spot)
	say = strings.TrimSpace(say)
	emotion = strings.TrimSpace(emotion)

	// A speech bubble is a few words wide. Anything longer is a reply that has
	// been put in the wrong place, and cutting it here is kinder than drawing
	// a paragraph over the page.
	if r := []rune(say); len(r) > 120 {
		say = string(r[:119]) + "…"
	}

	stage, fresh := a.pet.current()
	if !fresh {
		return "Nothing happened: the SuperAI window is not showing the character."
	}

	a.mu.Lock()
	driver := a.avatar
	a.mu.Unlock()
	if driver == nil {
		return "Nothing happened: no avatar bridge is attached."
	}
	if emotion != "" {
		driver.Emit(backend.AvatarEvent{Type: backend.AvatarTypeEmotion, Emotion: emotion})
	}
	driver.Emit(backend.AvatarEvent{Type: backend.AvatarTypePoint, Spot: spot, Text: say})

	known := spot == ""
	for _, s := range stage.Spots {
		if s.Name == spot {
			known = true
			break
		}
	}
	switch {
	case spot == "":
		return "The character is wandering again." + saidSuffix(say)
	case known:
		return fmt.Sprintf("The character is walking to %q on the %s page.%s", spot, stage.View, saidSuffix(say))
	default:
		return fmt.Sprintf("There is no %q on the %s page, so it is wandering instead.%s", spot, stage.View, saidSuffix(say))
	}
}

func saidSuffix(say string) string {
	if say == "" {
		return ""
	}
	return " It says: " + say
}

// str reads a string argument that arrived as JSON, where it may be anything.
func str(v any) string {
	s, _ := v.(string)
	return s
}
