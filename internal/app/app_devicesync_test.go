package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// A member takes its queen's paired devices from the hive's credential only,
// and from then on knows their tokens: a phone paired with the old queen is
// let in by the new one.
func TestAMemberTakesTheQueensDevicesAndKnowsThem(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	creds, err := loadOrCreateCredentials()
	if err != nil {
		t.Fatal(err)
	}
	a := &App{settings: &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleWorker}}}
	phoneTok := "phone-token"
	list := []Device{{ID: "d1", Name: "iPhone", TokenHash: hashToken(phoneTok), CreatedAt: time.Now()}}
	body := `[{"id":"d1","name":"iPhone","token_hash":"` + hashToken(phoneTok) + `","created_at":"2026-10-27T00:00:00Z"}]`
	post := func(tok string) int {
		r := httptest.NewRequest(http.MethodPost, hiveDevicesPath, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		a.handleHiveDevices(w, r)
		return w.Code
	}
	if code := post(phoneTok); code != http.StatusForbidden {
		t.Fatalf("a device's own token rewrote the list: %d", code)
	}
	if code := post(creds.Token); code != http.StatusOK {
		t.Fatalf("the hive's credential was refused: %d", code)
	}
	s, _ := openDevices(devicesPath())
	if !s.authenticate(phoneTok) || len(list) != len(s.snapshot()) {
		t.Fatal("the phone paired with the old queen is not known")
	}
	a.settings.Hive.Role = backend.HiveRoleQueen
	if code := post(creds.Token); code != http.StatusConflict {
		t.Fatalf("a queen took another's list: %d", code)
	}
}

// On a network the devices come with the roster.
func TestTheRosterBringsTheDevices(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	a := &App{}
	_, err := a.HiveRoster(LanRoster{Queen: "mac", Key: "k", Devices: []Device{{ID: "d", TokenHash: hashToken("t")}}})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := openDevices(devicesPath())
	if !s.authenticate("t") {
		t.Fatal("the roster's devices were not kept")
	}
}
