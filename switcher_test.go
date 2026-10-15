package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const sampleConfig = "{\n\t\"locale\": \"en-US\",\n\t\"oauth:tokenCache\": \"old\",\n\t\"nested\": {\n\t\t\"a\": [\n\t\t\t1,\n\t\t\t2\n\t\t]\n\t},\n\t\"lastKnownAccountUuid\": \"acc-a\",\n\t\"oauth:tokenCacheV2\": \"djEwAAAA\",\n\t\"zoom\": 1.5\n}"

func TestObjectRoundTripKeepsFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	write(t, path, sampleConfig)
	o, err := readObject(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.write(path); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != sampleConfig {
		t.Fatalf("round trip changed the file:\n%s", got)
	}
}

// The real app config must survive a round trip byte for byte.
// Set CLAUDE_CONFIG_SAMPLE to a copy of config.json to run it.
func TestRealConfigRoundTrip(t *testing.T) {
	src := os.Getenv("CLAUDE_CONFIG_SAMPLE")
	if src == "" {
		t.Skip("CLAUDE_CONFIG_SAMPLE not set")
	}
	want := read(t, src)
	path := filepath.Join(t.TempDir(), "config.json")
	write(t, path, want)
	o, err := readObject(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.write(path); err != nil {
		t.Fatal(err)
	}
	if read(t, path) != want {
		t.Fatal("real config changed on round trip")
	}
}

func TestProfileSwapKeepsSharedSettings(t *testing.T) {
	d := desktop{dir: t.TempDir()}
	profiles := t.TempDir()
	write(t, d.configPath(), sampleConfig)
	write(t, d.cookieFiles()[0], "cookies-a")

	if err := saveProfile(d, filepath.Join(profiles, "a")); err != nil {
		t.Fatal(err)
	}
	if err := clearSignIn(d); err != nil {
		t.Fatal(err)
	}
	if id, _ := d.activeAccount(); id != "" {
		t.Fatalf("still signed in as %q", id)
	}
	if _, err := os.Stat(d.cookieFiles()[0]); !os.IsNotExist(err) {
		t.Fatal("cookies not cleared")
	}

	// Account b signs in and changes a shared setting.
	cfg, _ := readObject(d.configPath())
	cfg.set("lastKnownAccountUuid", json.RawMessage(`"acc-b"`))
	cfg.set("oauth:tokenCacheV2", json.RawMessage(`"djEwBBBB"`))
	cfg.set("zoom", json.RawMessage(`2`))
	if err := cfg.write(d.configPath()); err != nil {
		t.Fatal(err)
	}
	write(t, d.cookieFiles()[0], "cookies-b")
	write(t, d.cookieFiles()[1], "journal-b")
	if err := saveProfile(d, filepath.Join(profiles, "b")); err != nil {
		t.Fatal(err)
	}

	if err := restoreProfile(d, filepath.Join(profiles, "a")); err != nil {
		t.Fatal(err)
	}
	if id, _ := d.activeAccount(); id != "acc-a" {
		t.Fatalf("active = %q, want acc-a", id)
	}
	if got := read(t, d.cookieFiles()[0]); got != "cookies-a" {
		t.Fatalf("cookies = %q", got)
	}
	if _, err := os.Stat(d.cookieFiles()[1]); !os.IsNotExist(err) {
		t.Fatal("journal of b left behind")
	}
	cfg, _ = readObject(d.configPath())
	if v, _ := cfg.get("zoom"); string(v) != "2" {
		t.Fatalf("shared setting lost: zoom = %s", v)
	}
	if v, _ := cfg.get("oauth:tokenCache"); string(v) != `"old"` {
		t.Fatalf("tokenCache = %s", v)
	}
}

func TestSyncSessions(t *testing.T) {
	root := t.TempDir()
	session := func(acct, org, id string, activity int64) string {
		p := filepath.Join(root, acct, org, "local_"+id+".json")
		write(t, p, `{"sessionId":"local_`+id+`","lastActivityAt":`+jsonInt(activity)+`}`)
		return p
	}
	session("a", "oa", "one", 100)
	session("a", "oa", "two", 300)
	session("a", "oa", "gone", 100)
	write(t, filepath.Join(root, "a", "oa", "deleted_gone"), "1")
	session("a", "oa", "dead", 100)
	write(t, filepath.Join(root, "b", "ob", "deleted_dead"), "1")
	session("b", "ob", "two", 200)
	session("b", "ob", "mine", 50)

	n, err := syncSessions(root, "b", "ob")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("wrote %d entries, want 2 (one, newer two)", n)
	}
	target := filepath.Join(root, "b", "ob")
	for _, name := range []string{"local_one.json", "local_two.json", "local_mine.json"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Fatalf("%s missing", name)
		}
	}
	for _, name := range []string{"local_gone.json", "local_dead.json"} {
		if _, err := os.Stat(filepath.Join(target, name)); err == nil {
			t.Fatalf("deleted chat %s came back", name)
		}
	}
	if at, _ := lastActivity(filepath.Join(target, "local_two.json")); at != 300 {
		t.Fatalf("two not refreshed, activity %d", at)
	}
	if n, _ := syncSessions(root, "b", "ob"); n != 0 {
		t.Fatalf("second sync wrote %d, want 0", n)
	}

	// A brand new account gets its folder created.
	if n, _ := syncSessions(root, "c", "oc"); n != 3 {
		t.Fatalf("new account got %d, want 3", n)
	}
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestAutoTarget(t *testing.T) {
	now := time.Now()
	idle := now.Add(-time.Hour)
	s := &switcher{st: State{AutoSwitch: true, Threshold: 96}, active: "a"}
	full := &Account{UUID: "a", Usage: &Usage{Session: 97, CheckedAt: now}}
	roomy := &Account{UUID: "b", Usage: &Usage{Session: 20, Weekly: 40, CheckedAt: now}}
	tight := &Account{UUID: "c", Usage: &Usage{Session: 90, CheckedAt: now}}
	reset := &Account{UUID: "d", Usage: &Usage{Session: 100, SessionReset: now.Add(-time.Minute), Weekly: 10, CheckedAt: now}}
	s.st.Accounts = []*Account{full, tight, roomy, reset}

	if got, _ := s.autoTarget(true, idle, now); got != reset {
		t.Fatalf("picked %v, want d (its session window already reset)", got)
	}
	if got, _ := s.autoTarget(false, idle, now); got != nil {
		t.Fatal("switched while Claude is closed")
	}
	full.Usage.CheckedAt = now.Add(-activeFresh - time.Minute)
	if got, _ := s.autoTarget(true, idle, now); got != nil {
		t.Fatal("switched on stale numbers for the active account")
	}
	full.Usage.CheckedAt = now
	reset.Usage.CheckedAt = now.Add(-idleFresh - time.Minute)
	if got, _ := s.autoTarget(true, idle, now); got != roomy {
		t.Fatalf("picked %v, want b once d is stale", got)
	}
	reset.Usage.CheckedAt = now
	s.st.LastSwitch = now.Add(-time.Minute)
	if got, _ := s.autoTarget(true, idle, now); got != nil {
		t.Fatal("switched again during the cooldown")
	}
	s.st.LastSwitch = time.Time{}
	full.Usage.Session = 50
	if got, _ := s.autoTarget(true, idle, now); got != nil {
		t.Fatalf("switched below the threshold to %s", got.UUID)
	}
}

