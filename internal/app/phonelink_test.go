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
	"testing"
	"time"

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

// Phones pair with the hive's queen. The desktop window is a client of the
// hive and opens no phone link of its own.
func TestTheDesktopPairsNoPhone(t *testing.T) {
	app := companionTestApp(t)
	app.ctx = context.Background() // the desktop app
	t.Cleanup(app.stopPhoneLink)
	if _, err := app.PairPhone(); err == nil {
		t.Fatal("the desktop paired a phone")
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
