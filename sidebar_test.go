package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSidebarSyncUsesOutgoingListOnEverySwitch(t *testing.T) {
	root := t.TempDir()
	a, b, c := filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c")
	write(t, filepath.Join(a, "local_keep.json"), `{"cliSessionId":"keep","lastActivityAt":2}`)
	write(t, filepath.Join(a, "local_new.json"), `{"cliSessionId":"new"}`)
	write(t, filepath.Join(a, "local_archived.json"), `{"cliSessionId":"archived","isArchived":true}`)
	write(t, filepath.Join(a, "deleted_gone"), "1")
	write(t, filepath.Join(b, "local_alias.json"), `{"cliSessionId":"keep","isArchived":true,"isStarred":true,"extra":42}`)
	write(t, filepath.Join(b, "local_old.json"), `{"cliSessionId":"gone","title":"old title"}`)
	write(t, filepath.Join(b, "deleted_keep"), "2")
	write(t, filepath.Join(b, archivedSessionsHint), `{"v":1,"archived":["local_alias"]}`)
	write(t, filepath.Join(c, "local_unrelated.json"), `{"cliSessionId":"unrelated"}`)

	switchSidebar(t, a, b)
	assertVisibleSidebar(t, b, "keep", "new")
	assertMissingSidebarFile(t, filepath.Join(b, "local_keep.json"))
	assertMissingSidebarFile(t, filepath.Join(b, "local_archived.json"))
	assertMissingSidebarFile(t, filepath.Join(b, archivedSessionsHint))
	alias, err := readObject(filepath.Join(b, "local_alias.json"))
	if err != nil {
		t.Fatal(err)
	}
	starred, _ := alias.get("isStarred")
	extra, _ := alias.get("extra")
	if string(starred) != "true" || string(extra) != "42" {
		t.Fatal("unarchiving changed account-local metadata")
	}
	if read(t, filepath.Join(b, "deleted_gone")) != "1" {
		t.Fatal("the source deletion marker was lost")
	}
	if changes, err := planSidebarSync(a, b); err != nil || len(changes) != 0 {
		t.Fatalf("retry planned %d writes, %v", len(changes), err)
	}

	if err := os.Remove(filepath.Join(b, "local_new.json")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(b, "deleted_new"), "3")
	write(t, filepath.Join(b, "local_second.json"), `{"cliSessionId":"second"}`)
	switchSidebar(t, b, a)
	assertVisibleSidebar(t, a, "keep", "second")
	if read(t, filepath.Join(a, "deleted_new")) != "3" {
		t.Fatal("the latest deletion marker did not follow the sidebar")
	}
	assertMissingSidebarFile(t, filepath.Join(a, "deleted_keep"))

	switchSidebar(t, a, c)
	assertVisibleSidebar(t, c, "keep", "second")
	if _, err := os.Stat(filepath.Join(c, "local_unrelated.json")); err != nil {
		t.Fatal("target-only conversation lost its recoverable index:", err)
	}
	fresh := filepath.Join(root, "fresh", "org")
	switchSidebar(t, c, fresh)
	assertVisibleSidebar(t, fresh, "keep", "second")
}

func TestSidebarSyncKeepsNewerTranscriptWithSameLocalID(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	write(t, filepath.Join(a, "local_same.json"), `{"cliSessionId":"new","priorCliSessionIds":["old"],"lastActivityAt":2,"completedTurns":3,"isStarred":true,"spaceId":"a"}`)
	target := filepath.Join(b, "local_same.json")
	write(t, target, `{"cliSessionId":"old","lastActivityAt":1,"isArchived":true,"spaceId":"b"}`)

	switchSidebar(t, a, b)
	assertVisibleSidebar(t, b, "new")
	record, err := readObject(target)
	if err != nil {
		t.Fatal(err)
	}
	turns, _ := record.get("completedTurns")
	if _, starred := record.get("isStarred"); starred || stringField(record, "spaceId") != "b" || string(turns) != "3" {
		t.Fatal("transcript refresh mixed transcript and account-local settings")
	}
	switchSidebar(t, b, a)
	assertVisibleSidebar(t, a, "new")
}

func TestSidebarSyncUnreadableListLeavesTargetUntouched(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	target := filepath.Join(b, "local_old.json")
	const original = `{"cliSessionId":"old"}`
	write(t, target, original)
	if changes, err := planSidebarSync(a, b); err == nil || len(changes) != 0 {
		t.Fatalf("missing source planned %d writes, %v", len(changes), err)
	}
	write(t, filepath.Join(a, "local_broken.json"), "{")
	if changes, err := planSidebarSync(a, b); err == nil || len(changes) != 0 {
		t.Fatalf("unreadable source planned %d writes, %v", len(changes), err)
	}
	if read(t, target) != original {
		t.Fatal("target changed without a reliable source")
	}
	if err := os.Remove(filepath.Join(a, "local_broken.json")); err != nil {
		t.Fatal(err)
	}
	brokenTarget := filepath.Join(b, "local_broken.json")
	write(t, brokenTarget, "{")
	if changes, err := planSidebarSync(a, b); err == nil || len(changes) != 0 {
		t.Fatalf("unreadable target planned %d writes, %v", len(changes), err)
	}
	if err := os.Remove(brokenTarget); err != nil {
		t.Fatal(err)
	}
	switchSidebar(t, a, b)
	assertVisibleSidebar(t, b)
}

func TestSidebarSyncWriteFailureRollsBack(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	write(t, filepath.Join(a, "local_new.json"), `{"cliSessionId":"new"}`)
	target := filepath.Join(b, "local_old.json")
	const original = `{"cliSessionId":"old"}`
	write(t, target, original)
	hint := filepath.Join(b, archivedSessionsHint)
	write(t, hint, `{"v":1,"archived":[]}`)
	if err := os.Mkdir(target+".switcher-tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	changes, err := planSidebarSync(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := applySidebarChanges(changes); err == nil {
		t.Fatal("expected a blocked write")
	}
	assertMissingSidebarFile(t, filepath.Join(b, "local_new.json"))
	if read(t, target) != original || read(t, hint) != `{"v":1,"archived":[]}` {
		t.Fatal("failed sync did not restore the target files")
	}
}

func switchSidebar(t *testing.T, source, target string) {
	t.Helper()
	changes, err := planSidebarSync(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := applySidebarChanges(changes); err != nil {
		t.Fatal(err)
	}
}

func assertVisibleSidebar(t *testing.T, path string, want ...string) {
	t.Helper()
	list, err := readSidebar(path, false)
	if err != nil {
		t.Fatal(err)
	}
	var visible []string
	for _, entry := range list.entries {
		if !entry.archived {
			visible = append(visible, entry.cli)
		}
	}
	slices.Sort(visible)
	if !slices.Equal(visible, want) {
		t.Fatalf("visible chats = %v; want %v", visible, want)
	}
}

func assertMissingSidebarFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, stat returned %v", path, err)
	}
}
