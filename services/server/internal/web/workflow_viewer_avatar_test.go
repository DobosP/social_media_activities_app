package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeWorkflowViewerAvatarKeepsExactGeneratedURI(t *testing.T) {
	const name = "web/activity_detail.html"
	r, request, data := workflowsFixture(t, name)
	avatar := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><text>~~~~~~</text></svg>`))
	if !strings.Contains(avatar, "+") {
		t.Fatal("viewer avatar fixture must exercise literal standard-base64 plus")
	}
	data["user"].(map[string]any)["avatar_uri"] = avatar
	before, _ := json.Marshal(data)
	page, err := r.workflowPage(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	if string(page.Avatar) != avatar || string(page.AvatarSource) != `src="`+avatar+`"` {
		t.Fatal("validated viewer avatar changed before template rendering")
	}
	raw, err := r.renderWorkflows(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `<img class="avatar nav-avatar" src="`+avatar+`"`) || strings.Contains(string(raw), "Generated <person>") {
		t.Fatal("literal viewer avatar or escaped viewer label lost")
	}
	after, _ := json.Marshal(data)
	if !bytes.Equal(before, after) {
		t.Fatal("viewer-avatar projection mutated the authorized source")
	}
}

func TestNativeWorkflowViewerAvatarRetainsOptionalImageOnlyBoundary(t *testing.T) {
	const name = "web/activity_detail.html"
	for _, absent := range []any{nil, ""} {
		r, request, data := workflowsFixture(t, name)
		data["user"].(map[string]any)["avatar_uri"] = absent
		page, err := r.workflowPage(request, name, data)
		if err != nil || page.Avatar != "" || page.AvatarSource != "" {
			t.Fatal("optional missing viewer avatar became required or fabricated", err)
		}
		raw, err := r.renderWorkflows(request, name, data)
		if err != nil || strings.Contains(string(raw), `class="avatar nav-avatar"`) {
			t.Fatal("missing optional viewer avatar produced a broken image", err)
		}
	}
	for _, invalid := range []any{
		true,
		"javascript:alert(1)",
		"https://untrusted.example/avatar.svg",
		"/api/media/file/unrelated/",
		"data:text/html;base64,PHNjcmlwdD4=",
		`data:image/svg+xml;base64,broken" onerror="alert(1)`,
		"data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<html>not SVG</html>`)),
		"data:image/svg+xml;base64," + strings.Repeat("A", 1<<20),
	} {
		r, request, data := workflowsFixture(t, name)
		data["user"].(map[string]any)["avatar_uri"] = invalid
		if raw, err := r.renderWorkflows(request, name, data); err == nil || len(raw) != 0 {
			t.Fatal("invalid viewer avatar escaped the existing image-only guard or wrote partial output")
		}
	}
}
