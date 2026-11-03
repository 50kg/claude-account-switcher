package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// A profile is a saved sign-in: the account's config.json entries and the
// app's cookie store. Tokens stay encrypted by the app's own key, the
// ordinary profiles never write them in plain text. The Windows fork's
// explicit, user-created account migration export is a separate exception.

// saveProfile copies the current sign-in into dir. The app must be closed.
func saveProfile(d desktop, dir string) error {
	cfg, err := readObject(d.configPath())
	if err != nil {
		return err
	}
	saved := &object{vals: map[string]json.RawMessage{}}
	for _, k := range authKeys {
		if v, ok := cfg.get(k); ok {
			saved.set(k, v)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := saved.write(filepath.Join(dir, "config.json")); err != nil {
		return err
	}
	for _, f := range d.cookieFiles() {
		if err := copyFile(f, filepath.Join(dir, filepath.Base(f))); err != nil {
			return err
		}
	}
	return nil
}

// restoreProfile puts a saved sign-in back. The app must be closed.
func restoreProfile(d desktop, dir string) error {
	saved, err := readObject(filepath.Join(dir, "config.json"))
	if err != nil {
		return err
	}
	cfg, err := readObject(d.configPath())
	if err != nil {
		return err
	}
	for _, k := range authKeys {
		if v, ok := saved.get(k); ok {
			cfg.set(k, v)
		} else {
			cfg.del(k)
		}
	}
	for _, f := range d.cookieFiles() {
		if err := copyFile(filepath.Join(dir, filepath.Base(f)), f); err != nil {
			return err
		}
	}
	return cfg.write(d.configPath())
}

// clearSignIn removes the current sign-in locally so the app shows its login
// screen. It does not sign out, which would revoke the saved tokens.
func clearSignIn(d desktop) error {
	cfg, err := readObject(d.configPath())
	if err != nil {
		return err
	}
	for _, k := range authKeys {
		cfg.del(k)
	}
	for _, f := range d.cookieFiles() {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return cfg.write(d.configPath())
}

const keepBackups = 10

// backup copies config.json and the cookie store before a switch touches them.
func backup(d desktop, root string) (string, error) {
	dir := filepath.Join(root, time.Now().Format("20060102-150405"))
	files := append([]string{d.configPath()}, d.cookieFiles()...)
	for _, f := range files {
		if err := copyFile(f, filepath.Join(dir, filepath.Base(f))); err != nil {
			return "", err
		}
	}
	pruneBackups(root)
	return dir, nil
}

// restoreBackup undoes a switch that failed halfway.
func restoreBackup(d desktop, dir string) error {
	files := append([]string{d.configPath()}, d.cookieFiles()...)
	for _, f := range files {
		if err := copyFile(filepath.Join(dir, filepath.Base(f)), f); err != nil {
			return err
		}
	}
	return nil
}

func pruneBackups(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keepBackups {
		_ = os.RemoveAll(filepath.Join(root, names[0]))
		names = names[1:]
	}
}
