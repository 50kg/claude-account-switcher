package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	repoURL = "https://github.com/SnlperStripes/claude-account-switcher"
	// GitHub is asked for the latest release at most this often.
	updateEvery = 24 * time.Hour
)

var latestAPI = "https://api.github.com/repos/SnlperStripes/claude-account-switcher/releases/latest"

// latestRelease asks GitHub for the version of the latest release, like "0.3.0".
// Nothing about the user or their accounts goes along.
func latestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "claude-account-switcher/"+version)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest release: %s", res.Status)
	}
	var body struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	v := strings.TrimPrefix(body.Tag, "v")
	if parseVersion(v) == nil {
		return "", fmt.Errorf("latest release: unexpected tag %q", body.Tag)
	}
	return v, nil
}

// parseVersion splits "1.2.3" into its numbers, nil when v is not a version.
func parseVersion(v string) []int {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		nums[i] = n
	}
	return nums
}

// newerVersion says whether version a comes after version b.
func newerVersion(a, b string) bool {
	x, y := parseVersion(a), parseVersion(b)
	if x == nil || y == nil {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// checkUpdate asks GitHub for the latest release when the check is on and
// the last answer is a day old. A failed check is tried again next time.
func (s *switcher) checkUpdate(ctx context.Context) {
	s.mu.Lock()
	due := !s.st.NoUpdateCheck && time.Since(s.st.UpdateChecked) >= updateEvery
	s.mu.Unlock()
	if !due {
		return
	}
	v, err := latestRelease(ctx)
	if err != nil {
		log.Printf("update check: %v", err)
		return
	}
	if newerVersion(v, version) {
		log.Printf("version %s is available", v)
	}
	s.mu.Lock()
	s.st.UpdateChecked, s.st.LatestVersion = time.Now(), v
	s.saveState()
	s.mu.Unlock()
	s.onChange()
}