func TestSpendWeekly(t *testing.T) {
	now := time.Now()
	idle := now.Add(-time.Hour)
	s := &switcher{st: State{SpendWeekly: true, Threshold: 96}, active: "a"}
	cur := &Account{UUID: "a", Usage: &Usage{Session: 30, Weekly: 40, WeeklyReset: now.Add(5 * 24 * time.Hour), CheckedAt: now}}
	later := &Account{UUID: "b", Usage: &Usage{Weekly: 0, WeeklyReset: now.Add(20 * time.Hour), CheckedAt: now}}
	sooner := &Account{UUID: "c", Usage: &Usage{Weekly: 10, WeeklyReset: now.Add(8 * time.Hour), CheckedAt: now}}
	s.st.Accounts = []*Account{cur, later, sooner}

	if got, _ := s.autoTarget(true, idle, now); got != sooner {
		t.Fatalf("picked %v, want c (its week resets first)", got)
	}
	sooner.Usage.Weekly = 70
	if got, _ := s.autoTarget(true, idle, now); got != later {
		t.Fatalf("picked %v, want b once c has used most of its week", got)
	}
	later.Usage.Session = 90
	if got, _ := s.autoTarget(true, idle, now); got != nil {
		t.Fatalf("switched to %s with its 5-hour limit nearly full", got.UUID)
	}
	later.Usage.Session = 0
	later.Usage.WeeklyReset = now.Add(30 * time.Hour)
	if got, _ := s.autoTarget(true, idle, now); got != nil {
		t.Fatal("switched for a week that resets in more than a day")
	}
	later.Usage.WeeklyReset = now.Add(20 * time.Hour)
	cur.Usage.WeeklyReset = now.Add(10 * time.Hour)
	if got, _ := s.autoTarget(true, idle, now); got != nil {
		t.Fatal("left an account whose own week is running out unused")
	}
	cur.Usage.WeeklyReset = now.Add(5 * 24 * time.Hour)
	s.st.HandSwitch = now.Add(-time.Hour)
	if got, _ := s.autoTarget(true, idle, now); got != nil {
		t.Fatal("overrode a switch made by hand an hour ago")
	}
	s.st.HandSwitch = time.Time{}
	s.st.SpendWeekly = false
	if got, _ := s.autoTarget(true, idle, now); got != nil {
		t.Fatal("switched with the setting off")
	}

	// Being nearly full still wins and picks the roomiest account,
	// not the one whose week resets first.
	s.st.SpendWeekly, s.st.AutoSwitch = true, true
	cur.Usage.Session = 97
	later.Usage.Weekly = 30
	roomy := &Account{UUID: "d", Usage: &Usage{Weekly: 5, WeeklyReset: now.Add(6 * 24 * time.Hour), CheckedAt: now}}
	s.st.Accounts = append(s.st.Accounts, roomy)
	if got, why := s.autoTarget(true, idle, now); got != roomy || why != nearlyFull {
		t.Fatalf("picked %v (cause %d), want d because a is nearly full", got, why)
	}
}

