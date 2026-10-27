package app

// The paired devices go wherever the queen goes.
//
// A phone or a machine pairs with the queen, and she alone keeps its token's
// hash (devices.json). When another member is elected queen, a device paired
// before then has to be known to her too, or every phone and agent is turned
// away the moment the hive changes queen — which it was, the first time a
// queen was replaced on the cluster. So the queen hands her list to every
// member as it changes: to the pods' hive over HTTP (shareDevices), to a hive
// on a network with the roster (LanRoster.Devices). A member keeps it on disk
// and in the store it would check tokens against, so the one elected knows
// them all from her first request.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	hiveDevicesPath = "/api/hive/devices"
	// shareDevicesEvery is how often a queen looks for a change to share,
	// and shareDevicesAnyway how often she sends the list regardless, for
	// a member that joined since.
	shareDevicesEvery  = 15 * time.Second
	shareDevicesAnyway = 2 * time.Minute
)

// snapshot is the devices as they are now.
func (s *deviceStore) snapshot() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Device(nil), s.devices...)
}

// adopt replaces the devices with the queen's list and keeps it.
func (s *deviceStore) adopt(list []Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices = append([]Device(nil), list...)
	return s.saveLocked()
}

func devicesDigest(list []Device) string {
	b, _ := json.Marshal(list)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// queenDevices is the queen's list to hand on.
func queenDevices() []Device {
	s, err := openDevices(devicesPath())
	if err != nil {
		return nil
	}
	return s.snapshot()
}

// shareDevices is a pod queen handing her paired devices to her members.
func (a *App) shareDevices(ctx context.Context) {
	creds, err := loadOrCreateCredentials()
	if err != nil {
		return
	}
	last, sent := "", time.Time{}
	tick := time.NewTicker(shareDevicesEvery)
	defer tick.Stop()
	for {
		list := queenDevices()
		if d := devicesDigest(list); d != last || time.Since(sent) > shareDevicesAnyway {
			a.mu.Lock()
			h := a.hive
			a.mu.Unlock()
			if h != nil {
				body, _ := json.Marshal(list)
				ok := 0
				for _, m := range h.Roster() {
					rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
					req, _ := http.NewRequestWithContext(rctx, http.MethodPost, strings.TrimRight(m.URL, "/")+hiveDevicesPath, bytes.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "Bearer "+creds.Token)
					if resp, err := http.DefaultClient.Do(req); err == nil {
						if resp.StatusCode == http.StatusOK {
							ok++
						}
						resp.Body.Close()
					}
					cancel()
				}
				last, sent = d, time.Now()
				if ok > 0 {
					log.Printf("hive: shared %d paired devices with %d members", len(list), ok)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// handleHiveDevices is a member taking its queen's list. Only the hive's own
// credential may hand it one — the core token, or a hive key — never a
// paired device's, which could otherwise rewrite who is paired.
func (a *App) handleHiveDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	creds, err := loadOrCreateCredentials()
	if err != nil || !(subtle.ConstantTimeCompare([]byte(tok), []byte(creds.Token)) == 1 || hiveKeyMatches(tok)) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "only the hive's queen hands out its devices"})
		return
	}
	if a.isQueen() {
		writeJSONStatus(w, http.StatusConflict, map[string]any{"error": "a queen keeps her own devices"})
		return
	}
	var list []Device
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&list); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := adoptDevices(list); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "devices": len(list)})
}

func adoptDevices(list []Device) error {
	s, err := openDevices(devicesPath())
	if err != nil {
		return err
	}
	if devicesDigest(s.snapshot()) == devicesDigest(list) {
		return nil
	}
	return s.adopt(list)
}
