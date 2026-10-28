package relay

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func selfSigned(t *testing.T, key *ecdsa.PrivateKey) tls.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "SuperLeo"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// chanListener is a computer's listener fed by its relay link.
type chanListener struct {
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *chanListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *chanListener) Addr() net.Addr { return &net.TCPAddr{} }

func startRelay(t *testing.T) (string, *Server) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Domain: "relay.test", Logf: func(string, ...any) {}}
	go s.Serve(ln)
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), s
}

func waitLinked(t *testing.T, s *Server, id string) {
	t.Helper()
	for range 200 {
		if s.Linked(id) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never linked", id)
}

// A phone that knows only the relay and the computer's fingerprint gets the
// computer's own answer, over TLS the relay cannot open.
func TestAPhoneReachesItsComputerThroughTheRelay(t *testing.T) {
	addr, s := startRelay(t)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert := selfSigned(t, key)
	fp := sha256.Sum256(cert.Certificate[0])

	ln := &chanListener{ch: make(chan net.Conn), done: make(chan struct{})}
	defer ln.Close()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello from the computer")
	})}
	go srv.Serve(tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2", "http/1.1"}}))
	defer srv.Close()

	ctx := t.Context()
	c := &Client{Addr: addr, Key: key, Deliver: func(conn net.Conn) { ln.ch <- conn }}
	go c.Run(ctx)
	id, _ := ID(&key.PublicKey)
	waitLinked(t, s, id)

	phone := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		},
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if got := sha256.Sum256(raw[0]); !bytes.Equal(got[:], fp[:]) {
					return errors.New("not the paired computer")
				}
				return nil
			},
		},
	}, Timeout: 5 * time.Second}
	for i := range 3 {
		resp, err := phone.Get("https://" + id + ".relay.test/")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(b) != "hello from the computer" {
			t.Fatalf("got %q", b)
		}
	}
}

// A computer cannot take a name whose key it does not hold.
func TestALinkNeedsTheKeyItsNameComesFrom(t *testing.T) {
	addr, s := startRelay(t)
	mine, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	theirs, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	victim, _ := ID(&theirs.PublicKey)

	c := &Client{Addr: addr, Key: mine}
	c.id = victim // claims the other key's name
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.once(ctx); err == nil {
		t.Fatal("link with someone else's name was kept")
	}
	if s.Linked(victim) {
		t.Fatal("relay linked a name to the wrong key")
	}
}

// A phone asking for a computer that is not linked is hung up on, and one
// asking for something that is not under the relay's domain too.
func TestUnknownNamesAreDropped(t *testing.T) {
	addr, _ := startRelay(t)
	for _, name := range []string{"aaaaaaaaaaaaaaaa.relay.test", "example.com"} {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: name, InsecureSkipVerify: true})
		if err == nil {
			conn.Close()
			t.Fatalf("%s: handshake finished", name)
		}
	}
}

func TestIDsAreDNSLabels(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id, err := ID(&key.PublicKey)
	if err != nil || !validID(id) || strings.ToLower(id) != id {
		t.Fatalf("id %q err %v", id, err)
	}
}