func TestUsageTextShowsWeeklyReset(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	a := Account{Usage: &Usage{Session: 20, Weekly: 40, WeeklyReset: now.Add(2 * 24 * time.Hour)}}
	if got, want := usageText(a, 96, now), "5h 20% · week 40%, resets Thu 12:00"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	a.Usage.Weekly = 97
	if got, want := usageText(a, 96, now), "5h 20% · week 97% until Thu 12:00"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	a.Usage.Weekly, a.Usage.WeeklyReset = 0, now.Add(6*24*time.Hour+time.Hour)
	if got, want := usageText(a, 96, now), "5h 20% · week 0%, resets Mon 12 Oct 13:00"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSpendWeeklyWaitsAndComesBack(t *testing.T) {
	now := time.Now()
	idle := now.Add(-time.Hour)
	s := &switcher{st: State{SpendWeekly: true, AutoSwitch: true, Threshold: 96}, active: "a"}
	a := &Account{UUID: "a", Usage: &Usage{Session: 30, Weekly: 40, WeeklyReset: now.Add(5 * 24 * time.Hour), CheckedAt: now}}
	b := &Account{UUID: "b", Usage: &Usage{Weekly: 10, WeeklyReset: now.Add(20 * time.Hour), CheckedAt: now}}
	s.st.Accounts = []*Account{a, b}

	if got, _ := s.autoTarget(true, now.Add(-5*time.Minute), now); got != nil {
		t.Fatal("switched while a chat was active 5 minutes ago")
	}
	if got, cause := s.autoTarget(true, idle, now); got != b || cause != spendWeekly {
		t.Fatalf("picked %v (cause %d), want b to spend its week", got, cause)
	}

	// Spend weekly moved to b. Its 5-hour limit fills at 55% of its week,
	// so the nearly full rule goes back to a.
	s.st.Spending, s.st.SpendUntil = "b", b.Usage.WeeklyReset
	s.active = "b"
	b.Usage.Session, b.Usage.Weekly = 97, 55
	if got, cause := s.autoTarget(true, idle, now); got != a || cause != nearlyFull {
		t.Fatalf("picked %v (cause %d), want a because b is nearly full", got, cause)
	}
	s.active = "a"
	if got, _ := s.autoTarget(true, idle, now); got != nil {
		t.Fatal("went back to b while its 5-hour limit is still full")
	}
	// b's 5-hour window resets: back to b although more than half its week is used.
	b.Usage.Session, b.Usage.SessionReset = 97, now.Add(-time.Minute)
	if got, _ := s.autoTarget(true, idle, now); got != b {
		t.Fatalf("picked %v, want b again once its 5-hour window reset", got)
	}
	// Once b's week has reset there is nothing left to spend.
	s.st.SpendUntil = now.Add(-time.Minute)
	b.Usage.WeeklyReset = now.Add(-time.Minute)
	if got, _ := s.autoTarget(true, idle, now); got != nil {
		t.Fatal("kept spending a week that already reset")
	}
}

func TestAutoNote(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.Local)
	s := &switcher{}
	s.st.Accounts = []*Account{{UUID: "a", Name: "Pepe", Plan: "Pro"}}
	s.st.LastSwitch = now.Add(-6 * time.Hour)
	if got := s.autoNote(now); got != "" {
		t.Fatalf("note after a switch by hand: %q", got)
	}
	s.st.LastCause, s.st.LastFrom = nearlyFull, "a"
	if got, want := s.autoNote(now), "Auto-switched at 03:00, Pepe (Pro) was nearly full"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	s.st.LastCause, s.st.SpendUntil = spendWeekly, now.Add(9*time.Hour)
	if got, want := s.autoNote(now), "Auto-switched at 03:00 to use up this account's week before it resets 18:00"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	s.st.LastSwitch = now.Add(-noteFor - time.Minute)
	if got := s.autoNote(now); got != "" {
		t.Fatalf("note still shown after %s: %q", noteFor, got)
	}
}

