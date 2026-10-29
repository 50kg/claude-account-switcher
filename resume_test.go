package main

import (
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
