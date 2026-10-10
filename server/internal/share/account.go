package share

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"slices"
)

// A host can share its AniList login with a Kumo it shares its library with
// (a TV with no keyboard to log in with, say): that Kumo then uses the same
// AniList account, its list and its progress.
//
// The host's hello tells a tag of its login (changing with it); the login
// itself is asked for at /api/peer/anilist and comes sealed for the guest
// (only it can read it). A guest that isn't logged in uses it by itself; one
// logged into its own account keeps it, and Settings offers the host's. When
// the host stops sharing it, or logs out, the guest is logged out too. One
// logged out by hand while using it doesn't use it again by itself.

// SealedAccount is a host's AniList login, sealed for one guest.
type SealedAccount struct {
	Token string `json:"token"`
}

var errNoAccount = errors.New("this Kumo isn't logged into AniList")

func (s *Service) accountToken() string {
	if s.AccountToken == nil {
		return ""
	}
	return s.AccountToken()
}

// accountTag stands for a login in hellos: it changes when the login does,
// and tells nothing of it.
func accountTag(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("kumo-share-v1|account|" + token))
	return hex.EncodeToString(sum[:8])
}

// SealAccount is this Kumo's AniList login for a guest it shares it with
// (checked by the caller, see Caller.Account).
func (s *Service) SealAccount(c Caller) (SealedAccount, error) {
	if !c.Allowed || !c.Account {
		return SealedAccount{}, errors.New("this Kumo doesn't share its AniList account with you")
	}
	token := s.accountToken()
	if token == "" {
		return SealedAccount{}, errNoAccount
	}
	s.mu.Lock()
	p := s.peers[c.ID]
	var pub []byte
	if p != nil {
		pub = p.pub
	}
	s.mu.Unlock()
	if pub == nil {
		return SealedAccount{}, errors.New("unknown Kumo")
	}
	sealed, err := s.id.sealFor(pub, c.ID, s.id.ID, []byte(token))
	if err != nil {
		return SealedAccount{}, err
	}
	return SealedAccount{Token: sealed}, nil
}

// The guest's side.

const accountKey = "share:account"

// accountState is which host's AniList login this Kumo uses ("" its own, or
// none), and the hosts whose it doesn't want (logged out of by hand).
type accountState struct {
	Host     string   `json:"host,omitempty"`
	Tag      string   `json:"tag,omitempty"`
	Declined []string `json:"declined,omitempty"`
}

func (s *Service) loadAccount() accountState {
	var st accountState
	_, _ = s.db.GetKV(accountKey, &st)
	return st
}

func (s *Service) saveAccount(st accountState) {
	if err := s.db.SetKV(accountKey, st); err != nil {
		log.Printf("sharing: %v", err)
	}
}

// AccountHost is the host whose AniList login this Kumo uses ("": none).
func (s *Service) AccountHost() string {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	return s.loadAccount().Host
}

// followAccount follows what a host says of its AniList login (tag, "" when
// it doesn't share it).
func (s *Service) followAccount(ctx context.Context, p *peer, tag string) {
	if s.UseAccount == nil || s.DropAccount == nil {
		return
	}
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	st := s.loadAccount()
	using := st.Host == p.id
	switch {
	case tag == "" && using:
		s.DropAccount()
		st.Host, st.Tag = "", ""
		s.saveAccount(st)
		s.hub.Info(s.nameOf(p) + " stopped sharing its AniList account: logged out of it here.")
	case tag != "" && using && tag != st.Tag:
		// Logged in again there, maybe to another account: the same here.
		if err := s.takeAccount(ctx, p, tag, &st); err != nil {
			log.Printf("sharing: %s's AniList account: %v", s.nameOf(p), err)
		}
	case tag != "" && !using && s.accountToken() == "" && !slices.Contains(st.Declined, p.id):
		if err := s.takeAccount(ctx, p, tag, &st); err != nil {
			log.Printf("sharing: %s's AniList account: %v", s.nameOf(p), err)
			return
		}
		s.hub.Success("Logged into AniList with " + s.nameOf(p) + "'s account.")
	}
}

// takeAccount logs into a host's AniList account. Must be called with
// accountMu held.
func (s *Service) takeAccount(ctx context.Context, p *peer, tag string, st *accountState) error {
	var sa SealedAccount
	if err := s.getJSON(ctx, p, "/api/peer/anilist", &sa); err != nil {
		return err
	}
	token, err := s.id.openFrom(p.pub, s.id.ID, p.id, sa.Token)
	if err != nil {
		return fmt.Errorf("can't read it: %w", err)
	}
	if err := s.UseAccount(ctx, string(token)); err != nil {
		return err
	}
	st.Host, st.Tag = p.id, tag
	st.Declined = slices.DeleteFunc(st.Declined, func(id string) bool { return id == p.id })
	s.saveAccount(*st)
	s.hub.Publish("sharing-updated", nil)
	return nil
}

// UseHostAccount logs into the AniList account a host shares, instead of
// this Kumo's own (Settings).
func (s *Service) UseHostAccount(ctx context.Context, id string) error {
	if s.UseAccount == nil {
		return errors.New("not available")
	}
	s.mu.Lock()
	p := s.peers[id]
	var tag string
	if p != nil && p.shares {
		tag = p.hostAccount
	}
	s.mu.Unlock()
	if tag == "" {
		return errors.New("that Kumo doesn't share its AniList account with this one")
	}
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	st := s.loadAccount()
	return s.takeAccount(ctx, p, tag, &st)
}

// LoggedOut notes a logout by hand: a host's account this Kumo was using
// isn't used again by itself.
func (s *Service) LoggedOut() {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	st := s.loadAccount()
	if st.Host == "" {
		return
	}
	if !slices.Contains(st.Declined, st.Host) {
		st.Declined = append(st.Declined, st.Host)
	}
	st.Host, st.Tag = "", ""
	s.saveAccount(st)
	s.hub.Publish("sharing-updated", nil)
}

// LoggedIn notes a login by hand: this Kumo's own account from now on.
func (s *Service) LoggedIn() {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	st := s.loadAccount()
	if st.Host == "" {
		return
	}
	st.Host, st.Tag = "", ""
	s.saveAccount(st)
	s.hub.Publish("sharing-updated", nil)
}
