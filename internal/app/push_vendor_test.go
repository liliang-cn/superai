package app

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
)

type vendorSent struct {
	provider, token string
	push            vendorPush
}

// fakeVendors records what each provider was handed.
type fakeVendors struct {
	mu  sync.Mutex
	got []vendorSent
}

type fakeVendor struct {
	f        *fakeVendors
	provider string
}

func (v fakeVendor) Send(_ context.Context, t pushTarget, p vendorPush) error {
	v.f.mu.Lock()
	v.f.got = append(v.f.got, vendorSent{v.provider, t.Token, p})
	v.f.mu.Unlock()
	return nil
}

func (f *fakeVendors) pick(provider string) vendorSender {
	if provider == pushAPNs {
		return nil
	}
	return fakeVendor{f, provider}
}

func (f *fakeVendors) sent() []vendorSent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]vendorSent(nil), f.got...)
}

// withAndroids adds a Xiaomi phone on MiPush and one holding the stream (sse)
// to the two iPhones pairedWithPush makes.
func withAndroids(t *testing.T) *deviceStore {
	t.Helper()
	s := pairedWithPush(t)
	for _, p := range [][2]string{{"mipush", "Mi+RegId/0123456789"}, {pushSSE, "install-0123456789"}} {
		code, _ := s.newCode()
		d, _, ok, err := s.claim(code, "android "+p[0])
		if !ok || err != nil {
			t.Fatal(ok, err)
		}
		if err := s.setPushToken(d.ID, p[1], "", p[0]); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestNoticesAreDispatchedPerProvider(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	store := withAndroids(t)
	apple := &fakeAlerts{}
	vendors := &fakeVendors{}
	np := noticePush{
		pusher:  func() noticePusher { return apple },
		devices: func() (*deviceStore, error) { return store, nil },
		vendors: vendors.pick,
	}
	np.send(context.Background(), backend.Notice{ID: "ntf_1", Title: "该出发了", Message: "**17:10** 出发", Kind: backend.KindReminder, Push: true})

	if got := apple.sent(); len(got) != 2 {
		t.Fatalf("APNs got %d, want the two iPhones: %+v", len(got), got)
	}
	got := vendors.sent()
	if len(got) != 2 {
		t.Fatalf("vendors got %+v", got)
	}
	by := map[string]vendorSent{}
	for _, v := range got {
		by[v.provider] = v
	}
	mi := by["mipush"]
	if mi.token != "Mi+RegId/0123456789" || mi.push.Silent || mi.push.Title != "该出发了" || mi.push.Body != "17:10 出发" || mi.push.Channel != "reminder" || mi.push.Data["id"] != "ntf_1" {
		t.Errorf("mipush: %+v", mi)
	}
	if _, ok := by[pushSSE]; !ok {
		t.Error("the sse phone was not dispatched")
	}

	// An approval card goes on the approval channel.
	vendors.got = nil
	np.deliver(context.Background(), ccApprovalAlert(backend.ApprovalRequest{ID: "ap1", By: "mac", Tool: "Bash"}), false)
	for _, v := range vendors.sent() {
		if v.push.Channel != "approval" || v.push.Data["kind"] != "approval" {
			t.Errorf("approval: %+v", v)
		}
	}

	// No APNs client: the Android phones are still reached.
	vendors.got = nil
	np.pusher = func() noticePusher { return nil }
	np.send(context.Background(), backend.Notice{Title: "简报", Message: "x", Kind: backend.KindBrief, Push: true})
	if got := vendors.sent(); len(got) != 2 || got[0].push.Channel != "general" {
		t.Errorf("without APNs: %+v", got)
	}
}

func TestAlarmWakesReachAndroidWithoutAPNs(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	store := withAndroids(t)
	vendors := &fakeVendors{}
	w := newAlarmWatcher()
	w.devices = func() (*deviceStore, error) { return store, nil }
	w.vendors = vendors.pick
	w.pushAll(context.Background(), "v9")
	got := vendors.sent()
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	for _, v := range got {
		if !v.push.Silent || v.push.Data["kind"] != "alarms" || v.push.Data["version"] != "v9" {
			t.Errorf("%+v", v)
		}
	}
}

func TestUnconfiguredVendorsSayOnceAndDrop(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	for p := range pushProviders {
		s := vendorSenderFor(p)
		switch p {
		case pushAPNs:
			if s != nil {
				t.Errorf("apns has a vendor sender")
			}
			continue
		case pushSSE:
			if _, ok := s.(sseSender); !ok {
				t.Errorf("sse sender is %T", s)
			}
			continue
		}
		if s == nil {
			t.Fatalf("%s has no sender", p)
		}
		fresh := &unconfiguredSender{provider: p}
		for range 3 {
			if err := fresh.Send(context.Background(), pushTarget{DeviceID: "d1", Provider: p}, vendorPush{}); err != nil {
				t.Errorf("%s: %v", p, err)
			}
		}
		if n := strings.Count(buf.String(), "provider "+p+" not configured"); n != 1 {
			t.Errorf("%s logged %d times: %s", p, n, buf.String())
		}
	}
}
