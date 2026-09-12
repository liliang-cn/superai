package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Saved dashboards.
//
// A reply that draws a wall of numbers — a `bigscreen` block of holdings, a
// chart of the week, a `ui` panel of what is scheduled — is worth more than one
// reading, and until now the only way back to one was to scroll a conversation
// until you found it.
//
// What is saved is the reply text, unchanged. It is markdown with fenced blocks
// in it, which is the only thing the renderer takes, so a dashboard is drawn by
// exactly the code that drew it in the transcript; there is no second format
// and nothing to keep in step.
//
// The prompt is saved beside it, and that is what makes this more than a
// scrapbook. Numbers go stale: a holdings wall saved on Monday is a picture of
// Monday, and shown on Friday without saying so it is simply wrong. So a
// dashboard carries the question that produced it, refreshing means asking that
// question again, and the card says how old what you are looking at is.

// Dashboard is one saved reply, and the question behind it.
type Dashboard struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Source is the assistant reply, verbatim — markdown plus whatever fenced
	// blocks it contained.
	Source string `json:"source"`
	// Prompt is the ask that produced Source. Empty means this dashboard cannot
	// be refreshed, which the UI has to say rather than offer a button that
	// does nothing.
	Prompt string `json:"prompt"`
	// Cron is the five-field schedule this refreshes itself on, or empty for
	// manual only.
	Cron        string    `json:"cron,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	RefreshedAt time.Time `json:"refreshed_at"`
	// LastError is why the most recent refresh failed, and is cleared by the
	// next one that succeeds. A dashboard whose refresh is failing still shows
	// its last good contents — stale data with a visible reason beats an empty
	// card.
	LastError string `json:"last_error,omitempty"`
	// Refreshing is not persisted: it is true only while a run is in flight in
	// this process, so the UI can show a spinner without inventing its own
	// bookkeeping that a restart would leave stuck on.
	Refreshing bool `json:"refreshing,omitempty"`
}

// dashboardSessionPrefix marks the conversation a dashboard's scheduled refresh
// runs in.
//
// This is how a finished scheduled run finds its way back to the dashboard that
// asked for it. agent-go's PromptRun carries the prompt and the session and
// nothing else — no id — so the session is the only structural handle there is.
// Matching on the prompt text instead would break the moment two dashboards
// asked the same question, or one was edited.
const dashboardSessionPrefix = "dashboard:"

// DashboardSession is the conversation id a dashboard's refresh runs in.
func DashboardSession(id string) string { return dashboardSessionPrefix + id }

// DashboardIDFromSession returns the dashboard a session belongs to, if any.
func DashboardIDFromSession(session string) (string, bool) {
	id, ok := strings.CutPrefix(session, dashboardSessionPrefix)
	return id, ok && id != ""
}

// dashboardStore is the saved set, on disk as one JSON file.
//
// A file rather than a table in cortex.db: this is a handful of rows that a
// person can read, edit and back up, and it is the same choice the life store
// and the session files made for the same reason.
type dashboardStore struct {
	mu    sync.Mutex
	path  string
	items []Dashboard
	// live tracks refreshes in flight, keyed by dashboard id. Not on the
	// Dashboard itself because that is what gets written to disk.
	live map[string]bool
}

func newDashboardStore(path string) *dashboardStore {
	s := &dashboardStore{path: path, live: map[string]bool{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(raw, &s.items); err != nil {
		// A file that does not parse is not thrown away: it is left where it
		// is, and the app starts with none. Overwriting it with an empty list
		// would destroy the only copy of whatever was in there.
		return &dashboardStore{path: path, live: map[string]bool{}}
	}
	return s
}

// save writes the whole list. The caller holds the lock.
func (s *dashboardStore) save() error {
	out, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, out, 0o644)
}

func (s *dashboardStore) list() []Dashboard {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Dashboard, len(s.items))
	copy(out, s.items)
	for i := range out {
		out[i].Refreshing = s.live[out[i].ID]
	}
	return out
}

func (s *dashboardStore) get(id string) (Dashboard, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.items {
		if d.ID == id {
			d.Refreshing = s.live[id]
			return d, true
		}
	}
	return Dashboard{}, false
}

// update applies a change to one dashboard and persists the result.
func (s *dashboardStore) update(id string, fn func(*Dashboard)) (Dashboard, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID != id {
			continue
		}
		fn(&s.items[i])
		out := s.items[i]
		out.Refreshing = s.live[id]
		return out, s.save()
	}
	return Dashboard{}, fmt.Errorf("no dashboard %q", id)
}

func (s *dashboardStore) setLive(id string, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if running {
		s.live[id] = true
	} else {
		delete(s.live, id)
	}
}

// ----------------------------------------------------------------------------
// Service surface
// ----------------------------------------------------------------------------

// Dashboards lists what has been saved, newest first.
func (s *Service) Dashboards() []Dashboard {
	if s == nil || s.dashboards == nil {
		return []Dashboard{}
	}
	items := s.dashboards.list()
	// Reversed rather than sorted by a timestamp: two saved in the same second
	// would otherwise swap places between reads for no reason a person could
	// see.
	out := make([]Dashboard, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		out = append(out, items[i])
	}
	return out
}

// Dashboard returns one by id.
func (s *Service) Dashboard(id string) (Dashboard, bool) {
	if s == nil || s.dashboards == nil {
		return Dashboard{}, false
	}
	return s.dashboards.get(id)
}

// SaveDashboard keeps a reply, and the question that produced it.
func (s *Service) SaveDashboard(name, source, prompt string) (Dashboard, error) {
	if s == nil || s.dashboards == nil {
		return Dashboard{}, errors.New("dashboard store is unavailable")
	}
	name = strings.TrimSpace(name)
	source = strings.TrimSpace(source)
	if source == "" {
		return Dashboard{}, errors.New("nothing to save")
	}
	if name == "" {
		name = "Untitled dashboard"
	}
	now := time.Now()
	d := Dashboard{
		// A whole UUID, not a short prefix of one. Eight hex characters is 32
		// bits, and 32 bits collide at about one chance in a hundred by three
		// hundred rows — which for a store that appends without checking would
		// have meant two dashboards under one id, and update and delete finding
		// whichever came first. The UI is free to show a prefix; the file keeps
		// the whole thing.
		ID:        uuid.NewString(),
		Name:      name,
		Source:    source,
		Prompt:    strings.TrimSpace(prompt),
		CreatedAt: now,
		// The source is as fresh as the reply it came from, which is now.
		RefreshedAt: now,
	}
	s.dashboards.mu.Lock()
	defer s.dashboards.mu.Unlock()
	// Belt to the UUID's braces: an id that already exists is a bug somewhere
	// upstream, and appending anyway would hide it behind two rows that look
	// identical and cannot both be deleted.
	for _, existing := range s.dashboards.items {
		if existing.ID == d.ID {
			return Dashboard{}, fmt.Errorf("dashboard id %s already exists", d.ID)
		}
	}
	s.dashboards.items = append(s.dashboards.items, d)
	return d, s.dashboards.save()
}

// ReplaceDashboard overwrites a dashboard's contents in place.
//
// The board keeps its id, the day it was made, and its schedule — everything
// about it that other things point at. Only what it shows and the question
// behind it change.
//
// This exists because "改一下这个看板" was, until it did, a save: dashboard_save
// could only append, so every round of improving a board left the last version
// beside it. Three rows called 资产管理 with three slightly different prompts is
// not a history, it is a mess with no way to tell which one is current.
func (s *Service) ReplaceDashboard(id, name, source, prompt string) (Dashboard, error) {
	if s == nil || s.dashboards == nil {
		return Dashboard{}, errors.New("dashboard store is unavailable")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return Dashboard{}, errors.New("nothing to save")
	}
	name, prompt = strings.TrimSpace(name), strings.TrimSpace(prompt)
	return s.dashboards.update(id, func(d *Dashboard) {
		if name != "" {
			d.Name = name
		}
		d.Source = source
		// An empty prompt does not erase the one that is there. A save that
		// forgot to repeat the question would otherwise turn a board that
		// refreshes itself into one that never can again.
		if prompt != "" {
			d.Prompt = prompt
		}
		d.RefreshedAt = time.Now()
		d.LastError = ""
	})
}

// DashboardsNamed returns every dashboard carrying a name, case-insensitively.
//
// More than one is possible — the store has never stopped it — so this answers
// with all of them and lets the caller decide. One match is a board being
// updated; several is an existing mess that must not be silently picked from.
func (s *Service) DashboardsNamed(name string) []Dashboard {
	name = strings.ToLower(strings.TrimSpace(name))
	if s == nil || s.dashboards == nil || name == "" {
		return nil
	}
	var out []Dashboard
	for _, d := range s.dashboards.list() {
		if strings.ToLower(strings.TrimSpace(d.Name)) == name {
			out = append(out, d)
		}
	}
	return out
}

// dashboardTarget decides which board a save is aimed at.
//
// An id is the explicit answer. A name that already exists is the implicit
// one, and taking it is the whole point: a board is what the user calls it, so
// asking twice to save 资产管理 means one board that changed, not two that are
// almost the same. Before this, dashboard_save could only append, and every
// round of "改一下这个看板" left the previous version sitting beside it.
//
// Returns the id to overwrite (empty to create), a sentence for the model
// about what it did, and a refusal when the answer is genuinely ambiguous.
func (s *Service) dashboardTarget(name, id string, forceNew bool) (target, note, refuse string) {
	if id = strings.TrimSpace(id); id != "" {
		if _, ok := s.Dashboard(id); !ok {
			return "", "", "no dashboard " + id + "; call dashboard_list for the ids"
		}
		return id, "Replaced the board with that id.", ""
	}
	if forceNew {
		return "", "", ""
	}
	same := s.DashboardsNamed(name)
	switch len(same) {
	case 0:
		return "", "", ""
	case 1:
		return same[0].ID,
			"A board called " + same[0].Name + " already existed, so this replaced it rather than adding a second.",
			""
	default:
		// Several already share the name. Picking one would be a guess, and
		// adding another would make the pile worse.
		return "", "", fmt.Sprintf(
			"there are already %d dashboards called %q, so it is not clear which to update — "+
				"pass the id of the one you mean (dashboard_list), or new:true for another",
			len(same), name)
	}
}

// RenameDashboard changes the label only.
func (s *Service) RenameDashboard(id, name string) error {
	if s == nil || s.dashboards == nil {
		return errors.New("dashboard store is unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name is required")
	}
	_, err := s.dashboards.update(id, func(d *Dashboard) { d.Name = name })
	return err
}

// SetDashboardCron records the schedule. Actually arming it is the app's job —
// the scheduler lives up there with the notifications it fires.
func (s *Service) SetDashboardCron(id, cron string) error {
	if s == nil || s.dashboards == nil {
		return errors.New("dashboard store is unavailable")
	}
	_, err := s.dashboards.update(id, func(d *Dashboard) { d.Cron = strings.TrimSpace(cron) })
	return err
}

// DeleteDashboard forgets one.
func (s *Service) DeleteDashboard(id string) error {
	if s == nil || s.dashboards == nil {
		return errors.New("dashboard store is unavailable")
	}
	s.dashboards.mu.Lock()
	defer s.dashboards.mu.Unlock()
	for i := range s.dashboards.items {
		if s.dashboards.items[i].ID != id {
			continue
		}
		s.dashboards.items = append(s.dashboards.items[:i], s.dashboards.items[i+1:]...)
		return s.dashboards.save()
	}
	return nil
}

// MarkDashboardRefreshing flags a run as in flight, for the spinner.
func (s *Service) MarkDashboardRefreshing(id string, running bool) {
	if s == nil || s.dashboards == nil {
		return
	}
	s.dashboards.setLive(id, running)
}

// ApplyDashboardRefresh stores the outcome of a re-run.
//
// A failed refresh keeps the old source on purpose. The alternative is a card
// that empties itself because the network was down for a minute, and last
// week's numbers with a visible error on them are more use than nothing.
func (s *Service) ApplyDashboardRefresh(id, source string, runErr error) (Dashboard, error) {
	if s == nil || s.dashboards == nil {
		return Dashboard{}, errors.New("dashboard store is unavailable")
	}
	return s.dashboards.update(id, func(d *Dashboard) {
		if runErr != nil {
			d.LastError = runErr.Error()
			return
		}
		if strings.TrimSpace(source) == "" {
			d.LastError = "the refresh returned nothing"
			return
		}
		d.Source = source
		d.RefreshedAt = time.Now()
		d.LastError = ""
	})
}
