package main

import (
	"testing"
	"time"
)

func TestPanelDismissalIgnoresOpeningFocusAndHeldClick(t *testing.T) {
	now := time.Now()
	var d panelDismissal
	d.shown(now, 1)
	if d.dismiss(now.Add(50*time.Millisecond), 1, false, false, true) {
		t.Fatal("opening activation dismissed the panel")
	}
	if d.dismiss(now.Add(time.Second), 1, false, false, true) {
		t.Fatal("the initiating held mouse button dismissed the panel")
	}
	if d.dismiss(now.Add(2*time.Second), 0, false, false, true) {
		t.Fatal("unfocused startup dismissed the panel")
	}
	if !d.dismiss(now.Add(3*time.Second), 1, false, false, true) {
		t.Fatal("a new outside click did not dismiss")
	}
}

func TestPanelDismissalTracksRealOwnedFocusAndInsideClicks(t *testing.T) {
	now := time.Now()
	var d panelDismissal
	d.shown(now, 0)
	if d.dismiss(now.Add(time.Second), 1, true, true, true) {
		t.Fatal("inside click dismissed")
	}
	if d.dismiss(now.Add(2*time.Second), 0, true, false, false) {
		t.Fatal("transient unknown foreground dismissed")
	}
	if !d.dismiss(now.Add(3*time.Second), 0, true, false, true) {
		t.Fatal("real focus loss after activation did not dismiss")
	}
	d.shown(now.Add(4*time.Second), 0)
	if d.dismiss(now.Add(5*time.Second), 0, false, false, true) {
		t.Fatal("reopened panel retained stale focus ownership")
	}
}
