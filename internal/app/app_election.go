package app

// The app's side of electing a queen (backend/hive_election.go): a worker
// that is picked takes over, a queen that is passed over steps down.
//
// Elections are on where $SUPERAI_ELECTION is set: the hive's pods. There a
// queen also wears a label on her pod, $SUPERAI_QUEEN_LABEL (with her term in
// the annotation beside it), and the services the world reaches the queen by
// select on it — so whichever pod is queen is where ai.superleo.cn, the
// agent link and the workers' join address lead, with nothing to forward.
// The label is also how two queens find out about each other: every few
// seconds a queen looks at who wears it, takes it off a queen of an earlier
// term, and steps down before one of a later term.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

const (
	// queenLabelEvery is how often a queen checks who wears the label.
	queenLabelEvery = 10 * time.Second
	// queenTermAnnotation carries a labelled queen's term on her pod.
	queenTermAnnotation = "superai/queen-term"
)

// electionOn is whether this instance takes part in electing a queen.
func electionOn() bool { return strings.TrimSpace(os.Getenv("SUPERAI_ELECTION")) != "" }

// electionJoinURL is where a queen that steps down goes to find the queen
// after her: $SUPERAI_QUEEN_URL, the address that always leads to whoever is
// queen (in k3s the service on the label).
func electionJoinURL() string { return strings.TrimSpace(os.Getenv("SUPERAI_QUEEN_URL")) }

// promoteToQueen makes this worker the queen it was elected, under term.
func (a *App) promoteToQueen(term int64) {
	a.mu.Lock()
	var next backend.Settings
	if a.settings != nil {
		next = *a.settings
	}
	a.mu.Unlock()
	next.Hive.Role = backend.HiveRoleQueen
	// A worker's settings say nothing of growing the hive; a queen's do.
	if next.Hive.Spawner == nil {
		if set := strings.TrimSpace(os.Getenv("SUPERAI_WORKER_STATEFULSET")); set != "" {
			ns := strings.TrimSpace(os.Getenv("POD_NAMESPACE"))
			next.Hive.Spawner = &backend.SpawnerSettings{Kind: "kubectl", Namespace: ns, StatefulSet: set, MaxWorkers: backend.DefaultMaxWorkers}
		}
	}
	if err := a.SaveSettings(next); err != nil {
		log.Printf("hive: could not take over as queen: %v", err)
		return
	}
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h != nil {
		h.SetTerm(term)
	}
	a.wearQueenLabel(context.Background(), term)
	a.emit("hosts:changed", nil)
	log.Printf("hive: queen now, term %d", term)
}

// stepDown makes a queen that was passed over a worker of the queen after
// her.
func (a *App) stepDown(why string) {
	join := electionJoinURL()
	if join == "" {
		log.Printf("hive: should step down (%s) but $SUPERAI_QUEEN_URL says nowhere to go", why)
		return
	}
	a.mu.Lock()
	var next backend.Settings
	if a.settings != nil {
		next = *a.settings
	}
	a.mu.Unlock()
	if next.Hive.Role != backend.HiveRoleQueen {
		return
	}
	log.Printf("hive: stepping down: %s", why)
	if label, pod := queenLabel(); label != "" && pod != "" {
		if _, err := kubectl(context.Background(), "label", "pod", pod, label+"-"); err != nil {
			log.Printf("hive: %v", err)
		}
	}
	next.Hive.Role = backend.HiveRoleWorker
	next.Hive.JoinURL = join
	if err := a.SaveSettings(next); err != nil {
		log.Printf("hive: could not step down: %v", err)
		return
	}
	a.emit("hosts:changed", nil)
}

