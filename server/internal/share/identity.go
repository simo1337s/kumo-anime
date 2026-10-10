// Package share shares the library with other Kumo apps on the home network,
// and plays the libraries they share.
//
// Every Kumo with sharing on announces itself on the network (a small UDP
// multicast message every few seconds, see discovery.go) and listens for the
// others. Each has its own key pair (X25519), and its ID is a fingerprint of
// its public key. Two Kumos derive the same secret from their keys (ECDH): a
// guest makes from it the token that proves who it is to a host, which no
// other computer can make. A host gives its library only to the Kumos turned
// on in its settings.
//
// A shared file is named kumo://<host ID>/<path on the host>. The app's pages
// and player pass it around like the path of a local file, and the server
// forwards whatever it's asked about it to the host (forward.go).
package share

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/simo1337s/animetest/server/internal/db"
)

// Identity is this Kumo's key pair.
type Identity struct {
	ID   string
	priv *ecdh.PrivateKey
}

const identityKey = "share:identity"

// LoadIdentity returns this Kumo's identity, made the first time.
func LoadIdentity(d *db.DB) (*Identity, error) {
	var saved struct {
		Private string `json:"private"`
	}
	if ok, err := d.GetKV(identityKey, &saved); err != nil {
		return nil, err
	} else if ok {
		raw, err := base64.StdEncoding.DecodeString(saved.Private)
		if err == nil {
			if priv, err := ecdh.X25519().NewPrivateKey(raw); err == nil {
				return newIdentity(priv), nil
			}
		}
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	saved.Private = base64.StdEncoding.EncodeToString(priv.Bytes())
	if err := d.SetKV(identityKey, saved); err != nil {
		return nil, err
	}
	return newIdentity(priv), nil
}

func newIdentity(priv *ecdh.PrivateKey) *Identity {
	return &Identity{ID: fingerprint(priv.PublicKey().Bytes()), priv: priv}
}

// PublicKey is the public key, as sent to the others.
func (id *Identity) PublicKey() string {
	return base64.StdEncoding.EncodeToString(id.priv.PublicKey().Bytes())
}

// fingerprint is the ID of the Kumo with this public key: 16 letters and
// digits.
func fingerprint(pub []byte) string {
	sum := sha256.Sum256(pub)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:10]))
}

// parsePublicKey checks a public key received with the ID it should have.
func parsePublicKey(id, b64 string) ([]byte, error) {
	pub, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(pub) != 32 {
		return nil, errors.New("bad public key")
	}
	if fingerprint(pub) != id {
		return nil, errors.New("the public key doesn't match the ID")
	}
	return pub, nil
}

// secret is what this Kumo and the one with that public key have in common,
// for a purpose: only those two can make it.
func (id *Identity) secret(peerPub []byte, purpose string) ([]byte, error) {
	pk, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, err
	}
	shared, err := id.priv.ECDH(pk)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, shared)
	mac.Write([]byte(purpose))
	return mac.Sum(nil), nil
}

// token is what the guest sends to the host to prove who it is: only those
// two can make it.
func (id *Identity) token(peerPub []byte, guest, host string) (string, error) {
	sum, err := id.secret(peerPub, fmt.Sprintf("kumo-share-v1|guest=%s|host=%s", guest, host))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum), nil
}

// sealFor encrypts something for the guest with that public key (the host's
// AniList login): only it can read it, though it crosses the network as is.
func (id *Identity) sealFor(guestPub []byte, guest, host string, plain []byte) (string, error) {
	aead, err := id.cipher(guestPub, guest, host)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, nil)), nil
}

// openFrom decrypts what the host with that public key sealed for this Kumo.
func (id *Identity) openFrom(hostPub []byte, guest, host, sealed string) ([]byte, error) {
	aead, err := id.cipher(hostPub, guest, host)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < aead.NonceSize() {
		return nil, errors.New("bad sealed data")
	}
	return aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
}

func (id *Identity) cipher(peerPub []byte, guest, host string) (cipher.AEAD, error) {
	key, err := id.secret(peerPub, fmt.Sprintf("kumo-share-v1|seal|guest=%s|host=%s", guest, host))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// The headers a guest's requests carry.
const (
	headerID    = "X-Kumo-Peer-Id"
	headerKey   = "X-Kumo-Peer-Key"
	headerName  = "X-Kumo-Peer-Name"
	headerPort  = "X-Kumo-Peer-Port"
	headerToken = "X-Kumo-Peer-Token"
	// The AniList account it's logged into (0: none).
	headerUser = "X-Kumo-Peer-User"
)

// IsPeerRequest reports a request from another Kumo (which Verify checks).
func IsPeerRequest(r *http.Request) bool { return r.Header.Get(headerID) != "" }

// sign adds to a request to the host (with that public key) what proves it
// comes from this Kumo.
func (id *Identity) sign(h http.Header, host string, hostPub []byte, name string, port, user int) error {
	tok, err := id.token(hostPub, id.ID, host)
	if err != nil {
		return err
	}
	h.Set(headerID, id.ID)
	h.Set(headerKey, id.PublicKey())
	h.Set(headerName, url.QueryEscape(name))
	h.Set(headerPort, strconv.Itoa(port))
	h.Set(headerToken, tok)
	if user > 0 {
		h.Set(headerUser, strconv.Itoa(user))
	}
	return nil
}

// caller is who sent a request, once checked.
type caller struct {
	id, name   string
	pub        []byte
	port, user int
}

// verify checks that a request comes from the Kumo it says.
func (id *Identity) verify(r *http.Request) (*caller, error) {
	c := &caller{id: r.Header.Get(headerID)}
	pub, err := parsePublicKey(c.id, r.Header.Get(headerKey))
	if err != nil {
		return nil, err
	}
	want, err := id.token(pub, c.id, id.ID)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(r.Header.Get(headerToken))) != 1 {
		return nil, errors.New("wrong token")
	}
	c.pub = pub
	c.name, _ = url.QueryUnescape(r.Header.Get(headerName))
	c.name = cleanName(c.name)
	c.port, _ = strconv.Atoi(r.Header.Get(headerPort))
	c.user, _ = strconv.Atoi(r.Header.Get(headerUser))
	return c, nil
}

// cleanName keeps a peer's name printable and short.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len([]rune(s)) > 60 {
		s = string([]rune(s)[:60])
	}
	return s
}
