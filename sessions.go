package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The app lists Claude Code chats from claude-code-sessions/<account>/<org>/,
// one local_<id>.json per chat. The conversations themselves live in
// ~/.claude/projects and are shared, so making a chat visible in another
// account only needs its entry file. A deleted_<id> file marks a chat the
// user deleted; such chats are never copied back.

// syncSessions copies chat entries from every other account folder into the
// target account's folder. It adds missing chats and refreshes older copies,
// it never deletes anything. Local IDs differ between accounts; cliSessionId
// identifies the actual conversation. An existing target entry keeps its own
// local ID and account-specific settings. Returns how many entries it wrote.
func syncSessions(root, account, org string) (int, error) {
	target := filepath.Join(root, account, org)
	dirs, err := filepath.Glob(filepath.Join(root, "*", "*"))
	if err != nil {
		return 0, err
	}

	deleted := map[string]bool{}
	var all, sources []sessionIndex
	targets := map[string]sessionIndex{}
	protectedTargets := map[string]bool{}
	byIdentity := map[string]string{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			log.Printf("Skip session index folder %s: %v", dir, err)
			if samePath(dir, target) {
				// Without the target's indexes, importing could overwrite its entries.
				return 0, nil
			}
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			switch {
			case strings.HasPrefix(name, "deleted_"):
				deleted[strings.TrimPrefix(name, "deleted_")] = true
			case strings.HasPrefix(name, "local_") && strings.HasSuffix(name, ".json"):
				entry, err := readSessionIndex(filepath.Join(dir, name))
				if err != nil {
					log.Printf("Skip session index %s: %v", filepath.Join(dir, name), err)
					if samePath(dir, target) {
						protectedTargets[name] = true
					}
					continue
				}
				all = append(all, entry)
				if samePath(dir, target) {
					targets[name] = entry
					byIdentity[entry.identity()] = name
				} else {
					sources = append(sources, entry)
				}
			}
		}
	}
	// A deletion marker may name another local alias of the same conversation.
	deletedCLI := map[string]bool{}
	for _, entry := range all {
		if deleted[entry.localID()] && entry.cli != "" {
			deletedCLI[entry.cli] = true
		}
	}
	// A new account imports the freshest copy once, even with many source aliases.
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].activity > sources[j].activity })

	if err := os.MkdirAll(target, 0o700); err != nil {
		return 0, err
	}
	written := 0
	for _, s := range sources {
		if deleted[s.localID()] || deletedCLI[s.cli] {
			continue
		}
		if protectedTargets[s.name] {
			log.Printf("Skip session index %s: target index could not be read", s.path)
			continue
		}
		if name, exists := byIdentity[s.identity()]; exists && name != s.name {
			// Do not replace account-local settings with another account's alias.
			continue
		}
		if dst, exists := targets[s.name]; exists && dst.cli != "" && s.cli != "" && dst.cli != s.cli {
			log.Printf("Skip session index %s: local ID belongs to a different conversation in the target", s.path)
			continue
		}
		dst := filepath.Join(target, s.name)
		if old, exists := targets[s.name]; exists && s.activity <= old.activity {
			continue
		}
		if err := copyFile(s.path, dst); err != nil {
			return written, err
		}
		written++
		targets[s.name] = s
		byIdentity[s.identity()] = s.name
	}
	return written, nil
}

type sessionIndex struct {
	path, name, cli string
	activity        int64
}

func readSessionIndex(path string) (sessionIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionIndex{}, err
	}
	var entry *struct {
		CLISessionID   string `json:"cliSessionId"`
		LastActivityAt int64  `json:"lastActivityAt"`
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		return sessionIndex{}, fmt.Errorf("read session index %s: %w", path, err)
	}
	if entry == nil {
		return sessionIndex{}, fmt.Errorf("session index %s is not an object", path)
	}
	return sessionIndex{path: path, name: filepath.Base(path), cli: entry.CLISessionID, activity: entry.LastActivityAt}, nil
}

func (s sessionIndex) identity() string {
	if s.cli != "" {
		return "cli:" + s.cli
	}
	// Legacy entries without a CLI ID remain distinct unless their filenames match.
	return "file:" + s.name
}

func (s sessionIndex) localID() string {
	return strings.TrimSuffix(strings.TrimPrefix(s.name, "local_"), ".json")
}

func lastActivity(path string) (int64, error) {
	entry, err := readSessionIndex(path)
	return entry.activity, err
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// lastChatWrite is when any chat entry was last written. The app rewrites
// the entry of a running Claude Code chat as it goes, so this tells whether
// someone is at work in Claude right now.
func lastChatWrite(root string) time.Time {
	var last time.Time
	paths, _ := filepath.Glob(filepath.Join(root, "*", "*", "local_*.json"))
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.ModTime().After(last) {
			last = info.ModTime()
		}
	}
	return last
}
