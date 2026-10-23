package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func useManualUpdates(t *testing.T, url string) {
	t.Helper()
	old := manualUpdateURL
	t.Cleanup(func() { manualUpdateURL = old })
	manualUpdateURL = url
}

func TestChineseReleaseUsesManualUpdates(t *testing.T) {
	const page = "https://github.com/50kg/claude-account-switcher/releases/latest"
	useManualUpdates(t, page)
	useLanguage(t, "zh-CN")
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	defer srv.Close()
	oldAPI, oldReleases := latestAPI, releasesURL
	t.Cleanup(func() { latestAPI, releasesURL = oldAPI, oldReleases })
	latestAPI, releasesURL = srv.URL, srv.URL
	s := &switcher{dir: t.TempDir(), onChange: func() {}}
	s.st.LatestVersion = "99.0.0" // State saved by an older upstream build.
	s.checkUpdate(context.Background())
	if got := s.snapshot().Update; got != "" {
		t.Fatalf("manual edition advertises an upstream update: %q", got)
	}
	if title, _ := updateMenuText(s.snapshot().Update); title != "下载中文版更新…" {
		t.Fatalf("manual update menu = %q", title)
	}
	var opened []string
	s.requestUpdate(context.Background(), func(url string) { opened = append(opened, url) })
	// Even a direct call must not reach the binary downloader or launcher.
	s.installUpdate(context.Background(), "99.0.0")
	if len(opened) != 1 || opened[0] != page {
		t.Fatalf("opened pages = %v, want only %s", opened, page)
	}
	if calls != 0 || !s.st.UpdateChecked.IsZero() {
		t.Fatalf("manual mode contacted the updater: %d requests", calls)
	}
}

func TestStandardUpdateMenu(t *testing.T) {
	useManualUpdates(t, "")
	useLanguage(t, "en")
	if title, _ := updateMenuText(""); title != "" {
		t.Fatalf("update shown without a newer release: %q", title)
	}
	if title, _ := updateMenuText("99.0.0"); title != "Version 99.0.0 is out, click to update" {
		t.Fatalf("standard update menu = %q", title)
	}
}
