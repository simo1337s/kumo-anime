package winsetup

import (
	"bytes"
	"runtime"
	"testing"
)

// Windows PowerShell reads a script without a byte order mark in the
// computer's code page: anything but ASCII comes out garbled.
func TestScriptIsASCII(t *testing.T) {
	for i, b := range script {
		if b >= 0x80 {
			line := bytes.Count(script[:i], []byte("\n")) + 1
			t.Fatalf("install-tools.ps1 line %d has a non-ASCII byte", line)
		}
	}
	for _, want := range []string{"scoop install", "bucket add extras", "ani-cli", "gh auth login"} {
		if !bytes.Contains(script, []byte(want)) {
			t.Errorf("install-tools.ps1 lacks %q", want)
		}
	}
}

func TestStartIsForWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("it would open a PowerShell window")
	}
	if Start() == nil {
		t.Error("Start worked outside Windows")
	}
	if Running() {
		t.Error("running outside Windows")
	}
}
