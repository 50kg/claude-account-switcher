package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Account is a Claude account the switcher has seen signed in.
type Account struct {
	UUID    string `json:"uuid"`
	Org     string `json:"org"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Plan    string `json:"plan"`
	Usage   *Usage `json:"usage,omitempty"`
	Problem string `json:"problem,omitempty"`
}

func (a *Account) label() string {
	name := a.Name
	if name == "" {
		name = a.Email
	}
	if a.Plan != "" {
		name += " (" + a.Plan + ")"
	}
	return name
}

// State is what the switcher remembers between runs.
type State struct {
	Accounts   []*Account `json:"accounts"`
	AutoSwitch bool       `json:"autoSwitch"`
	Threshold  float64    `json:"threshold"`
	// SpendWeekly moves to an account whose week is about to reset mostly unused.
	SpendWeekly bool      `json:"spendWeekly"`
	LastSwitch  time.Time `json:"lastSwitch"`
	HandSwitch  time.Time `json:"handSwitch"`
	// Why the last switch happened, and the account it left.
	LastCause switchCause `json:"lastCause,omitempty"`
	LastFrom  string      `json:"lastFrom,omitempty"`
	// Spend weekly moved to this account and keeps coming back to it until
	// its week resets, even once more than half of it is used.
	Spending   string    `json:"spending,omitempty"`
	SpendUntil time.Time `json:"spendUntil,omitempty"`
	AppExe     string    `json:"appExe,omitempty"`
	// The daily check for a new release is on unless this is set by hand.
	NoUpdateCheck bool      `json:"noUpdateCheck,omitempty"`
	UpdateChecked time.Time `json:"updateChecked,omitempty"`
	LatestVersion string    `json:"latestVersion,omitempty"`
}

// switchCause says why a switch happened.
type switchCause int

const (
	byHand switchCause = iota
	nearlyFull
	spendWeekly
)

const (
	defaultThreshold = 96
	// An auto switch only goes to an account with this much room below the threshold.
	autoHeadroom = 10
	// No second auto switch this soon after the last switch.
	autoCooldown = 10 * time.Minute
	// Spend weekly: an account whose week resets this soon with at most this
	// much of it used is worth moving to before the rest is lost.
	expiringWithin    = 24 * time.Hour
	expiringMaxWeekly = 50
	// A switch made by hand pauses spend weekly this long.
	handPause = 5 * time.Hour
	// Spend weekly is never urgent, so it waits until no chat has been
	// active this long instead of cutting off a reply.
	idleFor = 15 * time.Minute
	// The menu says why the last auto switch happened for this long.
	noteFor = 12 * time.Hour

	// The usage endpoint rate-limits per account, and Claude polls it too.
	// The active account is checked more often once it gets close.
	activeEvery = 2 * time.Minute
	hotEvery    = time.Minute
	hotAt       = 85
	idleEvery   = 10 * time.Minute
	// Auto switch only trusts numbers this fresh.
	activeFresh = 5 * time.Minute
	idleFresh   = idleEvery + 5*time.Minute
	maxBackoff  = 30 * time.Minute
)

type switcher struct {
	dir      string
	mu       sync.Mutex
	st       State
	active   string
	busy     atomic.Bool
	status   string
	addingAt time.Time
	onChange func()
	// After a 429, an account is not asked again before its backoff ends.
	backoff map[string]time.Time
	strikes map[string]int
}

func newSwitcher(dir string) *switcher {
	s := &switcher{dir: dir, st: State{Threshold: defaultThreshold}, onChange: func() {},
		backoff: map[string]time.Time{}, strikes: map[string]int{}}
	if data, err := os.ReadFile(s.statePath()); err == nil {
		_ = json.Unmarshal(data, &s.st)
	}
	if s.st.Threshold <= 0 {
		s.st.Threshold = defaultThreshold
	}
	return s
}

func (s *switcher) statePath() string             { return filepath.Join(s.dir, "state.json") }
func (s *switcher) profileDir(uuid string) string { return filepath.Join(s.dir, "accounts", uuid) }
func (s *switcher) backupsDir() string            { return filepath.Join(s.dir, "backups") }

// app looks the data folder up on every use, because Claude can move it in an
// update while the switcher keeps running.
func (s *switcher) app() desktop { return claudeDesktop() }

// saveState writes state.json. Callers hold s.mu.
func (s *switcher) saveState() {
	data, _ := json.MarshalIndent(s.st, "", "  ")
	if err := writeFileAtomic(s.statePath(), data); err != nil {
		log.Printf("save state: %v", err)
	}
}

func (s *switcher) setStatus(msg string) {
	s.mu.Lock()
	s.status = msg
	s.mu.Unlock()
	if msg != "" {
		log.Print(msg)
	}
	s.onChange()
}

func (s *switcher) account(uuid string) *Account {
	for _, a := range s.st.Accounts {
		if a.UUID == uuid {
			return a
		}
	}
	return nil
}

// tokenFor returns an access token for an account: from the app's live config
// when it is signed in, otherwise from its saved profile.
func (s *switcher) tokenFor(uuid, active string) (string, error) {
	path := filepath.Join(s.profileDir(uuid), "config.json")
	if uuid == active {
		path = s.app().configPath()
	}
	cache, err := tokenCacheOf(path)
	if err != nil {
		return "", err
	}
	return accessToken(cache, uuid)
}

// refresh notices sign-ins, keeps usage current and runs the auto switch.
func (s *switcher) refresh(ctx context.Context) {
	if what, do := s.check(ctx); do != nil {
		s.run(what, do)
	}
}

// check fetches the usage that is due and saves it. It never restarts Claude,
// so -status can use it; the switch or import it finds due is returned instead.
func (s *switcher) check(ctx context.Context) (string, func() error) {
	if s.busy.Load() {
		return "", nil
	}
	active, err := s.app().activeAccount()
	if err != nil {
		log.Printf("read app config: %v", err)
	}
	_, exe := desktopProcesses()

	s.mu.Lock()
	s.active = active
	if exe != "" {
		s.st.AppExe = exe
	}
	type job struct {
		uuid string
		new  bool
	}
	now := time.Now()
	var jobs []job
	if active != "" && s.account(active) == nil && now.After(s.backoff[active]) {
		jobs = append(jobs, job{active, true})
	}
	for _, a := range s.st.Accounts {
		every := idleEvery
		if a.UUID == active {
			every = activeEvery
			if a.Usage != nil && a.Usage.current(now).peak() >= hotAt {
				every = hotEvery
			}
		}
		due := a.Usage == nil || now.Sub(a.Usage.CheckedAt) >= every-5*time.Second
		if due && now.After(s.backoff[a.UUID]) {
			jobs = append(jobs, job{a.UUID, false})
		}
	}
	s.mu.Unlock()

	added := false
	for _, j := range jobs {
		token, err := s.tokenFor(j.uuid, active)
		var p profile
		var u *Usage
		if err == nil && j.new {
			p, err = fetchProfile(ctx, token)
		}
		if err == nil {
			u, err = fetchUsage(ctx, token)
		}

		s.mu.Lock()
		var limited *rateLimited
		if errors.As(err, &limited) {
			s.strikes[j.uuid]++
			wait := limited.after
			if wait <= 0 {
				wait = min(activeEvery<<(s.strikes[j.uuid]-1), maxBackoff)
			}
			s.backoff[j.uuid] = time.Now().Add(wait)
			log.Printf("usage %s: rate limited, next try in %s", j.uuid[:8], wait)
		} else if err == nil {
			delete(s.strikes, j.uuid)
		}
		a := s.account(j.uuid)
		if j.new && err == nil {
			a = &Account{UUID: j.uuid, Org: p.Organization.UUID, Name: p.Account.DisplayName, Email: p.Account.Email, Plan: p.plan()}
			s.st.Accounts = append(s.st.Accounts, a)
			added = true
			log.Printf("added account %s", a.label())
		}
		if a != nil {
			switch {
			case err == nil:
				a.Usage, a.Problem = u, ""
			case errors.Is(err, errSignedOut):
				a.Problem = "signed out, add it again"
			case limited != nil:
				// Logged above; the last numbers stay until the next try.
			default:
				log.Printf("usage %s: %v", a.label(), err)
			}
		}
		s.mu.Unlock()
	}

	lastChat := lastChatWrite(s.app().sessionsDir())
	s.mu.Lock()
	adding := !s.addingAt.IsZero()
	if added && adding {
		s.addingAt = time.Time{}
	} else if adding && time.Since(s.addingAt) > 15*time.Minute {
		s.addingAt, s.status = time.Time{}, ""
	}
	s.saveState()
	target, cause := s.autoTarget(desktopRunning(), lastChat, time.Now())
	s.mu.Unlock()
	s.onChange()

	switch {
	case added && adding:
		// A fresh sign-in: restart once so the chats of the other accounts show up.
		return "Importing chats", s.importChats
	case target != nil:
		return "Auto switch", func() error { return s.switchTo(target.UUID, cause) }
	}
	return "", nil
}

// autoTarget picks the account to switch to and says why: the active one is
// nearly full, or another one's week is about to reset mostly unused.
// lastChat is when a chat was last active. Callers hold s.mu.
func (s *switcher) autoTarget(running bool, lastChat, now time.Time) (*Account, switchCause) {
	cur := s.account(s.active)
	if !running || cur == nil || cur.Usage == nil || now.Sub(cur.Usage.CheckedAt) > activeFresh || now.Sub(s.st.LastSwitch) < autoCooldown {
		return nil, byHand
	}
	if s.st.AutoSwitch && cur.Usage.current(now).peak() >= s.st.Threshold {
		var best *Account
		for _, a := range s.st.Accounts {
			if s.usable(a, cur, now) && (best == nil || a.Usage.current(now).peak() < best.Usage.current(now).peak()) {
				best = a
			}
		}
		if best != nil {
			return best, nearlyFull
		}
	}
	// The active account keeps going while its own week is the one running out.
	if s.st.SpendWeekly && now.Sub(s.st.HandSwitch) >= handPause && now.Sub(lastChat) >= idleFor && !s.spendable(cur, now) {
		var best *Account
		for _, a := range s.st.Accounts {
			if s.usable(a, cur, now) && s.spendable(a, now) && (best == nil || a.Usage.WeeklyReset.Before(best.Usage.WeeklyReset)) {
				best = a
			}
		}
		if best != nil {
			return best, spendWeekly
		}
	}
	return nil, byHand
}

// usable says whether an auto switch may go to a: fresh numbers and room
// well below the threshold. Callers hold s.mu.
func (s *switcher) usable(a, cur *Account, now time.Time) bool {
	return a != cur && a.Usage != nil && a.Problem == "" && now.Sub(a.Usage.CheckedAt) <= idleFresh &&
		a.Usage.current(now).peak() < s.st.Threshold-autoHeadroom
}

// spendable says whether a's week should be used up first: it resets soon
// with much of it unused, or spend weekly already moved there and that week
// has not reset yet. a has usage. Callers hold s.mu.
func (s *switcher) spendable(a *Account, now time.Time) bool {
	if a.UUID == s.st.Spending && now.Before(s.st.SpendUntil) {
		return true
	}
	u := a.Usage.current(now)
	left := u.WeeklyReset.Sub(now)
	return !u.WeeklyReset.IsZero() && left > 0 && left <= expiringWithin && u.Weekly <= expiringMaxWeekly
}

// run does one app restart at a time and reports the outcome in the menu.
func (s *switcher) run(what string, f func() error) {
	if !s.busy.CompareAndSwap(false, true) {
		return
	}
	err := f()
	s.busy.Store(false)
	if err != nil {
		s.setStatus(fmt.Sprintf("%s failed: %v", what, err))
		return
	}
	s.mu.Lock()
	if s.addingAt.IsZero() {
		s.status = ""
	}
	s.mu.Unlock()
	s.onChange()
	s.refresh(context.Background())
}

// restart closes the app, runs change on its files and starts it again. If
// change fails, the app files are put back as they were.
func (s *switcher) restart(change func(active string) error) error {
	if err := quitDesktop(); err != nil {
		return err
	}
	s.mu.Lock()
	exe := s.st.AppExe
	s.mu.Unlock()
	defer func() {
		if err := startDesktop(exe); err != nil {
			s.setStatus("Could not start Claude: " + err.Error())
		}
	}()
	bk, err := backup(s.app(), s.backupsDir())
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	active, err := s.app().activeAccount()
	if err == nil {
		err = change(active)
	}
	if err != nil {
		if rerr := restoreBackup(s.app(), bk); rerr != nil {
			log.Printf("restore backup %s: %v", bk, rerr)
		}
	}
	return err
}

// saveActive keeps the signed-in account's latest login before it is replaced.
func (s *switcher) saveActive(active string) error {
	if active == "" {
		return nil
	}
	return saveProfile(s.app(), s.profileDir(active))
}

// switchTo signs the app in as uuid, for the reason in cause.
func (s *switcher) switchTo(uuid string, cause switchCause) error {
	s.mu.Lock()
	target := s.account(uuid)
	from := s.active
	s.mu.Unlock()
	if target == nil {
		return errors.New("unknown account")
	}
	switch cause {
	case nearlyFull:
		log.Printf("auto switch to %s: the active account is nearly full", target.label())
	case spendWeekly:
		log.Printf("auto switch to %s: its weekly usage resets soon", target.label())
	}
	if _, err := os.Stat(filepath.Join(s.profileDir(uuid), "config.json")); err != nil && uuid != s.snapshot().Active {
		return fmt.Errorf("no saved sign-in for %s, use Add account", target.label())
	}
	s.setStatus("Switching to " + target.label() + "…")
	err := s.restart(func(active string) error {
		if active == uuid {
			return nil
		}
		if err := s.saveActive(active); err != nil {
			return fmt.Errorf("save current sign-in: %w", err)
		}
		if err := restoreProfile(s.app(), s.profileDir(uuid)); err != nil {
			return fmt.Errorf("restore sign-in: %w", err)
		}
		n, err := syncSessions(s.app().sessionsDir(), target.UUID, target.Org)
		log.Printf("switched to %s, %d chats imported", target.label(), n)
		s.importProjects(target)
		return err
	})
	if err == nil {
		s.mu.Lock()
		s.st.LastSwitch = time.Now()
		s.st.LastCause, s.st.LastFrom = cause, from
		switch cause {
		case byHand:
			// The user's pick ends spending another account's week.
			s.st.HandSwitch = s.st.LastSwitch
			s.st.Spending, s.st.SpendUntil = "", time.Time{}
		case spendWeekly:
			if target.Usage != nil {
				s.st.Spending, s.st.SpendUntil = uuid, target.Usage.WeeklyReset
			}
		}
		s.active = uuid
		s.saveState()
		s.mu.Unlock()
	}
	return err
}

// addAccount keeps the current sign-in and opens the app on its login screen.
func (s *switcher) addAccount() error {
	s.setStatus("Sign in with the other account in Claude")
	err := s.restart(func(active string) error {
		if err := s.saveActive(active); err != nil {
			return err
		}
		return clearSignIn(s.app())
	})
	if err == nil {
		s.mu.Lock()
		s.addingAt = time.Now()
		s.mu.Unlock()
	}
	return err
}

// importChats makes the chats of all accounts visible in the signed-in one.
func (s *switcher) importChats() error {
	s.setStatus("Importing chats…")
	return s.restart(func(active string) error {
		s.mu.Lock()
		a := s.account(active)
		s.mu.Unlock()
		if a == nil {
			return errors.New("no signed-in account")
		}
		n, err := syncSessions(s.app().sessionsDir(), a.UUID, a.Org)
		log.Printf("%d chats imported into %s", n, a.label())
		s.importProjects(a)
		return err
	})
}

// importProjects copies the projects of all accounts into a. A failure only
// costs the project grouping, so it is logged and the switch goes on.
func (s *switcher) importProjects(a *Account) {
	n, err := syncSpaces(s.app().spacesDir(), a.UUID, a.Org, filepath.Join(s.dir, "spaces-seen.json"))
	if err != nil {
		log.Printf("projects not imported into %s: %v", a.label(), err)
		return
	}
	log.Printf("%d projects imported into %s", n, a.label())
}

func (s *switcher) setAuto(on bool) {
	s.mu.Lock()
	s.st.AutoSwitch = on
	s.saveState()
	s.mu.Unlock()
	s.onChange()
}

func (s *switcher) setSpendWeekly(on bool) {
	s.mu.Lock()
	s.st.SpendWeekly = on
	s.saveState()
	s.mu.Unlock()
	s.onChange()
}

func (s *switcher) setThreshold(t float64) {
	s.mu.Lock()
	s.st.Threshold = t
	s.saveState()
	s.mu.Unlock()
	s.onChange()
}

// snapshot is a copy of what the menu shows.
type snapshot struct {
	Accounts    []Account
	Active      string
	AutoSwitch  bool
	SpendWeekly bool
	Threshold   float64
	Status      string
	Note        string
	Busy        bool
	// Update is a version newer than this one, empty while there is none.
	Update string
}

func (s *switcher) snapshot() snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := snapshot{Active: s.active, AutoSwitch: s.st.AutoSwitch, SpendWeekly: s.st.SpendWeekly, Threshold: s.st.Threshold,
		Status: s.status, Note: s.autoNote(time.Now()), Busy: s.busy.Load()}
	if !s.st.NoUpdateCheck && newerVersion(s.st.LatestVersion, version) {
		snap.Update = s.st.LatestVersion
	}
	for _, a := range s.st.Accounts {
		snap.Accounts = append(snap.Accounts, *a)
	}
	return snap
}

// autoNote says why the last switch happened when the switcher made it on
// its own, for a while afterwards. Callers hold s.mu.
func (s *switcher) autoNote(now time.Time) string {
	if now.Sub(s.st.LastSwitch) > noteFor {
		return ""
	}
	at := resetText(s.st.LastSwitch, now)
	switch s.st.LastCause {
	case nearlyFull:
		from := "the last account"
		if a := s.account(s.st.LastFrom); a != nil {
			from = a.label()
		}
		return fmt.Sprintf("Auto-switched at %s, %s was nearly full", at, from)
	case spendWeekly:
		return fmt.Sprintf("Auto-switched at %s to use up this account's week before it resets %s", at, resetText(s.st.SpendUntil, now))
	}
	return ""
}

func (s *switcher) adding() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.addingAt.IsZero()
}
