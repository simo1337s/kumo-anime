package util

import (
	"context"
	"errors"
	"net"
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
