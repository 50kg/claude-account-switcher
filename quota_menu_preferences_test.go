package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuotaMenuHeightRoundTripOnlyWritesUIPreference(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ui.json")
	sentinel := []byte("account state must remain untouched")
	if err := os.WriteFile(filepath.Join(dir, "state.json"), sentinel, 0o600); err != nil {
		t.Fatal(err)
	}
	if height, err := loadQuotaMenuHeight(path); err != nil || height != 0 {
		t.Fatalf("missing preference: %d, %v", height, err)
	}
	for _, want := range []int{520, 810, 0} {
		if err := saveQuotaMenuHeight(path, want); err != nil {
			t.Fatal(err)
		}
		if got, err := loadQuotaMenuHeight(path); err != nil || got != want {
			t.Fatalf("saved height = %d, %v; want %d", got, err, want)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil || string(data) != string(sentinel) {
		t.Fatal("UI preference changed account state")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatal("preference left unexpected files")
	}
}

func TestQuotaMenuHeightRejectsInvalidAndHandlesWriteFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ui.json")
	for _, data := range []string{`{`, `{"menuHeight":-1}`, `{"menuHeight":1}`, `{"menuHeight":999999}`, strings.Repeat(" ", 4097)} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if height, err := loadQuotaMenuHeight(path); err == nil || height != 0 {
			t.Fatalf("invalid preference accepted: %d, %v", height, err)
		}
	}
	if err := saveQuotaMenuHeight(path, -1); err == nil {
		t.Fatal("invalid height was saved")
	}
	// A regular file cannot be used as the parent directory.
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveQuotaMenuHeight(filepath.Join(blocked, "ui.json"), 520); err == nil {
		t.Fatal("write failure was hidden")
	}
	if data, _ := os.ReadFile(blocked); string(data) != "keep" {
		t.Fatal("failed preference write changed an unrelated file")
	}
}
