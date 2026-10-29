package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The dialog that asks which stopped chats go on is a web page in its own
// process: on macOS the tray already owns the main thread a web view needs,
// and a crash in the dialog cannot take the tray with it.

//go:embed resume.html
var resumePage string

// resumeFlag starts this program as the dialog. It reads a resumeRequest on
// stdin and writes the answers as a JSON array of booleans on stdout.
const resumeFlag = "-resume-dialog"

type resumeRequest struct {
	Account string       `json:"account"`
	Wait    int          `json:"wait"`
	Chats   []resumeChat `json:"chats"`
}

type resumeChat struct {
	Title  string `json:"title"`
	Folder string `json:"folder"`
}

// chooseResume asks which of the stopped chats should go on and returns one
// answer per chat. After wait without an answer all of them go on. Without
// a web view the platform's plain dialog asks instead.
func chooseResume(account string, chats []cutChat, wait time.Duration) []bool {
	req := resumeRequest{Account: account, Wait: int(wait / time.Second)}
	titles := make([]string, len(chats))
	for i, c := range chats {
		req.Chats = append(req.Chats, resumeChat{Title: c.title, Folder: c.folder})
		titles[i] = c.title
	}
	keep, err := askInDialogProcess(req, wait)
	if err == nil {
		return keep
	}
	log.Printf("resume dialog: %v, using the plain one", err)
	return nativeResume(titles, wait)
}

func askInDialogProcess(req resumeRequest, wait time.Duration) ([]bool, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	in, _ := json.Marshal(req)
	// The page counts down by itself; this only catches a dialog that hangs.
	ctx, cancel := context.WithTimeout(context.Background(), wait+time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, resumeFlag)
	cmd.Stdin = bytes.NewReader(in)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("no answer")
		}
		return nil, fmt.Errorf("%v %s", err, strings.TrimSpace(stderr.String()))
	}
	var keep []bool
	if err := json.Unmarshal(out.Bytes(), &keep); err != nil || len(keep) != len(req.Chats) {
		return nil, fmt.Errorf("unexpected answer %q", out.String())
	}
	return keep, nil
}

// runResumeDialog is the dialog process. Closing the window counts as
// Don't continue.
func runResumeDialog() int {
	var req resumeRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil || len(req.Chats) == 0 {
		fmt.Fprintln(os.Stderr, "bad request:", err)
		return 2
	}
	page, err := resumeHTML(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	keep, err := showWebDialog(page, len(req.Chats))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if keep == nil {
		keep = make([]bool, len(req.Chats))
	}
	out, _ := json.Marshal(keep)
	os.Stdout.Write(out)
	return 0
}

// resumeHTML puts the request into the page in place of its sample data.
// json.Marshal escapes <, > and &, so titles cannot end the script.
func resumeHTML(req resumeRequest) (string, error) {
	const slot = "/*DATA*/"
	start := strings.Index(resumePage, slot)
	if start < 0 {
		return "", errors.New("resume page has no data slot")
	}
	// The sample data has no semicolon of its own.
	end := strings.Index(resumePage[start:], ";")
	if end < 0 {
		return "", errors.New("resume page data slot is not closed")
	}
	data, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return resumePage[:start] + string(data) + resumePage[start+end:], nil
}
