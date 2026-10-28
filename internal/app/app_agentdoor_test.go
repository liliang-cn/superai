package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
)

// A machine joining the desktop app is told the agent link is on the same
// address it reached the claim by: nobody has to tell the app its own.
func TestTheClaimSaysTheLinkIsWhereTheClaimCameIn(t *testing.T) {
	t.Setenv(agentAdvertiseEnv, "")
	c := testCreds(t, "hunter2")
	s, err := openDevices(filepath.Join(t.TempDir(), devicesFile))
	if err != nil {
		t.Fatal(err)
	}
	c.devices = s
	mux := http.NewServeMux()
	pairRoutesLinking(mux, c, func() int { return desktopAgentPort })
	code, _ := s.newCode()
	r := httptest.NewRequest(http.MethodPost, pairClaimPath, strings.NewReader(`{"code":"`+code+`","device":"agent box"}`))
	r.Host = "192.168.123.123:43781"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	var got struct {
		Token     string `json:"token"`
		AgentLink string `json:"agent_link"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || got.Token == "" || got.AgentLink != "192.168.123.123:43782" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if !credentialsCheck(c)(got.Token) {
		t.Fatal("the claimed token does not open the agent link")
	}
}

// Settings gets the command to run on the other machine, once per address
// this one has; a door that is not open says why instead of a command that
// would not work.
func TestAddingAnAgentGivesTheCommandToRun(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	a := &App{}
	if j, err := a.AgentJoinCode(); err != nil || j.Error == "" || j.Code != "" {
		t.Fatalf("a closed door gave %+v %v", j, err)
	}
	a.agentDoor.set(desktopClaimPort, desktopAgentPort, nil)
	j, err := a.AgentJoinCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Code) != 6 || j.ExpiresAt.IsZero() {
		t.Fatalf("%+v", j)
	}
	for _, c := range j.Commands {
		if !strings.HasPrefix(c, "superleo agent join http://") || !strings.HasSuffix(c, ":43781 "+j.Code) {
			t.Fatalf("command %q", c)
		}
	}
	if len(j.Commands) == 0 && j.Error == "" {
		t.Fatal("no command and no reason")
	}
}

// A SuperLeo on its own becomes the queen of the machines that joined it once
// one of them can think; one the person kept on its own, or a worker, stays.
func TestAThinkingAgentMakesASoloSuperLeoItsQueen(t *testing.T) {
	for _, c := range []struct {
		name string
		hive backend.HiveSettings
		want string
	}{
		{"on its own", backend.HiveSettings{}, backend.HiveRoleQueen},
		{"kept on its own", backend.HiveSettings{Standalone: true}, ""},
		{"a worker", backend.HiveSettings{Role: backend.HiveRoleWorker, JoinURL: "http://q:1"}, backend.HiveRoleWorker},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("SUPERLEO_HOME", t.TempDir())
			a := NewApp()
			s, err := backend.LoadSettings()
			if err != nil {
				t.Fatal(err)
			}
			s.CLIProxyEnabled = false
			s.Hive = c.hive
			a.settings = s
			defer a.Shutdown(context.Background())
			a.queenForWorkers("box")
			if got := a.settings.Hive.Role; got != c.want {
				t.Fatalf("role %q, want %q", got, c.want)
			}
		})
	}
}
