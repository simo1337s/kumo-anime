package util

import "testing"

func TestWithToolDirs(t *testing.T) {
	brew := func(d string) bool {
		return d == "/opt/homebrew/bin" || d == "/opt/homebrew/sbin" || d == "/usr/local/bin"
	}
	for _, tc := range []struct{ path, want string }{
		// Opened from the Finder.
		{"/usr/bin:/bin:/usr/sbin:/sbin", "/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"},
		// From a Terminal, which has them already.
		{"/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin", "/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin"},
		{"/usr/local/bin:/usr/bin", "/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin"},
		{"", "/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin"},
	} {
		if got := withToolDirs(tc.path, toolDirs, brew); got != tc.want {
			t.Errorf("withToolDirs(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
