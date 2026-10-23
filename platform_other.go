//go:build !windows && !darwin

package main

import (
	"errors"
	"os"
	"path/filepath"
)

// There is no official Claude desktop app for this platform. These stubs keep
// the project building so the shared code can be tested here.

var errUnsupported = errors.New("the Claude desktop app is not supported on this platform")

func claudeDesktop() desktop {
	home, _ := os.UserHomeDir()
	return desktop{dir: filepath.Join(home, ".config", "Claude")}
}

func desktopRunning() bool                 { return false }
func desktopProcesses() ([]uint32, string) { return nil, "" }
func quitDesktop() error                   { return nil }
func startDesktop(string) error            { return errUnsupported }
func shellOpen(string)                     {}
func autostartEnabled() bool               { return false }
func setAutostart(bool) error              { return errUnsupported }
func decryptValue([]byte) ([]byte, error)  { return nil, errUnsupported }

func relaunchOutsideDesktop(string) bool { return false }
func launch(string) error                { return errUnsupported }
func endOldSwitcher() bool               { return false }
