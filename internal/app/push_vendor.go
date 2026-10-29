package app

// Push to Android phones. A device registers a token with a provider
// (RegisterPushToken, pairing.go); APNs tokens go through the APNs client as
// they always have (postnotify.go, app_alarms.go), every other provider
// through its sender here:
//
//	mipush  Xiaomi / Redmi / POCO (HyperOS, MIUI)
//	hms     Huawei (HMS Push Kit)
//	honor   Honor (Honor Push)
//	oppo    OPPO / OnePlus / realme (ColorOS push)
//	vivo    vivo / iQOO (OriginOS push)
//	sse     no vendor channel: the phone holds /api/events open (保持连接)
//	        and has already had the event a push would carry
//
// The vendor senders are wired but not implemented: each needs a developer
// account's app id and secret, and which one is worth it depends on the phone
// in hand. Until one is, a push to that provider logs once that it is not
// configured and is dropped; the phone still catches up when the app opens
// or its periodic job runs.

import (
	"context"
	"log/slog"
	"sync"
)

const pushSSE = "sse"

// vendorPush is what one push says, whatever carries it. A silent one only
// wakes the app (alarms changed); an alert is shown.
type vendorPush struct {
	Silent bool
	Title  string
	Body   string
	// Channel is the Android notification channel the alert belongs on:
	// approval, reminder or general.
	Channel string
	Data    map[string]any
}

// vendorSender delivers to one provider's tokens.
type vendorSender interface {
	Send(ctx context.Context, t pushTarget, p vendorPush) error
}

// unconfiguredSender stands in for a vendor channel nobody set up: it says so
// once per process and drops the push.
type unconfiguredSender struct {
	provider string
	once     sync.Once
}

func (s *unconfiguredSender) Send(ctx context.Context, t pushTarget, _ vendorPush) error {
	s.once.Do(func() {
		slog.WarnContext(ctx, "push: provider not configured; device gets no push until it is", "provider", s.provider, "device", t.DeviceID)
	})
	return nil
}

// sseSender sends nothing: an "sse" device keeps the event stream open and
// was told by the event itself.
type sseSender struct{}

func (sseSender) Send(context.Context, pushTarget, vendorPush) error { return nil }

// defaultVendorSenders is the one sender per non-APNs provider, built once so
// each unconfigured provider logs once.
var defaultVendorSenders = sync.OnceValue(func() map[string]vendorSender {
	m := map[string]vendorSender{pushSSE: sseSender{}}
	for p := range pushProviders {
		if p != pushAPNs && p != pushSSE {
			m[p] = &unconfiguredSender{provider: p}
		}
	}
	return m
})

// vendorSenderFor is the sender for provider; nil for APNs, which has its own path.
func vendorSenderFor(provider string) vendorSender {
	if provider == pushAPNs {
		return nil
	}
	return defaultVendorSenders()[provider]
}

// pushToVendors sends p to every device registered with a provider other
// than APNs. senders picks the sender (vendorSenderFor when nil).
func pushToVendors(ctx context.Context, store *deviceStore, what string, senders func(string) vendorSender, p vendorPush) {
	if senders == nil {
		senders = vendorSenderFor
	}
	for _, t := range store.allPushTargets() {
		if t.Provider == pushAPNs {
			continue
		}
		s := senders(t.Provider)
		if s == nil {
			continue
		}
		if err := s.Send(ctx, t, p); err != nil {
			slog.WarnContext(ctx, "push to device", "module", what, "provider", t.Provider, "device", t.DeviceID, "error", err)
		}
	}
}
