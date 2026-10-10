package util

import (
	"context"
	"net/http"
	"testing"
)

// Through a proxy, the host a request goes to is checked here (the proxy
// connects to it, not this computer): never this one or the LAN.
func TestPublicProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:3128")
	t.Setenv("NO_PROXY", "")
	for host, ok := range map[string]bool{"127.0.0.1:43211": false, "localhost": false, "kumo.localhost": false, "192.168.1.1": false, "[::1]": false, "169.254.169.254": false, "203.0.113.5": true} {
		req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://"+host+"/", nil)
		if err := CheckPublicHost(req.Context(), req.URL.Hostname()); (err == nil) != ok {
			t.Errorf("%s: %v", host, err)
		}
	}
	// A Host other than the URL's is where a proxy goes: checked too.
	req, _ := http.NewRequest("GET", "http://203.0.113.5/", nil)
	req.Host = "127.0.0.1:43211"
	if _, err := PublicProxy(req); err == nil {
		t.Error("a request to 127.0.0.1 through the proxy, by its Host")
	}
}
