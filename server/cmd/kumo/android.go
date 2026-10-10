//go:build android

package main

import (
	"os"
	"time"
)

// Go on Android always starts in UTC: the app gives the device's time zone.
func init() {
	if tz := os.Getenv("TZ"); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			time.Local = loc
		}
	}
}