func TestLastChatWrite(t *testing.T) {
	root := t.TempDir()
	if got := lastChatWrite(root); !got.IsZero() {
		t.Fatalf("no chats, got %v", got)
	}
	old, recent := filepath.Join(root, "a", "oa", "local_one.json"), filepath.Join(root, "b", "ob", "local_two.json")
	write(t, old, "{}")
	write(t, recent, "{}")
	write(t, filepath.Join(root, "b", "ob", "deleted_three"), "1")
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	os.Chtimes(old, at.Add(-time.Hour), at.Add(-time.Hour))
	os.Chtimes(recent, at, at)
	os.Chtimes(filepath.Join(root, "b", "ob", "deleted_three"), time.Now(), time.Now())
	if got := lastChatWrite(root); !got.Equal(at) {
		t.Fatalf("got %v, want %v", got, at)
	}
}

func TestCookieFilesFollowAppLayout(t *testing.T) {
	d := desktop{dir: t.TempDir()}
	if got, want := d.cookieFiles()[0], filepath.Join(d.dir, "Cookies"); got != want {
		t.Fatalf("without Network/: %q, want %q", got, want)
	}
	if err := os.Mkdir(filepath.Join(d.dir, "Network"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, want := d.cookieFiles()[0], filepath.Join(d.dir, "Network", "Cookies"); got != want {
		t.Fatalf("with Network/: %q, want %q", got, want)
	}
}
