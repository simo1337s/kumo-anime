// Package share shares the library with other Kumo apps on the home network,
// and plays the libraries they share.
//
// Every Kumo with sharing on announces itself on the network (a small UDP
// multicast message every few seconds, see discovery.go) and listens for the
// others. Each has its own key pair (X25519), and its ID is a fingerprint of
// its public key. Two Kumos derive the same secret from their keys (ECDH): a
// guest signs each request to a host with it, which no other computer can
// do (bound to the request, its time and a nonce: it can't be sent again),
// and the host signs its answer the same way. A host gives its library only
// to the Kumos turned on in its settings.
//
// A shared file is named kumo://<host ID>/<path on the host>. The app's pages
// and player pass it around like the path of a local file, and the server
// forwards whatever it's asked about it to the host (forward.go).
package share

import (
	"bytes"
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
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/db"
)

// Identity is this Kumo's key pair.
type Identity struct {
	ID   string
	priv *ecdh.PrivateKey

	// The nonces of the requests checked lately (see verify).
	mu     sync.Mutex
	nonces map[string]time.Time
	pruned time.Time
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
	// When it was made (Unix seconds, by the host's clock as far as the
	// guest knows it), and a number never used again: no one can send it
	// again.
	headerTime  = "X-Kumo-Peer-Time"
	headerNonce = "X-Kumo-Peer-Nonce"
	// HeaderReply is in the host's answer: it proves the answer comes from
	// the host (made with the request's nonce, and its Date).
	HeaderReply = "X-Kumo-Peer-Reply"
)

const (
	// How far a request's time may be from the host's clock.
	clockSkew = 5 * time.Minute
	// How long mpv may play a shared file with the headers it was given (it
	// repeats the same request: a ticket).
	ticketFor = 12 * time.Hour
	// The largest request body a host reads to check it.
	maxSignedBody = 16 << 20
)

var (
	errOldPeer = errors.New("that Kumo is older than this one: update it")
	errClock   = errors.New("the clocks of the two devices are too far apart")
	errReplay  = errors.New("a request sent again")
)

// IsPeerRequest reports a request from another Kumo (which Verify checks).
func IsPeerRequest(r *http.Request) bool { return r.Header.Get(headerID) != "" }

// mac proves a request, or the answer to it, between a guest and a host:
// only those two can make it, and it changes with each of its parts.
func (id *Identity) mac(peerPub []byte, guest, host string, parts ...string) (string, error) {
	key, err := id.secret(peerPub, fmt.Sprintf("kumo-share-v2|guest=%s|host=%s", guest, host))
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		fmt.Fprintf(m, "%d:%s", len(p), p)
	}
	return hex.EncodeToString(m.Sum(nil)), nil
}

// requestParts is what a request's token proves: all of it but the headers
// that only ask how to send the answer (Range…).
func requestParts(r *http.Request, body string) []string {
	h := r.Header
	return []string{"request", r.Method, r.URL.RequestURI(), h.Get(headerTime), h.Get(headerNonce), h.Get(headerName), h.Get(headerPort), h.Get(headerUser), body}
}

// signature is a request's, as made or checked: what proves the answer.
type signature struct {
	id          *Identity
	peerPub     []byte
	guest, host string
	nonce       string
}

// reply is what proves the answer to the request comes from the host.
func (s *signature) reply(date string) (string, error) {
	return s.id.mac(s.peerPub, s.guest, s.host, "reply", s.nonce, date)
}