// kubectl runs kubectl in this pod's namespace.
func kubectl(ctx context.Context, args ...string) (string, error) {
	ns := strings.TrimSpace(os.Getenv("POD_NAMESPACE"))
	if ns == "" {
		ns = "superai"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "kubectl", append([]string{"-n", ns}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("kubectl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// queenLabel is the label a queen's pod wears, and this pod's name; empty
// outside a pod set up for it.
func queenLabel() (label, pod string) {
	return strings.TrimSpace(os.Getenv("SUPERAI_QUEEN_LABEL")), strings.TrimSpace(os.Getenv("POD_NAME"))
}

// wearQueenLabel puts the queen's label and term on this pod.
func (a *App) wearQueenLabel(ctx context.Context, term int64) {
	label, pod := queenLabel()
	if label == "" || pod == "" {
		return
	}
	if _, err := kubectl(ctx, "annotate", "pod", pod, "--overwrite", fmt.Sprintf("%s=%d", queenTermAnnotation, term)); err != nil {
		log.Printf("hive: %v", err)
	}
	if _, err := kubectl(ctx, "label", "pod", pod, "--overwrite", label+"=yes"); err != nil {
		log.Printf("hive: %v", err)
	}
}

// labelledQueens is every pod wearing the queen's label, with its term.
func labelledQueens(ctx context.Context, label string) (map[string]int64, error) {
	out, err := kubectl(ctx, "get", "pods", "-l", label+"=yes", "-o",
		`jsonpath={range .items[*]}{.metadata.name}{" "}{.metadata.annotations.superai/queen-term}{"\n"}{end}`)
	if err != nil {
		return nil, err
	}
	pods := map[string]int64{}
	for _, line := range strings.Split(out, "\n") {
		name, term, _ := strings.Cut(strings.TrimSpace(line), " ")
		if name == "" {
			continue
		}
		t, _ := strconv.ParseInt(strings.TrimSpace(term), 10, 64)
		pods[name] = t
	}
	return pods, nil
}

// yields is whether a queen of (term, name) gives way to one of (other,
// otherName): a later term wins, and between equal terms the lower name, so
// two queens never both stay.
func yields(term int64, name string, other int64, otherName string) bool {
	return other > term || (other == term && otherName < name)
}

// keepQueenLabel is a queen's watch on the label: hers to wear, queens of an
// earlier term to take it off, and a queen of a later term to step down
// before. It runs while this instance is queen.
func (a *App) keepQueenLabel(ctx context.Context) {
	label, pod := queenLabel()
	if label == "" || pod == "" {
		return
	}
	check := func() {
		a.mu.Lock()
		h := a.hive
		a.mu.Unlock()
		if h == nil {
			return
		}
		term := h.Term()
		pods, err := labelledQueens(ctx, label)
		if err != nil {
			log.Printf("hive: %v", err)
			return
		}
		for other, t := range pods {
			if other == pod {
				continue
			}
			if yields(term, pod, t, other) {
				go a.stepDown(fmt.Sprintf("%s is queen on term %d, after this one's %d", other, t, term))
				return
			}
			if _, err := kubectl(ctx, "label", "pod", other, label+"-"); err != nil {
				log.Printf("hive: %v", err)
			} else {
				log.Printf("hive: took the queen's label off %s (term %d, before this one's %d)", other, t, term)
			}
		}
		if t, ok := pods[pod]; !ok || t != term {
			a.wearQueenLabel(ctx, term)
		}
	}
	check()
	tick := time.NewTicker(queenLabelEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			check()
		}
	}
}

// isQueen is whether this instance is the hive's queen now.
func (a *App) isQueen() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings != nil && a.settings.Hive.Role == backend.HiveRoleQueen
}

// queenRestartHold is how long the members wait for a queen that said she
// is restarting before they elect another.
const queenRestartHold = 3 * time.Minute

// announceRestart tells every member a queen who is stopping on purpose (a
// rollout, a settings change that restarts her) that she will be back, so
// they wait for her instead of electing another. A queen that goes without
// saying so — her node gone, her process killed — is replaced at once.
func (a *App) announceRestart() {
	if !electionOn() || !a.isQueen() {
		return
	}
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h == nil {
		return
	}
	creds, err := loadOrCreateCredentials()
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, m := range h.Roster() {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			body := fmt.Sprintf(`{"seconds":%d}`, int(queenRestartHold/time.Second))
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(url, "/")+"/api/hive/hold", strings.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+creds.Token)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
			}
		}(m.URL)
	}
	wg.Wait()
	log.Printf("hive: told %d members the queen is restarting", len(h.Roster()))
}

// handleHiveHold is a member hearing that its queen is restarting: it waits
// for her for as long as she asked, at most queenRestartHold.
//
//	POST /api/hive/hold  {"seconds":180}
func (a *App) handleHiveHold(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	var body struct {
		Seconds int `json:"seconds"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)
	d := time.Duration(body.Seconds) * time.Second
	if d <= 0 || d > queenRestartHold {
		d = queenRestartHold
	}
	a.mu.Lock()
	ann := a.hiveAnn
	a.mu.Unlock()
	if ann == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "this instance is not a worker"})
		return
	}
	ann.Hold(d)
	writeJSONStatus(w, http.StatusOK, map[string]any{"ok": true, "seconds": int(d / time.Second)})
}
