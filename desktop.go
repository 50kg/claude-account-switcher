package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// desktop describes the data folder of the Claude desktop app.
type desktop struct{ dir string }

func (d desktop) configPath() string     { return filepath.Join(d.dir, "config.json") }
func (d desktop) localStatePath() string { return filepath.Join(d.dir, "Local State") }
func (d desktop) sessionsDir() string    { return filepath.Join(d.dir, "claude-code-sessions") }
func (d desktop) spacesDir() string      { return filepath.Join(d.dir, "local-agent-mode-sessions") }

// cookieFiles hold the claude.ai web session. They are locked while the app runs.
// Newer Chromium builds keep them in Network/, the macOS app keeps them in the
// data folder itself.
func (d desktop) cookieFiles() []string {
	base := d.dir
	if _, err := os.Stat(filepath.Join(d.dir, "Network")); err == nil {
		base = filepath.Join(d.dir, "Network")
	}
	return []string{
		filepath.Join(base, "Cookies"),
		filepath.Join(base, "Cookies-journal"),
	}
}

// authKeys are the config.json entries that belong to the signed-in account.
// Everything else in config.json is app state shared by all accounts.
var authKeys = []string{"oauth:tokenCache", "oauth:tokenCacheV2", "lastKnownAccountUuid"}

// activeAccount returns the account the app is signed in with, or "" when signed out.
func (d desktop) activeAccount() (string, error) {
	cfg, err := readObject(d.configPath())
	if err != nil {
		return "", err
	}
	var id string
	if raw, ok := cfg.get("lastKnownAccountUuid"); ok {
		_ = json.Unmarshal(raw, &id)
	}
	if _, ok := cfg.get("oauth:tokenCacheV2"); !ok {
		return "", nil
	}
	return id, nil
}

// tokenCache returns the encrypted oauth:tokenCacheV2 value of a config file.
func tokenCacheOf(configPath string) (string, error) {
	cfg, err := readObject(configPath)
	if err != nil {
		return "", err
	}
	raw, ok := cfg.get("oauth:tokenCacheV2")
	if !ok {
		return "", errors.New("no saved sign-in")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return s, nil
}

// object is a JSON object that keeps its key order, so config.json only
// changes where we touch it.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func readObject(path string) (*object, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("%s: not a JSON object", path)
	}
	o := &object{vals: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(tok.(string), v)
	}
	return o, nil
}

func (o *object) get(k string) (json.RawMessage, bool) {
	v, ok := o.vals[k]
	return v, ok
}

func (o *object) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// write saves the object the way the app does: tab indented, no trailing newline.
func (o *object) write(path string) error {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(o.vals[k])
	}
	buf.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, buf.Bytes(), "", "\t"); err != nil {
		return err
	}
	return writeFileAtomic(path, out.Bytes())
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".switcher-tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// copyFile copies src to dst. A missing src removes dst, so a snapshot
// restores exactly the files it had.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst+".switcher-tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if info, err := in.Stat(); err == nil {
		_ = os.Chtimes(dst+".switcher-tmp", info.ModTime(), info.ModTime())
	}
	return os.Rename(dst+".switcher-tmp", dst)
}
