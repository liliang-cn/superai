package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/relay"
)

// phoneOver is a phone's HTTP client: it dials addr whatever the URL says, and
// trusts only the certificate with this fingerprint.
func phoneOver(addr, fp string) *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		},
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if certFingerprint(raw[0]) != fp {
					return errors.New("not the paired computer")
				}
				return nil
			},
		},
	}}
}

func claimOver(t *testing.T, c *http.Client, base, code string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code, "device": "test phone"})
	resp, err := c.Post(base+pairClaimPath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("claim over %s: %v", base, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// The desktop app's pairing link names the local network and the relay, and a
// phone holding it can pair over either, trusting only this computer.
func TestTheDesktopPairsAPhoneOverTheLANAndTheRelay(t *testing.T) {
	app := companionTestApp(t)
	app.ctx = context.Background() // the desktop app
	prev := phoneLinkPort
	phoneLinkPort = 0
	t.Cleanup(func() { phoneLinkPort = prev })
	t.Cleanup(app.stopPhoneLink)

	rl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()
	rs := &relay.Server{Domain: "localhost", Logf: func(string, ...any) {}}
	go rs.Serve(rl)
	t.Setenv("SUPERLEO_RELAY", "localhost:"+portOf(rl.Addr()))

	first, err := app.PairPhone()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(first.Link)
	if err != nil || u.Scheme != "superleo" || u.Host != "pair" {
		t.Fatalf("link %q", first.Link)
	}
	q := u.Query()
	fp := q.Get("fp")
	urls := append([]string{q.Get("server")}, q["alt"]...)
	relayed := urls[len(urls)-1]
	if fp == "" || q.Get("code") != first.Code || !strings.HasSuffix(relayed, ".localhost:"+portOf(rl.Addr())) {
		t.Fatalf("link %q", first.Link)
	}

	// Straight to the listener, as on the same network.
	lan := "127.0.0.1:" + portOf(app.phones.ln.Addr())
	if got := claimOver(t, phoneOver(lan, fp), "https://"+lan, first.Code); got != http.StatusOK {
		t.Fatalf("LAN claim: %d", got)
	}

	// Through the relay, as from anywhere else.
	id := strings.TrimPrefix(strings.SplitN(relayed, ".", 2)[0], "https://")
	for range 200 {
		if rs.Linked(id) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	second, err := app.PairPhone()
	if err != nil {
		t.Fatal(err)
	}
	if got := claimOver(t, phoneOver(rl.Addr().String(), fp), relayed, second.Code); got != http.StatusOK {
		t.Fatalf("relay claim: %d", got)
	}

	// A phone pinned to another certificate does not get as far as a request.
	body, _ := json.Marshal(map[string]string{"code": "000000", "device": "x"})
	if _, err := phoneOver(lan, "not-it").Post("https://"+lan+pairClaimPath, "application/json", bytes.NewReader(body)); err == nil {
		t.Fatal("a phone trusted a certificate it was not given")
	}

	// The certificate, and so the fingerprint, survives a restart.
	app.stopPhoneLink()
	again, err := app.PairPhone()
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := url.Parse(again.Link); u.Query().Get("fp") != fp {
		t.Fatal("the fingerprint changed across a restart")
	}
	if got := app.PhoneLinks(); got.Fingerprint != fp || len(got.URLs) == 0 {
		t.Fatalf("PhoneLinks = %+v", got)
	}
}

// Serve mode has no phone link: the page builds the link from its own address.
func TestServeModeLeavesTheLinkToThePage(t *testing.T) {
	app := companionTestApp(t)
	c, err := app.PairPhone()
	if err != nil {
		t.Fatal(err)
	}
	if c.Link != "" || app.phones != nil {
		t.Fatalf("serve mode opened a phone link: %+v", c)
	}
}

func TestVirtualInterfacesAreNotOffered(t *testing.T) {
	for _, n := range []string{"bridge100", "utun4", "vEthernet (WSL)", "VMware Network Adapter VMnet8", "docker0", "tailscale0"} {
		if !virtualInterface(n) {
			t.Errorf("%s offered", n)
		}
	}
	for _, n := range []string{"en0", "en1", "Wi-Fi", "Ethernet", "eth0", "wlan0"} {
		if virtualInterface(n) {
			t.Errorf("%s left out", n)
		}
	}
}

func portOf(a net.Addr) string {
	_, p, _ := net.SplitHostPort(a.String())
	return p
}
