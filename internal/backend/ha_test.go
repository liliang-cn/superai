package backend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A haify controller the way the SDS cluster has it: openclaw active on
// node-e with node-b ready, cortexdb with nothing ready to take over.
func TestHaifyServices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/nodes":
			w.Write([]byte(`{"success":true,"nodes":[{"name":"node-b","hostname":"sds-b","address":"192.168.123.227"},{"name":"node-e","hostname":"sds-e"}]}`))
		case "/v1/ha":
			w.Write([]byte(`{"success":true,"configs":[
				{"resource":"openclaw","vip":"192.168.123.252/24","services":["openclaw.service"]},
				{"resource":"cortexdb","vip":"192.168.123.253/24","services":["cortexdb-grpc.service"]}]}`))
		case "/v1/ha/openclaw/status":
			w.Write([]byte(`{"success":true,"promoters":[{"drbdResource":"openclaw","primaryOn":"sds-e","activeNode":"node-e","standbys":[{"node":"node-b","ready":true,"diskState":"UpToDate"}]}]}`))
		case "/v1/ha/cortexdb/status":
			// snake_case, as other haify front ends write it.
			w.Write([]byte(`{"success":true,"promoters":[{"primary_on":"sds-b","standbys":[{"node":"node-e","ready":false}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	got, err := HaifyServices(context.Background(), HaifySource{URL: srv.URL, Token: "tok"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("services: %+v", got)
	}
	cx, oc := got[0], got[1]
	if oc.Name != "openclaw" || oc.VIP != "192.168.123.252" || oc.Active != "sds-e" || oc.State != "active" ||
		len(oc.Standbys) != 1 || oc.Standbys[0].Host != "sds-b" || !oc.Standbys[0].Ready {
		t.Fatalf("openclaw: %+v", oc)
	}
	if !oc.Serves("openclaw") || oc.Serves("hermes") {
		t.Fatalf("openclaw serves: %v", oc.Units)
	}
	if cx.Name != "cortexdb" || cx.Active != "sds-b" || cx.State != "degraded" {
		t.Fatalf("cortexdb: %+v", cx)
	}

	if _, err := HaifyServices(context.Background(), HaifySource{URL: srv.URL, Token: "wrong"}, nil); err == nil {
		t.Fatal("a wrong token went through")
	}
}
