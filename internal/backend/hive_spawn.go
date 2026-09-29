package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Making workers.
//
// A queen that can only command what already exists is a queen someone has to
// keep feeding. This is how it makes more, and how it lets some go.
//
// The only way it does that today is by changing the replica count of the
// StatefulSet the workers run in. That is a smaller idea than "start a
// process", and it is the right one here: the cluster already knows how to
// place, restart and give a name to a pod, the new worker announces itself to
// the queen on its own (hive.go), and nothing the queen has to remember about
// a worker it created is left to go stale — the roster is the truth.
//
// It costs one thing worth knowing: a StatefulSet scales by ordinal. Growing
// adds the next numbers; shrinking removes the highest. The queen can ask for
// fewer workers but not for a particular one to go.

// SpawnerSettings says how this queen makes workers. Absent means it cannot,
// which is the default: creating pods is a power, and a queen that was never
// given it is one that cannot be talked into using it.
type SpawnerSettings struct {
	// Kind is "kubectl", the only one so far.
	Kind        string `json:"kind"`
	Namespace   string `json:"namespace,omitempty"`
	StatefulSet string `json:"statefulset,omitempty"`
	// MaxWorkers caps the replica count. Zero takes 20. A cap that lives in
	// settings rather than in a prompt is the one that holds when the model
	// has been asked, politely and at length, for a thousand.
	MaxWorkers int `json:"max_workers,omitempty"`
	// Kubectl is the binary; empty finds it on PATH.
	Kubectl string `json:"kubectl,omitempty"`
}

// DefaultMaxWorkers is the cap when the settings do not set one.
const DefaultMaxWorkers = 20

// Spawner changes how many workers there are.
type Spawner interface {
	Replicas(ctx context.Context) (int, error)
	SetReplicas(ctx context.Context, n int) error
	Max() int
	// Prefix is what every worker's name starts with; a worker's ordinal
	// follows it after a dash.
	Prefix() string
}

// NewSpawner builds the spawner the settings describe, or nil when they
// describe none.
func NewSpawner(s *SpawnerSettings) (Spawner, error) {
	if s == nil || strings.TrimSpace(s.Kind) == "" {
		return nil, nil
	}
	if s.Kind != "kubectl" {
		return nil, fmt.Errorf("spawner kind %q is not supported; only \"kubectl\"", s.Kind)
	}
	if strings.TrimSpace(s.StatefulSet) == "" {
		return nil, errors.New("spawner needs the name of the statefulset the workers run in")
	}
	k := &kubectlSpawner{s: *s}
	if k.s.Namespace == "" {
		k.s.Namespace = "default"
	}
	if k.s.MaxWorkers <= 0 {
		k.s.MaxWorkers = DefaultMaxWorkers
	}
	if k.s.Kubectl == "" {
		k.s.Kubectl = "kubectl"
	}
	return k, nil
}

type kubectlSpawner struct{ s SpawnerSettings }

func (k *kubectlSpawner) Max() int       { return k.s.MaxWorkers }
func (k *kubectlSpawner) Prefix() string { return k.s.StatefulSet }

func (k *kubectlSpawner) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// An argument vector, never a shell string: the names come from settings,
	// and settings are not a place to be trusted with quoting.
	cmd := exec.CommandContext(ctx, k.s.Kubectl, append([]string{"-n", k.s.Namespace}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("kubectl: %s", msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func (k *kubectlSpawner) Replicas(ctx context.Context) (int, error) {
	out, err := k.run(ctx, "get", "statefulset", k.s.StatefulSet, "-o", "jsonpath={.spec.replicas}")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0, fmt.Errorf("kubectl answered %q for the replica count", out)
	}
	return n, nil
}

func (k *kubectlSpawner) SetReplicas(ctx context.Context, n int) error {
	if n < 0 || n > k.s.MaxWorkers {
		return fmt.Errorf("%d workers is outside 0..%d", n, k.s.MaxWorkers)
	}
	_, err := k.run(ctx, "scale", "statefulset", k.s.StatefulSet, fmt.Sprintf("--replicas=%d", n))
	return err
}
