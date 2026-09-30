package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKubectl writes a script that records its arguments and answers the one
// question the spawner asks.
func fakeKubectl(t *testing.T, replicas string) (bin, log string) {
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, "kubectl")
	// printf, not echo: the arguments start with -n, and a POSIX sh's echo
	// (dash, on the CI runner) takes that as its own option and drops it.
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\ncase \"$*\" in *jsonpath*) printf '%s' " + replicas + " ;; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func TestNoSpawnerSettingsMeansNoSpawner(t *testing.T) {
	sp, err := NewSpawner(nil)
	if sp != nil || err != nil {
		t.Fatalf("%v %v", sp, err)
	}
	if sp, err := NewSpawner(&SpawnerSettings{}); sp != nil || err != nil {
		t.Fatalf("%v %v", sp, err)
	}
}

func TestAnUnknownSpawnerKindIsRefused(t *testing.T) {
	if _, err := NewSpawner(&SpawnerSettings{Kind: "ssh"}); err == nil {
		t.Fatal("accepted a kind it cannot do")
	}
	if _, err := NewSpawner(&SpawnerSettings{Kind: "kubectl"}); err == nil {
		t.Fatal("accepted a spawner with nothing to scale")
	}
}

func TestTheSpawnerReadsAndSetsTheReplicaCount(t *testing.T) {
	bin, log := fakeKubectl(t, "4")
	sp, err := NewSpawner(&SpawnerSettings{Kind: "kubectl", Namespace: "hive", StatefulSet: "w", Kubectl: bin})
	if err != nil {
		t.Fatal(err)
	}
	n, err := sp.Replicas(context.Background())
	if err != nil || n != 4 {
		t.Fatalf("%d %v", n, err)
	}
	if err := sp.SetReplicas(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	got := string(calls)
	for _, want := range []string{"-n hive get statefulset w -o jsonpath={.spec.replicas}", "-n hive scale statefulset w --replicas=7"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing call %q in:\n%s", want, got)
		}
	}
}

func TestTheCapHoldsWhateverIsAsked(t *testing.T) {
	bin, log := fakeKubectl(t, "1")
	sp, _ := NewSpawner(&SpawnerSettings{Kind: "kubectl", StatefulSet: "w", MaxWorkers: 5, Kubectl: bin})
	if sp.Max() != 5 {
		t.Fatalf("max %d", sp.Max())
	}
	if err := sp.SetReplicas(context.Background(), 6); err == nil {
		t.Fatal("went past the cap")
	}
	if err := sp.SetReplicas(context.Background(), -1); err == nil {
		t.Fatal("went below zero")
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "scale") {
		t.Fatal("a refused request still reached the cluster")
	}
	def, _ := NewSpawner(&SpawnerSettings{Kind: "kubectl", StatefulSet: "w"})
	if def.Max() != DefaultMaxWorkers {
		t.Fatalf("default cap %d", def.Max())
	}
}

func TestAKubectlFailureSaysWhatKubectlSaid(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "kubectl")
	os.WriteFile(bin, []byte("#!/bin/sh\necho 'forbidden: cannot patch statefulsets' >&2\nexit 1\n"), 0o755)
	sp, _ := NewSpawner(&SpawnerSettings{Kind: "kubectl", StatefulSet: "w", Kubectl: bin})
	err := sp.SetReplicas(context.Background(), 2)
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("%v", err)
	}
}
