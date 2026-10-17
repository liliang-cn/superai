package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHeartbeatCanBeAskedFor(t *testing.T) {
	cases := map[string]time.Duration{
		"/api/events":                           defaultHeartbeat,
		"/api/events?heartbeat=":                defaultHeartbeat,
		"/api/events?heartbeat=soon":            defaultHeartbeat,
		"/api/events?heartbeat=60":              60 * time.Second,
		"/api/events?heartbeat=1":               minHeartbeat,
		"/api/events?heartbeat=9999":            maxHeartbeat,
		"/api/events?only=notice&heartbeat=120": 120 * time.Second,
	}
	for url, want := range cases {
		if got := heartbeatEvery(httptest.NewRequest(http.MethodGet, url, nil)); got != want {
			t.Errorf("%s: %v, want %v", url, got, want)
		}
	}
}
