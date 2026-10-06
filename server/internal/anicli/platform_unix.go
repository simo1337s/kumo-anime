//go:build !windows

package anicli

import (
	"context"
	"os/exec"

	"github.com/simo1337s/animetest/server/internal/util"
)

// findScript resolves the configured ani-cli.
func findScript(path string) (string, bool) { return util.LookPath(path) }

// scriptCommand runs the ani-cli script.
func scriptCommand(ctx context.Context, script string, args ...string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, script, args...), nil
}

// shellPath is a path as the ani-cli script sees it.
func shellPath(p string) string { return p }
