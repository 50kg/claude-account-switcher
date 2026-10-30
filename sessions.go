package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// When the app quits it marks the chats it stopped mid-turn, and on its next
// start it sends them a message to go on. These are the keys of that mark.
var cutMarkKeys = []string{"interruptedByQuitAt", "interruptedUnseenResume", "interruptedAwaitingPerson"}

// cutChat is a chat whose turn the app stopped when it quit.
type cutChat struct {
	path, title, folder string
}

// takeCutChats marks the chats stopped mid-turn by the quit at since on
// their entry in the account's folder, where the app looks for the mark on
// its next start. Those are the chats the app marked itself and the running
// ones from runningChats: the app only marks when it quits through its own
// cleanup, which a quit from outside does not always reach. Marks left on
// other accounts' copies would continue the chat a second time after a later
// switch, so they are removed. Chats the account cannot see keep their mark.
func takeCutChats(root, account, org string, since time.Time, running map[string]bool) []cutChat {
	target := filepath.Join(root, account, org)
	paths, _ := filepath.Glob(filepath.Join(root, "*", "*", "local_*.json"))
	type entry struct {
		path string
		obj  *object
	}
	marked := map[string][]entry{}
	inTarget := map[string]entry{}
	for _, p := range paths {
		o, err := readObject(p)
		if err != nil {
			continue
		}
		cli := stringField(o, "cliSessionId")
		if cli == "" {
			continue
		}
		if samePath(filepath.Dir(p), target) {
			inTarget[cli] = entry{p, o}
		}
		var at int64
		if raw, ok := o.get("interruptedByQuitAt"); ok && json.Unmarshal(raw, &at) == nil && at >= since.UnixMilli() {
			marked[cli] = append(marked[cli], entry{p, o})
		}
	}
	for cli := range running {
		if _, ok := marked[cli]; !ok {
			marked[cli] = nil
		}
	}
	var cut []cutChat
	for cli, sources := range marked {
		dst, ok := inTarget[cli]
		if !ok {
			continue
		}
		switch {
		case len(sources) == 0:
			at, _ := json.Marshal(since.UnixMilli())
			dst.obj.set("interruptedByQuitAt", at)
			dst.obj.set("interruptedAwaitingPerson", json.RawMessage("false"))
		case !samePath(sources[0].path, dst.path):
			for _, k := range cutMarkKeys {
				if v, ok := sources[0].obj.get(k); ok {
					dst.obj.set(k, v)
				}
			}
		}
		if len(sources) == 0 || !samePath(sources[0].path, dst.path) {
			if err := dst.obj.write(dst.path); err != nil {
				log.Printf("move quit mark to %s: %v", dst.path, err)
				continue
			}
		}
		for _, src := range sources {
			if !samePath(src.path, dst.path) {
				if err := clearCutMark(src.path); err != nil {
					log.Printf("clear quit mark of %s: %v", src.path, err)
				}
			}
		}
		folder := stringField(dst.obj, "originCwd")
		if folder == "" {
			folder = stringField(dst.obj, "cwd")
		}
		if folder != "" {
			folder = filepath.Base(folder)
		}
		title := stringField(dst.obj, "title")
		if title == "" {
			title = folder
		}
		cut = append(cut, cutChat{path: dst.path, title: title, folder: folder})
	}
	sort.Slice(cut, func(i, j int) bool { return cut[i].title < cut[j].title })
	return cut
}

// A transcript written to within this long can belong to a running chat. A
// chat that sat this long on an open turn is waiting for someone anyway.
const runningWithin = 30 * time.Minute

// runningChats returns the CLI session IDs of the chats whose turn is open,
// from the transcripts the CLI keeps in projectsDir/<folder>/<id>.jsonl.
func runningChats(projectsDir string, now time.Time) map[string]bool {
	running := map[string]bool{}
	paths, _ := filepath.Glob(filepath.Join(projectsDir, "*", "*.jsonl"))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || now.Sub(info.ModTime()) > runningWithin {
			continue
		}
		if turnOpen(readTail(p, 256<<10)) {
			running[strings.TrimSuffix(filepath.Base(p), ".jsonl")] = true
		}
	}
	return running
}

func readTail(path string, n int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > n {
		_, _ = f.Seek(-n, io.SeekEnd)
	}
	data, _ := io.ReadAll(f)
	return data
}

// turnOpen reads a transcript's last message: the turn is open while Claude
// still has to answer a prompt or a tool result, or has called a tool.
func turnOpen(tail []byte) bool {
	lines := bytes.Split(tail, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var e struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				StopReason *string         `json:"stop_reason"`
				Content    json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(lines[i], &e) != nil || e.IsSidechain {
			continue
		}
		switch e.Type {
		case "assistant":
			if e.Message.StopReason == nil {
				return true
			}
			switch *e.Message.StopReason {
			case "end_turn", "stop_sequence", "max_tokens", "refusal":
				return false
			}
			return true
		case "user":
			return !bytes.Contains(e.Message.Content, []byte("[Request interrupted by user"))
		}
	}
	return false
}

// clearCutMark removes a chat's quit mark, so the app leaves the chat alone.
func clearCutMark(path string) error {
	o, err := readObject(path)
	if err != nil {
		return err
	}
	for _, k := range cutMarkKeys {
		o.del(k)
	}
	return o.write(path)
}

func stringField(o *object, key string) string {
	var s string
	if raw, ok := o.get(key); ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
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

// projectsDir is where the Claude Code CLI keeps its transcripts.
func projectsDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}
