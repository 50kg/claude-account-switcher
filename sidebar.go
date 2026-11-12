package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const archivedSessionsHint = "archived-sessions.idx"

type sidebarFile struct {
	data     []byte
	modified time.Time
}

type sidebarEntry struct {
	sessionIndex
	sidebarFile
	archived bool
	priorCLI []string
}

type sidebar struct {
	entries []sidebarEntry
	deleted map[string]os.DirEntry
	hint    os.DirEntry
}

func readSidebar(dir string, allowMissing bool) (sidebar, error) {
	files, err := os.ReadDir(dir)
	if allowMissing && errors.Is(err, os.ErrNotExist) {
		return sidebar{}, nil
	}
	if err != nil {
		return sidebar{}, err
	}
	result := sidebar{deleted: map[string]os.DirEntry{}}
	for _, file := range files {
		name := file.Name()
		if name == archivedSessionsHint {
			result.hint = file
			continue
		}
		if id, ok := strings.CutPrefix(name, "deleted_"); ok && id != "" {
			if !file.Type().IsRegular() {
				return sidebar{}, fmt.Errorf("not a regular deletion marker: %s", filepath.Join(dir, name))
			}
			result.deleted[id] = file
			continue
		}
		if !strings.HasPrefix(name, "local_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(dir, name)
		record, err := readSidebarFile(path, file)
		if err != nil {
			return sidebar{}, err
		}
		index, err := parseSessionIndex(path, record.data)
		if err != nil {
			return sidebar{}, err
		}
		var state struct {
			Archived bool     `json:"isArchived"`
			PriorCLI []string `json:"priorCliSessionIds"`
		}
		if err := json.Unmarshal(record.data, &state); err != nil {
			return sidebar{}, fmt.Errorf("read sidebar state %s: %w", path, err)
		}
		result.entries = append(result.entries, sidebarEntry{index, record, state.Archived, state.PriorCLI})
	}
	return result, nil
}

func readSidebarFile(path string, file os.DirEntry) (sidebarFile, error) {
	if !file.Type().IsRegular() {
		return sidebarFile{}, fmt.Errorf("not a regular sidebar file: %s", path)
	}
	info, err := file.Info()
	if err != nil {
		return sidebarFile{}, err
	}
	data, err := os.ReadFile(path)
	return sidebarFile{data, info.ModTime()}, err
}

// planSidebarSync reads both lists before deciding which target files to change.
// A missing target is a fresh account. A missing source is an unknown list.
func planSidebarSync(sourceDir, targetDir string) ([]sidebarChange, error) {
	source, err := readSidebar(sourceDir, false)
	if err != nil {
		return nil, err
	}
	target, err := readSidebar(targetDir, true)
	if err != nil {
		return nil, err
	}

	byIdentity := map[string]sidebarEntry{}
	byName := map[string]sidebarEntry{}
	for _, entry := range target.entries {
		if old, ok := byIdentity[entry.identity()]; !ok || old.archived && !entry.archived {
			byIdentity[entry.identity()] = entry
		}
		byName[entry.name] = entry
	}
	sort.SliceStable(source.entries, func(i, j int) bool {
		return source.entries[i].activity > source.entries[j].activity
	})
	var changes []sidebarChange
	seen, visible := map[string]bool{}, map[string]bool{}
	visibleIDs := map[string]bool{}
	for _, entry := range source.entries {
		if entry.archived {
			continue
		}
		// Tombstones suppress discovery of unmanaged transcripts. Claude still
		// shows a live local record, even if this account once deleted its copy.
		visibleIDs[entry.localID()] = true
		visibleIDs[entry.cli] = true
		for _, id := range entry.priorCLI {
			visibleIDs[id] = true
		}
		id := entry.identity()
		if seen[id] {
			continue
		}
		seen[id] = true
		dst, exists := byIdentity[id]
		if !exists {
			dst, exists = byName[entry.name]
			if exists && !sameSidebarLineage(entry, dst) {
				return nil, fmt.Errorf("conflicting sidebar session at %s", dst.path)
			}
		}
		if !exists {
			visible[entry.name] = true
			changes = append(changes, sidebarChange{path: filepath.Join(targetDir, entry.name), after: entry.sidebarFile})
			continue
		}
		if visible[dst.name] {
			return nil, fmt.Errorf("ambiguous sidebar session at %s", dst.path)
		}
		visible[dst.name] = true
		next, err := visibleSidebarFile(entry, dst)
		if err != nil {
			return nil, err
		}
		if next.data != nil {
			changes = append(changes, sidebarChange{dst.path, dst.sidebarFile, next})
		}
	}
	for _, entry := range target.entries {
		if entry.archived || visible[entry.name] {
			continue
		}
		next, err := editSidebarFile(entry.sidebarFile, true, nil)
		if err != nil {
			return nil, err
		}
		changes = append(changes, sidebarChange{entry.path, entry.sidebarFile, next})
	}
	if len(changes) > 0 && target.hint != nil {
		// Claude rebuilds this cache from the records. A stale cache can defer
		// loading a session that was just restored to the active list.
		path := filepath.Join(targetDir, archivedSessionsHint)
		old, err := readSidebarFile(path, target.hint)
		if err != nil {
			return nil, err
		}
		changes = slices.Insert(changes, 0, sidebarChange{path: path, before: old})
	}
	// Claude uses these markers to keep a deleted session from appearing as
	// an unmanaged CLI transcript when the target has never had an index.
	for id, file := range source.deleted {
		if visibleIDs[id] || target.deleted[id] != nil {
			continue
		}
		record, err := readSidebarFile(filepath.Join(sourceDir, file.Name()), file)
		if err != nil {
			return nil, err
		}
		changes = append(changes, sidebarChange{path: filepath.Join(targetDir, file.Name()), after: record})
	}
	return changes, nil
}

func sameSidebarLineage(src, dst sidebarEntry) bool {
	return src.cli == "" || dst.cli == "" ||
		slices.Contains(src.priorCLI, dst.cli) || slices.Contains(dst.priorCLI, src.cli)
}

func visibleSidebarFile(src, dst sidebarEntry) (sidebarFile, error) {
	if src.cli != dst.cli && src.cli != "" && (dst.cli == "" || slices.Contains(src.priorCLI, dst.cli)) {
		// Claude can replace a transcript while keeping the same local session.
		// Carry the new transcript and its metadata as a unit.
		return editSidebarFile(src.sidebarFile, false, dst.data)
	}
	if dst.archived {
		return editSidebarFile(dst.sidebarFile, false, nil)
	}
	return sidebarFile{}, nil
}

func editSidebarFile(file sidebarFile, archived bool, settings []byte) (sidebarFile, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(file.data, &fields); err != nil {
		return sidebarFile{}, err
	}
	if settings != nil {
		var local map[string]json.RawMessage
		if err := json.Unmarshal(settings, &local); err != nil {
			return sidebarFile{}, err
		}
		for _, key := range []string{"isStarred", "spaceId"} {
			delete(fields, key)
			if value, ok := local[key]; ok {
				fields[key] = value
			}
		}
	}
	fields["isArchived"], _ = json.Marshal(archived)
	data, err := json.MarshalIndent(fields, "", "\t")
	return sidebarFile{data, file.modified}, err
}

type sidebarChange struct {
	path          string
	before, after sidebarFile
}

func applySidebarChanges(changes []sidebarChange) error {
	if len(changes) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(changes[0].path), 0o700); err != nil {
		return err
	}
	for i, change := range changes {
		if err := writeSidebarFile(change.path, change.after); err != nil {
			return errors.Join(err, restoreSidebarChanges(changes[:i]))
		}
	}
	return nil
}

func restoreSidebarChanges(changes []sidebarChange) error {
	var err error
	for _, change := range slices.Backward(changes) {
		if undo := writeSidebarFile(change.path, change.before); undo != nil {
			err = errors.Join(err, fmt.Errorf("restore sidebar file: %w", undo))
		}
	}
	return err
}

func writeSidebarFile(path string, file sidebarFile) error {
	if file.data == nil {
		return os.Remove(path)
	}
	if err := writeFileAtomic(path, file.data); err != nil {
		return err
	}
	_ = os.Chtimes(path, file.modified, file.modified)
	return nil
}
