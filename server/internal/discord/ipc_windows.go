//go:build windows

package discord

import "fmt"

// socketPaths lists the named pipes Discord may listen on.
func socketPaths() []string {
	var out []string
	for i := 0; i < 10; i++ {
		out = append(out, fmt.Sprintf(`\\.\pipe\discord-ipc-%d`, i))
	}
	return out
}
