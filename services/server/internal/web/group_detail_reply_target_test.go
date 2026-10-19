package web

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/flosch/pongo2/v6"
)

// socialThreadContext always stores its `var target map[string]any` in the
// context, even without a ?reply_to query. That typed nil map means no target;
// it must not be confused with a supplied malformed nonnil target row.
func TestNativeGroupDetailTypedNilReplyTargetKeepsOrdinaryWritableGET(t *testing.T) {
	for _, test := range []struct {
		name   string
		target any
	}{
		{"untyped-nil", nil},
		{"source-typed-nil-map", map[string]any(nil)},
		{"admitted-context-typed-nil-map", pongo2.Context(nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			renderer, request, data := groupDetailProjectionFixture(t)
			data["reply_target"] = test.target
			page, err := renderer.groupDetailPage(request, data)
			if err != nil || !page.CanPost || page.ComposeReplyTo != "" || page.ReplyAuthor != "" {
				t.Fatal("absent optional reply target broke ordinary admitted composer", err)
			}
			raw, err := renderer.renderGroupDetail(request, data)
			if err != nil || !bytes.Contains(raw, []byte(`id="compose" action="/groups/41/post/"`)) || !bytes.Contains(raw, []byte(`name="reply_to" id="id_reply_to" value=""`)) || bytes.Contains(raw, []byte(`compose-reply-note`)) {
				t.Fatal("ordinary writable GET lost its empty reply field or invented a reply", err)
			}
		})
	}
}

func TestNativeGroupDetailNilReplyTargetPreservesBoundPOSTAndRealGETTarget(t *testing.T) {
	t.Run("bound-POST-no-query-target-keeps-first-hidden-value", func(t *testing.T) {
		renderer, request, data := groupDetailProjectionFixture(t)
		request.Method = http.MethodPost
		request.PostForm = url.Values{"reply_to": {"61", "99"}}
		data["reply_target"] = map[string]any(nil)
		page, err := renderer.groupDetailPage(request, data)
		if err != nil || page.ComposeReplyTo != "61" || page.ReplyAuthor != "" || !page.CanPost {
			t.Fatal("absent optional target changed current bound first-value ordering", err)
		}
	})
	t.Run("actual-target-row-keeps-source-author-and-id", func(t *testing.T) {
		renderer, request, data := groupDetailProjectionFixture(t)
		data["reply_target"] = data["posts"].([]map[string]any)[0]
		page, err := renderer.groupDetailPage(request, data)
		if err != nil || page.ComposeReplyTo != "61" || page.ReplyAuthor != "Generated group owner" {
			t.Fatal("present authorized reply target lost source identity", err)
		}
		raw, err := renderer.renderGroupDetail(request, data)
		if err != nil || !bytes.Contains(raw, []byte(`name="reply_to" id="id_reply_to" value="61"`)) || !bytes.Contains(raw, []byte(`compose-reply-note`)) {
			t.Fatal("present authorized target lost actual reply form/note", err)
		}
	})
}

func TestNativeGroupDetailNonnilMalformedReplyTargetsStillRefuseBeforeOutput(t *testing.T) {
	for _, test := range []struct {
		name   string
		target any
	}{
		{"empty-map", map[string]any{}},
		{"wrong-type", "PRIVATE-reply-target"},
		{"missing-author", map[string]any{"id": int64(61)}},
		{"typed-nil-author-is-not-an-absent-target", map[string]any{"id": int64(61), "author": map[string]any(nil)}},
		{"missing-id", map[string]any{"author": map[string]any{"display_name": "Generated author"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			renderer, request, data := groupDetailProjectionFixture(t)
			data["reply_target"] = test.target
			raw, err := renderer.renderGroupDetail(request, data)
			if err == nil || len(raw) != 0 {
				t.Fatal("malformed nonnil target bypassed strict projection or leaked partial output", err)
			}
		})
	}
}
