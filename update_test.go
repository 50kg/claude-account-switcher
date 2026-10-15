package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.3.0", "0.2.0", true},
		{"0.10.0", "0.9.9", true},
		{"1.0.0", "0.99.99", true},
		{"0.2.1", "0.2.0", true},
		{"0.2.0", "0.2.0", false},
		{"0.1.9", "0.2.0", false},
		{"", "0.2.0", false},
		{"0.3", "0.2.0", false},
		{"0.3.0-beta", "0.2.0", false},
	} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckUpdate(t *testing.T) {
	tag, calls := "v99.0.0", 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}))
	defer srv.Close()
	defer func(old string) { latestAPI = old }(latestAPI)
	latestAPI = srv.URL

	s := &switcher{dir: t.TempDir(), onChange: func() {}}
	s.checkUpdate(context.Background())
	if got := s.snapshot().Update; got != "99.0.0" {
		t.Fatalf("update = %q, want 99.0.0", got)
	}
	s.checkUpdate(context.Background())
	if calls != 1 {
		t.Fatalf("asked GitHub %d times within a day", calls)
	}

	s.st.NoUpdateCheck = true
	if got := s.snapshot().Update; got != "" {
		t.Fatalf("update shown with the check off: %q", got)
	}
	s.st.UpdateChecked = time.Time{}
	s.checkUpdate(context.Background())
	if calls != 1 {
		t.Fatal("asked GitHub with the check off")
	}

	tag = "v" + version
	s.st.NoUpdateCheck = false
	s.checkUpdate(context.Background())
	if got := s.snapshot().Update; calls != 2 || got != "" {
		t.Fatalf("after turning the check on: %d calls, update %q", calls, got)
	}

	tag = "nightly"
	s.st.UpdateChecked = time.Time{}
	s.checkUpdate(context.Background())
	if !s.st.UpdateChecked.IsZero() {
		t.Fatal("an odd tag counted as a check")
	}
}
