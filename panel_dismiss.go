package main

import "time"

// Dismiss only on a real outside click or after a stable owned foreground
// window loses activation. Startup/browser blur is not an outside click.
type panelDismissal struct {
	shownAt    time.Time
	buttons    uint8
	ownedFocus bool
}

func (d *panelDismissal) shown(now time.Time, buttons uint8) {
	d.shownAt, d.buttons, d.ownedFocus = now, buttons, false
}

func (d *panelDismissal) dismiss(now time.Time, buttons uint8, inside, ownForeground, knownForeground bool) bool {
	pressed := buttons &^ d.buttons
	d.buttons = buttons
	if d.shownAt.IsZero() || now.Sub(d.shownAt) < 250*time.Millisecond {
		return false
	}
	if pressed != 0 && !inside {
		return true
	}
	if ownForeground {
		d.ownedFocus = true
	}
	return d.ownedFocus && knownForeground && !ownForeground
}
