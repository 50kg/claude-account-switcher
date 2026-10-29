package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// The titles come in as arguments, so they need no AppleScript quoting.
const resumeScript = `on run argv
	set r to choose from list argv with title "Continue chats?" with prompt "These chats were working when Claude closed for the switch. Claude continues the selected ones when it starts again." default items argv OK button name "Continue" cancel button name "Don't continue" with multiple selections allowed
	if r is false then return ""
	set out to ""
	repeat with i in r
		set out to out & i & linefeed
	end repeat
	return out
end run`

// nativeResume asks which of the chats titled titles should go on and
// returns one answer per title. After wait without an answer, or if the
// dialog cannot open, all of them go on.
func nativeResume(titles []string, wait time.Duration) []bool {
	keep := make([]bool, len(titles))
	for i := range keep {
		keep[i] = true
	}
	// Numbered, so two chats with the same title stay apart.
	args := []string{"-e", resumeScript}
	for i, t := range titles {
		args = append(args, fmt.Sprintf("%d. %s", i+1, t))
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", args...).Output()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return keep
	case errors.As(err, &exit):
		// Don't continue makes osascript fail with "User canceled".
		return make([]bool, len(titles))
	case err != nil:
		return keep
	}
	keep = make([]bool, len(titles))
	for _, line := range strings.Split(string(out), "\n") {
		n, _, _ := strings.Cut(line, ". ")
		if i, err := strconv.Atoi(n); err == nil && i >= 1 && i <= len(titles) {
			keep[i-1] = true
		}
	}
	return keep
}
