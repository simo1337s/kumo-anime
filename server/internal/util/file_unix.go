//go:build !windows

package util

import "os"

// OpenShared opens a file for reading while another program may replace or
// delete it.
func OpenShared(name string) (*os.File, error) { return os.Open(name) }

// ReadFileShared reads a file another program may replace or delete
// meanwhile.
func ReadFileShared(name string) ([]byte, error) { return os.ReadFile(name) }
