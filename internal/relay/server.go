package relay

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	// helloWait is how long a new connection has to say what it is.
	helloWait = 10 * time.Second
	// acceptWait is how long a phone is held while its computer dials back.
	acceptWait = 10 * time.Second
	// linkIdle is how long a computer's link may stay silent: it pings every
	// pingEvery, so three missed pings and it is gone.
	linkIdle  = 90 * time.Second
	pingEvery = 30 * time.Second

	maxPendingPerLink = 16
	maxPending        = 1024
	maxLinks          = 10000
)

// Server is the relay. Domain is the name phones reach computers under:
// a phone asking for <id>.<Domain> is put through to the computer <id>.
type Server struct {
	Domain string
	Logf   func(format string, args ...any)

	mu      sync.Mutex
	links   map[string]*link
	pending map[string]*waiting
}

type link struct {
	id   string
	conn net.Conn
	wmu  sync.Mutex
	held int // phones waiting on this computer; guarded by Server.mu
}

func (l *link) send(line string) error {
	l.wmu.Lock()
	defer l.wmu.Unlock()
	_ = l.conn.SetWriteDeadline(time.Now().Add(helloWait))
	_, err := io.WriteString(l.conn, line+"\n")
	return err
}

// waiting is a phone whose ClientHello has been read, held until its computer
// dials back for it.
type waiting struct {
	id    string
	conn  net.Conn
	r     *bufio.Reader
	head  []byte
	timer *time.Timer
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
		return
	}
	slog.Info(fmt.Sprintf(format, args...), "module", "relay")
}

// Serve accepts connections until ln is closed.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.links == nil {
		s.links = map[string]*link{}
		s.pending = map[string]*waiting{}
	}
	s.mu.Unlock()
	for {
		c, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		go s.handle(c)
	}
}

// Linked reports whether a computer is linked under id.
func (s *Server) Linked(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.links[id]
	return ok
}

func (s *Server) handle(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(helloWait))
	r := bufio.NewReaderSize(c, 4096)
	first, err := r.Peek(1)
	if err != nil {
		c.Close()
		return
	}
	if first[0] == 0x16 {
		s.phone(c, r)
		return
	}
	head, err := r.Peek(len(magic))
	if err != nil || string(head) != magic {
		c.Close()
		return
	}
	line, err := readLine(r)
	if err != nil {
		c.Close()
		return
	}
	f := strings.Fields(strings.TrimPrefix(line, magic))
	switch {
	case len(f) == 2 && f[0] == "link" && validID(f[1]):
		s.link(c, r, f[1])
	case len(f) == 3 && f[0] == "accept" && validID(f[1]):
		s.accept(c, r, f[1], f[2])
	default:
		c.Close()
	}
}

// phone reads the name a phone asked for and holds it for that computer.
func (s *Server) phone(c net.Conn, r *bufio.Reader) {
	name, head, err := readServerName(r)
	if err != nil {
		c.Close()
		return
	}
	id, ok := strings.CutSuffix(strings.ToLower(name), "."+s.Domain)
	if !ok || !validID(id) {
		c.Close()
		return
	}
	sid := randomHex(16)
	w := &waiting{id: id, conn: c, r: r, head: head}

	s.mu.Lock()
	l := s.links[id]
	if l == nil || l.held >= maxPendingPerLink || len(s.pending) >= maxPending {
		s.mu.Unlock()
		c.Close()
		return
	}
	l.held++
	s.pending[sid] = w
	w.timer = time.AfterFunc(acceptWait, func() {
		if s.take(sid, id) != nil {
			c.Close()
		}
	})
	s.mu.Unlock()

	if err := l.send("open " + sid); err != nil {
		if s.take(sid, id) != nil {
			c.Close()
		}
		l.conn.Close()
	}
}

// take removes a held phone, if it is still held and is held for id.
func (s *Server) take(sid, id string) *waiting {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.pending[sid]
	if w == nil || w.id != id {
		return nil
	}
	delete(s.pending, sid)
	if l := s.links[id]; l != nil && l.held > 0 {
		l.held--
	}
	w.timer.Stop()
	return w
}

