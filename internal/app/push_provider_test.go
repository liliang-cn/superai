package app

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestAndroidPhonesRegisterVendorTokens(t *testing.T) {
	h, c, _ := pairingServer(t)
	claim := func(name string) (string, string) {
		code := startCode(t, h, c)
		rec := call(h, http.MethodPost, pairClaimPath, "", `{"code":"`+code+`","device":"`+name+`"}`)
		var got struct {
			Token    string `json:"token"`
			DeviceID string `json:"device_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Token == "" {
			t.Fatalf("claim: %d %s", rec.Code, rec.Body)
		}
		return got.Token, got.DeviceID
	}
	iphone, iphoneID := claim("iPhone")
	android, androidID := claim("Xiaomi")
	apnsTok := strings.Repeat("ab", 32)
	miTok := "Mi+RegId/AbC=123456"

	// The iOS app's two arguments mean APNs, as before.
	if rec := call(h, http.MethodPost, registerPushPath, iphone, `["`+apnsTok+`","sandbox"]`); rec.Code != http.StatusOK {
		t.Fatalf("apns: %d %s", rec.Code, rec.Body)
	}
	// A vendor token keeps its case and ignores env.
	if rec := call(h, http.MethodPost, registerPushPath, android, `["`+miTok+`","production","MiPush"]`); rec.Code != http.StatusOK {
		t.Fatalf("mipush: %d %s", rec.Code, rec.Body)
	}
	all := map[string]pushTarget{}
	for _, t := range c.devices.allPushTargets() {
		all[t.DeviceID] = t
	}
	if got := all[androidID]; got.Token != miTok || got.Provider != "mipush" || got.Env != "" {
		t.Errorf("android: %+v", got)
	}
	if got := all[iphoneID]; got.Provider != pushAPNs || got.Env != "sandbox" {
		t.Errorf("iphone: %+v", got)
	}
	// APNs is sent only APNs tokens.
	if ts := c.devices.pushTargets(); len(ts) != 1 || ts[0].DeviceID != iphoneID {
		t.Errorf("apns targets: %+v", ts)
	}
	// "apns" said out loud is the same as saying nothing.
	if rec := call(h, http.MethodPost, registerPushPath, iphone, `["`+apnsTok+`","production","apns"]`); rec.Code != http.StatusOK {
		t.Errorf("explicit apns: %d", rec.Code)
	}

	for _, body := range []string{
		`["` + miTok + `","","fcm"]`,     // not a provider we take
		`["short","","hms"]`,             // too short to be a token
		`["has space token","","hms"]`,   // not printable without spaces
		`["` + miTok + `","","hms","x"]`, // too many arguments
		`["` + miTok + `","sandbox"]`,    // two arguments are APNs: not hex
	} {
		if rec := call(h, http.MethodPost, registerPushPath, android, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}

	// Unregistering clears the provider too, and the file keeps old devices readable.
	call(h, http.MethodPost, registerPushPath, android, `["","","mipush"]`)
	if len(c.devices.allPushTargets()) != 1 {
		t.Errorf("unregistered: %+v", c.devices.allPushTargets())
	}
	raw, _ := os.ReadFile(c.devices.path)
	if strings.Contains(string(raw), "push_provider") {
		t.Errorf("an APNs-only file grew a provider field: %s", raw)
	}
}
