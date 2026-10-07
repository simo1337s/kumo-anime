// Package lifecycle lets Kumo's services end the process the way a signal
// does: cmd/kumo shuts everything down first (the HTTP server, the app,
// the files it wrote), then exits as asked or starts Kumo again. The
// updater uses it to restart into a new version.
package lifecycle

// Exit codes that tell the desktop app (desktop/main.js) what happened.
const (
	// CodeRestart asks the desktop app to start again: the whole app, so
	// that a new version of its window and of the server is loaded.
	CodeRestart = 75
	// CodeInstalling says an installer is replacing Kumo and starts it
	// again when it's done: the desktop app quits without a word.
	CodeInstalling = 76
)

// Exit is how Kumo ends once it has shut down.
type Exit struct {
	// Restart starts Kumo again: the desktop app does it when it started
	// the server, otherwise the server runs itself again where the system
	// allows it (exit code CodeRestart where it doesn't).
	Restart bool
	// Code is the exit code when not restarting.
	Code int
	// Before runs once Kumo has shut down, right before it exits. When it
	// fails, Kumo restarts instead, so it doesn't just disappear.
	Before func() error
}

// Exits carries a request to end Kumo from the service that makes it to
// cmd/kumo. Only the first request counts: the shutdown has begun.
type Exits struct {
	ch chan Exit
}

func NewExits() *Exits {
	return &Exits{ch: make(chan Exit, 1)}
}

// Request asks Kumo to shut down and end as e says. It reports false when
// an earlier request is already pending.
func (x *Exits) Request(e Exit) bool {
	select {
	case x.ch <- e:
		return true
	default:
		return false
	}
}

// C delivers the request to cmd/kumo.
func (x *Exits) C() <-chan Exit { return x.ch }