// accept is a computer's connection back for one phone: from here on the
// relay only copies.
func (s *Server) accept(c net.Conn, r *bufio.Reader, id, sid string) {
	w := s.take(sid, id)
	if w == nil {
		c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	_ = w.conn.SetReadDeadline(time.Time{})
	if _, err := c.Write(w.head); err != nil {
		c.Close()
		w.conn.Close()
		return
	}
	pipe(w.conn, w.r, c, r)
}

func pipe(a net.Conn, ar io.Reader, b net.Conn, br io.Reader) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(b, ar); done <- struct{}{} }()
	go func() { _, _ = io.Copy(a, br); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
	<-done
}

// link checks that the computer holds the key id is named after, then keeps
// its link until it goes quiet or is replaced.
func (s *Server) link(c net.Conn, r *bufio.Reader, id string) {
	nonce := randomHex(32)
	l := &link{id: id, conn: c}
	if err := l.send("challenge " + nonce); err != nil {
		c.Close()
		return
	}
	line, err := readLine(r)
	if err != nil || !proves(line, id, nonce) {
		c.Close()
		return
	}

	s.mu.Lock()
	old := s.links[id]
	if old == nil && len(s.links) >= maxLinks {
		s.mu.Unlock()
		c.Close()
		return
	}
	s.links[id] = l
	s.mu.Unlock()
	if old != nil {
		old.conn.Close()
	}
	if err := l.send("ok"); err != nil {
		s.unlink(l)
		return
	}
	s.logf("relay: %s linked from %s", id, c.RemoteAddr())
	for {
		_ = c.SetReadDeadline(time.Now().Add(linkIdle))
		line, err := readLine(r)
		if err != nil {
			break
		}
		if line == "ping" {
			if l.send("pong") != nil {
				break
			}
		}
	}
	s.unlink(l)
}

func (s *Server) unlink(l *link) {
	s.mu.Lock()
	gone := s.links[l.id] == l
	if gone {
		delete(s.links, l.id)
	}
	s.mu.Unlock()
	if gone {
		s.logf("relay: %s unlinked", l.id)
	}
	l.conn.Close()
}

// proves checks "proof <public key> <signature>": the key hashes to id and
// signed this nonce.
func proves(line, id, nonce string) bool {
	f := strings.Fields(line)
	if len(f) != 3 || f[0] != "proof" {
		return false
	}
	der, err := base64.RawStdEncoding.DecodeString(f[1])
	if err != nil || idFromDER(der) != id {
		return false
	}
	sig, err := base64.RawStdEncoding.DecodeString(f[2])
	if err != nil {
		return false
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return false
	}
	pub, ok := k.(*ecdsa.PublicKey)
	return ok && ecdsa.VerifyASN1(pub, challengeDigest(nonce), sig)
}

func readLine(r *bufio.Reader) (string, error) {
	var b []byte
	for {
		chunk, more, err := r.ReadLine()
		if err != nil {
			return "", err
		}
		b = append(b, chunk...)
		if len(b) > 1024 {
			return "", errors.New("line too long")
		}
		if !more {
			return string(b), nil
		}
	}
}

var errSeen = errors.New("seen")

// readServerName reads a TLS ClientHello far enough to know the server name,
// and returns every byte it read so they can be passed on unchanged.
func readServerName(r io.Reader) (string, []byte, error) {
	rc := &recordingConn{r: r}
	var name string
	err := tls.Server(rc, &tls.Config{
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			name = h.ServerName
			return nil, errSeen
		},
	}).Handshake()
	if name == "" {
		if err == nil {
			err = errors.New("no server name")
		}
		return "", nil, fmt.Errorf("client hello: %w", err)
	}
	return name, rc.buf.Bytes(), nil
}

// recordingConn is what crypto/tls reads the ClientHello from: it keeps what
// was read and writes nothing back.
type recordingConn struct {
	r   io.Reader
	buf bytes.Buffer
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.buf.Write(p[:n])
	return n, err
}
func (c *recordingConn) Write(p []byte) (int, error)      { return 0, io.ErrClosedPipe }
func (c *recordingConn) Close() error                     { return nil }
func (c *recordingConn) LocalAddr() net.Addr              { return nil }
func (c *recordingConn) RemoteAddr() net.Addr             { return nil }
func (c *recordingConn) SetDeadline(time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(time.Time) error { return nil }

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
