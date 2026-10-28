package app

import (
	"encoding/json"
	"github.com/liliang-cn/superleo/internal/backend"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The first start's "paste an API key": the model list for picking, a real
// one-token answer for "it works", and the endpoint's own words when not.
func TestCheckingAModelListsModelsAndAsksTheChosenOne(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`{"data":[{"id":"model-b"},{"id":"model-a"}]}`))
		case "/v1/chat/completions":
			var body struct {
				Model string `json:"model"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Model != "model-a" {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error":{"message":"no such model"}}`))
				return
			}
			w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
		}
	}))
	defer llm.Close()
	a := NewApp()

	got := a.CheckModel(llm.URL+"/v1/", "good", "")
	if !got.OK || strings.Join(got.Models, ",") != "model-a,model-b" {
		t.Fatalf("listing: %+v", got)
	}
	if got := a.CheckModel(llm.URL+"/v1", "good", "model-a"); !got.OK || got.Error != "" {
		t.Fatalf("a good model: %+v", got)
	}
	if got := a.CheckModel(llm.URL+"/v1", "good", "model-x"); got.OK || !strings.Contains(got.Error, "no such model") {
		t.Fatalf("a wrong model: %+v", got)
	}
	if got := a.CheckModel(llm.URL+"/v1", "bad", "model-a"); got.OK || !strings.Contains(got.Error, "invalid api key") {
		t.Fatalf("a wrong key: %+v", got)
	}
	if got := a.CheckModel("", "good", "model-a"); got.OK || got.Error == "" {
		t.Fatalf("no address: %+v", got)
	}
}

// A fresh SuperLeo leaves the coding CLIs' configs alone until it is allowed
// to use the agents here; a hive member, set up without a first start, may.
func TestTheCLIHooksWaitUntilTheAgentsAreAllowed(t *testing.T) {
	a := NewApp()
	a.settings = &backend.Settings{}
	if a.mayInstallCLIHooks() {
		t.Fatal("a first start installs hooks before it is allowed")
	}
	a.settings = &backend.Settings{ExternalAgents: backend.ExternalAgents{Enabled: true}}
	if !a.mayInstallCLIHooks() {
		t.Fatal("allowed agents, no hooks")
	}
	a.settings = &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleWorker}}
	if !a.mayInstallCLIHooks() {
		t.Fatal("a hive member gets no hooks")
	}
}
