package extensions

import (
	"net/url"
	"testing"
)

// What a plugin may reach: its own domains, and only read from the ones
// every plugin may (where a POST could leave the user's data); paths as the
// server reads them.
func TestPluginFetchRules(t *testing.T) {
	f := NewPluginFetcher([]string{"api.example.com", "site.example/api/"}, builtinPluginDomains)
	for _, c := range []struct {
		url, method string
		ok          bool
	}{
		{"https://api.example.com/x", "POST", true},
		{"https://site.example/api/list", "GET", true},
		{"https://site.example/api/../admin/secret", "GET", false},
		{"https://site.example/admin", "GET", false},
		{"https://graphql.anilist.co/", "GET", true},
		{"https://storage.googleapis.com/bucket/o?uploadType=media", "POST", false},
		{"https://api.github.com/gists", "POST", false},
		{"https://evil.example.com/", "GET", false},
		{"http://127.0.0.1:43211/api/settings", "GET", false},
		{"http://localhost:43211/", "GET", false},
	} {
		u, _ := url.Parse(c.url)
		if err := f.allowed(u, c.method); (err == nil) != c.ok {
			t.Errorf("%s %s: %v", c.method, c.url, err)
		}
	}
	// Without network access asked for: only reading the common ones.
	none := NewPluginFetcher(nil, builtinPluginDomains)
	if u, _ := url.Parse("https://api.example.com/x"); none.allowed(u, "GET") == nil {
		t.Error("a plugin without network access reached a host")
	}
	// Each extension has its own client, and so its own cookies.
	if a, b := NewFetcher(), NewFetcher(); a.follow == b.follow || a.follow.GetClient().Jar == b.follow.GetClient().Jar {
		t.Error("two extensions share cookies")
	}
}
