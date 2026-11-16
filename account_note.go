package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

const accountNoteMaxLength = 160

func validAccountNote(note string) bool {
	return utf8.ValidString(note) && utf8.RuneCountInString(note) <= accountNoteMaxLength && !strings.ContainsFunc(note, unicode.IsControl)
}

func (s *switcher) setAccountNote(id, note string) error {
	if !validAccountNote(note) {
		return errors.New(tr("Use a single line of up to 160 characters", "请输入不超过160个字符的单行备注"))
	}
	s.mu.Lock()
	if s.busy.Load() || s.usageSyncing.Load() {
		s.mu.Unlock()
		return errors.New(tr("Wait for the current operation to finish", "请等待当前操作完成"))
	}
	a := s.account(id)
	if a == nil {
		s.mu.Unlock()
		return errors.New(tr("Account no longer exists", "该账号已不存在"))
	}
	previous := a.Note
	a.Note = strings.TrimSpace(note)
	data, err := json.MarshalIndent(s.st, "", "  ")
	if err == nil {
		err = writeFileAtomic(s.statePath(), data)
	}
	if err != nil {
		a.Note = previous
		_ = os.Remove(s.statePath() + ".switcher-tmp")
	}
	s.mu.Unlock()
	if err == nil {
		s.onChange()
	}
	return err
}
