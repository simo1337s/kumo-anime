package util

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// ErrPrivateAddress is returned when something tries to reach the LAN.
var ErrPrivateAddress = errors.New("connections to local/private network addresses are not allowed")

// IsPrivateIP reports loopback, private, link-local, CGNAT and similar ranges.
func IsPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return true
	}
	return false
}

// proxyHosts are the configured HTTP proxies (allowed even when local).
func proxyHosts() map[string]bool {
	out := map[string]bool{}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		v := os.Getenv(k)
		if v == "" {
			continue
		}
		if !strings.Contains(v, "://") {
			v = "http://" + v
		}
		if u, err := url.Parse(v); err == nil && u.Host != "" {
			host := u.Host
			if u.Port() == "" {
				host = net.JoinHostPort(u.Hostname(), "80")
			}
			out[strings.ToLower(host)] = true
		}
	}
	return out
}

// PublicDialContext dials only public addresses (the configured HTTP proxy
// excepted), checked after DNS resolution so rebinding tricks don't work.
func PublicDialContext(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	proxies := proxyHosts()
	plain := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	guarded := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip != nil && IsPrivateIP(ip) {
				return ErrPrivateAddress
			}
			return nil
		},
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if proxies[strings.ToLower(addr)] {
			return plain.DialContext(ctx, network, addr)
		}
		return guarded.DialContext(ctx, network, addr)
	}
}

// PublicProxy is http.ProxyFromEnvironment for the clients that may reach
// only public addresses: through a proxy, the proxy connects to the host, not
// this computer, so PublicDialContext never sees it; the host is checked
// here instead (each request, redirects too).
func PublicProxy(req *http.Request) (*url.URL, error) {
	p, err := http.ProxyFromEnvironment(req)
	if err != nil || p == nil {
		return p, err
	}
	if err := CheckPublicHost(req.Context(), req.URL.Hostname()); err != nil {
		return nil, err
	}
	// A request to a proxy names its Host as where to go, when it has one.
	if req.Host != "" {
		host := req.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if err := CheckPublicHost(req.Context(), host); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// CheckPublicHost fails for a host that is, or resolves to, a local or
// private address. A name this computer can't resolve passes: the proxy
// resolves it.
func CheckPublicHost(ctx context.Context, host string) error {
	host = strings.Trim(strings.ToLower(host), "[]")
	if ip := net.ParseIP(host); ip != nil {
		if IsPrivateIP(ip) {
			return ErrPrivateAddress
		}
		return nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ErrPrivateAddress
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	for _, ip := range ips {
		if IsPrivateIP(ip.IP) {
			return ErrPrivateAddress
		}
	}
	return nil
}
