package app

import (
	"encoding/json"
	"sync"

	"github.com/liliang-cn/superleo/internal/backend"
)

// Settings are changed one field at a time.
//
// The settings page used to keep a copy of the whole file and save it whole.
// A card that changed something on its own (desktop control's list of apps
// left alone) was then undone by the page's next save of its older copy: the
// person removed WeChat from the list, and a minute later it was back. A
// patch names only what it changes, so nothing it does not name can be put
// back by it.

var settingsPatchMu sync.Mutex // one patch read-merge-write at a time

// PatchSettings applies a JSON merge patch (RFC 7396) to the settings and
// saves them: the keys given replace what is there, objects are merged key
// by key, and null removes a key. It returns the settings as saved.
func (a *App) PatchSettings(patch map[string]any) (backend.Settings, error) {
	settingsPatchMu.Lock()
	defer settingsPatchMu.Unlock()
	next, err := mergeSettingsPatch(a.GetSettings(), patch)
	if err != nil {
		return backend.Settings{}, err
	}
	if err := a.SaveSettings(next); err != nil {
		return backend.Settings{}, err
	}
	return a.GetSettings(), nil
}

func mergeSettingsPatch(cur backend.Settings, patch map[string]any) (backend.Settings, error) {
	raw, err := json.Marshal(cur)
	if err != nil {
		return cur, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return cur, err
	}
	mergePatch(doc, patch)
	raw, err = json.Marshal(doc)
	if err != nil {
		return cur, err
	}
	var next backend.Settings
	if err := json.Unmarshal(raw, &next); err != nil {
		return cur, err
	}
	return next, nil
}

func mergePatch(dst map[string]any, patch map[string]any) {
	for k, v := range patch {
		if v == nil {
			delete(dst, k)
			continue
		}
		if pv, ok := v.(map[string]any); ok {
			if dv, ok := dst[k].(map[string]any); ok {
				mergePatch(dv, pv)
				continue
			}
		}
		dst[k] = v
	}
}
