package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResumeHTMLInsertsRequest(t *testing.T) {
	page, err := resumeHTML(resumeRequest{Account: "A", Wait: 30, Chats: []resumeChat{{Title: "</script><b>x", Folder: "app"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page, "Stripe-Zahlung") {
		t.Fatal("sample data left in the page")
	}
	if !strings.Contains(page, `const data = {"account":"A","wait":30,"chats":[{"title":`) || !strings.Contains(page, `"folder":"app"}]};`) {
		t.Fatal("request not inserted in place of the sample data")
	}
	if strings.Contains(page, "</script><b>x") {
		t.Fatal("a chat title could end the page's script")
	}
}

func TestResumeHTMLPreservesDialogLanguage(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN"} {
		t.Run(lang, func(t *testing.T) {
			page, err := resumeHTML(resumeRequest{Account: "A", Wait: 30, Chats: []resumeChat{{Title: "Work"}}, Language: lang})
			if err != nil {
				t.Fatal(err)
			}
			_, data, ok := strings.Cut(page, "const data = ")
			if !ok {
				t.Fatal("missing dialog data")
			}
			line, _, _ := strings.Cut(data, "\n")
			var payload struct {
				Language string `json:"language"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimSpace(line), ";")), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Language != lang {
				t.Fatalf("dialog language = %q, want %q", payload.Language, lang)
			}
		})
	}
}