// sentBody is the SHA-256 of the body of a request to send, which stays to
// be sent.
func sentBody(r *http.Request) (string, error) {
	var b []byte
	switch {
	case r.Body == nil || r.Body == http.NoBody:
	case r.GetBody != nil:
		rc, err := r.GetBody()
		if err != nil {
			return "", err
		}
		b, err = io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return "", err
		}
	default:
		var err error
		if b, err = io.ReadAll(r.Body); err != nil {
			return "", err
		}
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// receivedBody is the SHA-256 of the body of a request received, which
// stays to be read.
func receivedBody(r *http.Request) (string, error) {
	var b []byte
	if r.Body != nil && r.Body != http.NoBody {
		var err error
		b, err = io.ReadAll(io.LimitReader(r.Body, maxSignedBody+1))
		r.Body.Close()
		if err != nil {
			return "", err
		}
		if len(b) > maxSignedBody {
			return "", errors.New("request too large")
		}
		r.Body = io.NopCloser(bytes.NewReader(b))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// sign signs a request to the host with that public key: it proves it comes
// from this Kumo and is this request, made at that time (by the host's
// clock, as far as this Kumo knows it), and only once. A ticket is a GET of
// a file mpv may repeat for a while (ticketFor), with no nonce.
func (id *Identity) sign(req *http.Request, host string, hostPub []byte, name string, port, user int, at time.Time, ticket bool) (*signature, error) {
	body, err := sentBody(req)
	if err != nil {
		return nil, err
	}
	nonce := ""
	if !ticket {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		nonce = hex.EncodeToString(b)
	}
	h := req.Header
	h.Set(headerID, id.ID)
	h.Set(headerKey, id.PublicKey())
	h.Set(headerName, url.QueryEscape(name))
	h.Set(headerPort, strconv.Itoa(port))
	h.Del(headerUser)
	if user > 0 {
		h.Set(headerUser, strconv.Itoa(user))
	}
	h.Set(headerTime, strconv.FormatInt(at.Unix(), 10))
	h.Set(headerNonce, nonce)
	tok, err := id.mac(hostPub, id.ID, host, requestParts(req, body)...)
	if err != nil {
		return nil, err
	}
	h.Set(headerToken, tok)
	return &signature{id: id, peerPub: hostPub, guest: id.ID, host: host, nonce: nonce}, nil
}

// caller is who sent a request, once checked.
type caller struct {
	id, name   string
	pub        []byte
	port, user int
}

// isTicket reports a request mpv may repeat: a GET of a shared file.
func isTicket(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/api/peer/local/file"
}

// verify checks that a request comes from the Kumo it says, as it was made,
// now and for the first time. The signature (to prove the answer) is there
// once the token is right, also when the request is refused for its time.
func (id *Identity) verify(r *http.Request, now time.Time) (*caller, *signature, error) {
	c := &caller{id: r.Header.Get(headerID)}
	pub, err := parsePublicKey(c.id, r.Header.Get(headerKey))
	if err != nil {
		return nil, nil, err
	}
	if r.Header.Get(headerTime) == "" {
		return nil, nil, errOldPeer
	}
	body, err := receivedBody(r)
	if err != nil {
		return nil, nil, err
	}
	want, err := id.mac(pub, c.id, id.ID, requestParts(r, body)...)
	if err != nil {
		return nil, nil, err
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(r.Header.Get(headerToken))) != 1 {
		return nil, nil, errors.New("wrong token")
	}
	nonce := r.Header.Get(headerNonce)
	sig := &signature{id: id, peerPub: pub, guest: c.id, host: id.ID, nonce: nonce}
	sec, err := strconv.ParseInt(r.Header.Get(headerTime), 10, 64)
	if err != nil {
		return nil, sig, errors.New("bad time")
	}
	at := time.Unix(sec, 0)
	switch {
	case nonce == "" && isTicket(r):
		if at.Before(now.Add(-ticketFor)) || at.After(now.Add(clockSkew)) {
			return nil, sig, errClock
		}
	case nonce == "" || len(nonce) > 64:
		return nil, sig, errors.New("no nonce")
	case at.Before(now.Add(-clockSkew)) || at.After(now.Add(clockSkew)):
		return nil, sig, errClock
	case !id.firstUse(nonce, at.Add(clockSkew), now):
		return nil, sig, errReplay
	}
	c.pub = pub
	c.name, _ = url.QueryUnescape(r.Header.Get(headerName))
	c.name = cleanName(c.name)
	c.port, _ = strconv.Atoi(r.Header.Get(headerPort))
	c.user, _ = strconv.Atoi(r.Header.Get(headerUser))
	return c, sig, nil
}

// firstUse notes a request's nonce until its time is over (until): it's
// the first time it's seen.
func (id *Identity) firstUse(nonce string, until, now time.Time) bool {
	id.mu.Lock()
	defer id.mu.Unlock()
	if id.nonces == nil {
		id.nonces = map[string]time.Time{}
	}
	if len(id.nonces) > 4096 && now.Sub(id.pruned) > 10*time.Second {
		for n, t := range id.nonces {
			if now.After(t) {
				delete(id.nonces, n)
			}
		}
		id.pruned = now
	}
	if _, seen := id.nonces[nonce]; seen {
		return false
	}
	id.nonces[nonce] = until
	return true
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
