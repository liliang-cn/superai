// Package relay lets a phone reach a SuperLeo on a computer it shares no
// network with, without the relay being able to read a byte of it.
//
// The computer keeps one outbound link open to the relay and proves, by
// signing a challenge, that it holds the key its name is derived from. A phone
// opens TLS to https://<name>.<relay domain>:<port>; the relay reads only the
// name in the ClientHello, asks the computer for a fresh connection, and from
// then on copies bytes. The TLS session is between the phone and the computer:
// the certificate is the computer's own, self-signed, and the phone pins its
// fingerprint from the pairing QR code.
//
// Everything is plain TCP on one port. TLS from a phone begins with a
// handshake record (0x16); a computer's lines begin with "SAIR1 ".
package relay

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"strings"
)

// DefaultHost is the relay a computer uses when nothing else is set.
const DefaultHost = "relay.superleo.app"

// DefaultPort is where the relay listens for phones and computers alike.
const DefaultPort = "43443"

const magic = "SAIR1 "

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// ID is the relay name of the key: 16 base32 characters of the hash of its
// public key, so whoever answers to a name can be asked to prove it.
func ID(pub *ecdsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	return idFromDER(der), nil
}

func idFromDER(der []byte) string {
	sum := sha256.Sum256(der)
	return strings.ToLower(idEncoding.EncodeToString(sum[:10]))
}

// challengeDigest is what the computer signs: the nonce, tied to this protocol
// so a signature cannot be lifted from anywhere else.
func challengeDigest(nonce string) []byte {
	sum := sha256.Sum256([]byte("superai-relay\n" + nonce))
	return sum[:]
}

func validID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '2' && r <= '7') {
			return false
		}
	}
	return true
}
