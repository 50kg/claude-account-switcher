package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	repoURL = "https://github.com/50kg/claude-account-switcher"
	// GitHub is asked for the latest release at most this often.
	updateEvery = 24 * time.Hour
	// The one file each release carries for this program, next to its .sha256.
	releaseAsset = "claude-account-switcher-windows-amd64.exe"
)

var latestAPI = "https://api.github.com/repos/50kg/claude-account-switcher/releases/latest"

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
	if _, err := s.fetchLatest(ctx); err != nil {
		log.Printf("update check: %v", err)
	}
}

// fetchLatest asks GitHub now and remembers the answer.
func (s *switcher) fetchLatest(ctx context.Context) (string, error) {
	v, err := latestRelease(ctx)
	if err != nil {
		return "", err
	}
	if newerVersion(v, version) {
		log.Printf("version %s is available", v)
	}
	s.mu.Lock()
	s.st.UpdateChecked, s.st.LatestVersion = time.Now(), v
	s.saveState()
	s.mu.Unlock()
	s.onChange()
	return v, nil
}

// updateMenuText is the Settings entry: an offer once a newer release is
// known, otherwise a plain check.
func updateMenuText(v string) string {
	if v != "" {
		return fmt.Sprintf(tr("Version %s is out, click to update", "发现新版本 %s，点击更新"), v)
	}
	return tr("Check for updates", "检查更新")
}

// requestUpdate is the Settings click. It installs a release already known
// to be newer; otherwise it asks GitHub at once, even with the daily check
// off, and installs what it finds.
func (s *switcher) requestUpdate(ctx context.Context) {
	v := s.snapshot().Update
	if v == "" {
		s.setStatus(tr("Checking for updates…", "正在检查更新…"))
		latest, err := s.fetchLatest(ctx)
		switch {
		case err != nil:
			s.setStatus(tr("Update check failed: ", "检查更新失败：") + err.Error())
			return
		case !newerVersion(latest, version):
			s.setStatus(fmt.Sprintf(tr("Version %s is the latest", "当前版本 %s 已是最新"), version))
			return
		}
		v = latest
	}
	s.installUpdate(ctx, v)
}

var (
	releasesURL = repoURL + "/releases"
	downloader  = &http.Client{Timeout: 5 * time.Minute}
	updating    atomic.Bool
)

// installUpdate puts version v in place of the running file and starts it,
// and the new process then asks this one to quit. A file the switcher
// downloads itself carries no mark of the web, so SmartScreen stays quiet.
// When that does not work, the download page opens instead.
func (s *switcher) installUpdate(ctx context.Context, v string) {
	if !updating.CompareAndSwap(false, true) {
		return
	}
	defer updating.Store(false)
	s.setStatus(fmt.Sprintf(tr("Downloading version %s…", "正在下载版本 %s…"), v))
	err := func() error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil {
			return err
		}
		bin, err := fetchRelease(ctx, v)
		if err != nil {
			return err
		}
		if err := replaceExe(exe, bin); err != nil {
			return err
		}
		if err := launch(exe); err != nil {
			restoreExe(exe)
			return err
		}
		return nil
	}()
	if err != nil {
		s.setStatus(tr("Update failed: ", "更新失败：") + err.Error())
		shellOpen(releasesURL + "/latest")
		return
	}
	s.setStatus(fmt.Sprintf(tr("Starting version %s…", "正在启动版本 %s…"), v))
}

// fetchRelease downloads the exe of version v and checks it against the
// checksum published next to it.
func fetchRelease(ctx context.Context, v string) ([]byte, error) {
	url := releasesURL + "/download/v" + v + "/" + releaseAsset
	bin, err := download(ctx, url, 64<<20)
	if err != nil {
		return nil, err
	}
	sum, err := download(ctx, url+".sha256", 1<<10)
	if err != nil {
		return nil, err
	}
	want, _, _ := strings.Cut(strings.TrimSpace(string(sum)), " ")
	got := sha256.Sum256(bin)
	if !strings.EqualFold(want, hex.EncodeToString(got[:])) {
		return nil, errors.New(tr("the download does not match its checksum", "下载文件的校验值不匹配"))
	}
	return bin, nil
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "claude-account-switcher/"+version)
	res, err := downloader.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(tr("download: %s", "下载失败：%s"), res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New(tr("download: file too large", "下载失败：文件过大"))
	}
	return data, nil
}

// replaceExe writes bin in place of exe and keeps the old file as exe.old.
// Windows cannot overwrite a running program, but it can rename it.
func replaceExe(exe string, bin []byte) error {
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	_ = os.Remove(exe + ".old")
	if err := os.Rename(exe, exe+".old"); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		restoreExe(exe)
		os.Remove(tmp)
		return err
	}
	return nil
}

// restoreExe puts exe.old back when the new file cannot be started.
func restoreExe(exe string) {
	_ = os.Remove(exe)
	if err := os.Rename(exe+".old", exe); err != nil {
		log.Printf("restore %s: %v", exe, err)
	}
}

// removeReplaced deletes the file an update left behind once the old process,
// which still runs from it for a moment, has quit.
func removeReplaced() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return
	}
	old := exe + ".old"
	for range 20 {
		if err := os.Remove(old); err == nil || errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}
