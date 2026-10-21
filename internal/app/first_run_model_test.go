package app

import (
	"encoding/json"
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
