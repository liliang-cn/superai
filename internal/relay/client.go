package relay

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client is a computer's side: it keeps the link to the relay up and hands
// each phone that comes through it to Deliver, as a connection that still has
// the phone's TLS handshake to run.
type Client struct {
	// Addr is the relay, host:port.
	Addr    string
	Key     *ecdsa.PrivateKey
	Deliver func(net.Conn)

	id     string
	linked atomic.Bool
}

// Linked reports whether the link is up right now.
func (c *Client) Linked() bool { return c.linked.Load() }

// Run keeps the link up until ctx ends, dialling again after each loss.
func (c *Client) Run(ctx context.Context) error {
	id, err := ID(&c.Key.PublicKey)
	if err != nil {
		return err
	}
	c.id = id
	wait := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		_ = c.once(ctx)
		c.linked.Store(false)
		if time.Since(start) > time.Minute {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		wait = min(wait*2, time.Minute)
	}
	return ctx.Err()
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: helloWait, KeepAlive: pingEvery}
	return d.DialContext(ctx, "tcp", c.Addr)
}

func (c *Client) once(ctx context.Context) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()

	var wmu sync.Mutex
	send := func(line string) error {
		wmu.Lock()
		defer wmu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(helloWait))
		_, err := io.WriteString(conn, line+"\n")
		return err
	}
	r := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(helloWait))
	if err := send(magic + "link " + c.id); err != nil {
		return err
	}
	line, err := readLine(r)
	if err != nil {
		return err
	}
	nonce, ok := strings.CutPrefix(line, "challenge ")
	if !ok {
		return fmt.Errorf("relay said %q", line)
	}
	proof, err := c.prove(nonce)
	if err != nil {
		return err
	}
	if err := send(proof); err != nil {
		return err
	}
	if line, err = readLine(r); err != nil {
		return err
	}
	if line != "ok" {
		return fmt.Errorf("relay said %q", line)
	}
	c.linked.Store(true)

	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if send("ping") != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(linkIdle))
		line, err := readLine(r)
		if err != nil {
			return err
		}
		if sid, ok := strings.CutPrefix(line, "open "); ok {
			go c.accept(ctx, sid)
		}
	}
}

// accept dials back for one phone and hands the connection over.
func (c *Client) accept(ctx context.Context, sid string) {
	conn, err := c.dial(ctx)
	if err != nil {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(helloWait))
	if _, err := io.WriteString(conn, magic+"accept "+c.id+" "+sid+"\n"); err != nil {
		conn.Close()
		return
	}
	_ = conn.SetWriteDeadline(time.Time{})
	c.Deliver(conn)
}

func (c *Client) prove(nonce string) (string, error) {
	if nonce == "" {
		return "", errors.New("empty challenge")
	}
	der, err := x509.MarshalPKIXPublicKey(&c.Key.PublicKey)
	if err != nil {
		return "", err
	}
	sig, err := ecdsa.SignASN1(rand.Reader, c.Key, challengeDigest(nonce))
	if err != nil {
		return "", err
	}
	return "proof " + base64.RawStdEncoding.EncodeToString(der) + " " + base64.RawStdEncoding.EncodeToString(sig), nil
}
