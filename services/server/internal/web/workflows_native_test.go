package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func workflowsFixture(t *testing.T, name string) (*Renderer, *http.Request, pongo2.Context) {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	actor := platform.Actor{ID: 981, PublicID: "55555555-5555-4555-8555-555555555981", Username: "generated-workflow", DisplayName: "Generated <person>", Cohort: "adult", AgeBand: "adult", IsActive: true}
	path := map[string]string{"web/activity_form.html": "/activities/new/", "web/activity_edit.html": "/activities/29/edit/", "web/activity_detail.html": "/activities/29/", "web/activity_safe_exit.html": "/activities/29/", "web/place_propose.html": "/places/propose/", "web/place_claim.html": "/places/31/claim/", "web/places_pending.html": "/places/pending/", "web/messages.html": "/messages/"}[name]
	request := platform.WithActor(httptest.NewRequest("GET", "https://workflow.example"+path, nil), actor)
	data := pongo2.Context{"language": "en", "display_theme": "auto", "display_text": "normal", "display_motion": "auto", "site_name": "Generated workflow site", "canonical_url": path, "csrf": "synthetic-workflow-csrf", "request": map[string]any{"path": path, "csp_nonce": "synthetic-workflow-nonce"}, "user": socialActor(actor), "connections_enabled": false, "PRIVATE_unused": "PRIVATE-workflow-source"}
	avatar := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	user := map[string]any{"id": actor.ID, "public_id": actor.PublicID, "username": actor.Username, "display_name": actor.DisplayName, "avatar_uri": avatar}
	stamp := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	activity := map[string]any{"id": 29, "pk": 29, "title": "Supplied <activity>", "description": "First paragraph\n\nSecond paragraph", "cohort": "adult", "status": "open", "get_status_display": "Open", "cost_band": "free", "get_cost_band_display": "Free", "cost_amount": "0.00", "cost_note": "Supplied cost note", "difficulty": "unspecified", "starts_at": stamp, "ends_at": stamp.Add(time.Hour), "beginners_welcome": true, "guardian_accompanied": false, "is_publicly_listed": false, "first_time_note": "Supplied first-time note", "meeting_point": "Supplied meeting point", "what_to_bring": "Supplied water", "organizer_note": "Supplied organizer note", "activity_type": map[string]any{"id": 7, "name": "Court", "slug": "court"}, "secondary_types": map[string]any{"all": []map[string]any{}}, "place": map[string]any{"id": 31, "name": "Supplied place", "display_name": "Supplied place", "address_city": "Supplied city"}, "owner": user, "thread": map[string]any{"id": 41}}
	vocab := []map[string]any{{"id": 7, "name": "Court", "slug": "court", "category": "Outside", "aliases": []any{"Racquet", "<script>vocab</script>"}}}
	switch name {
	case "web/activity_form.html":
		data["form"] = workflowFormTestSnapshot("ActivityForm", actor)
		data["type_vocabulary"] = vocab
	case "web/activity_edit.html":
		data["activity"] = activity
		data["form"] = workflowFormTestSnapshot("ActivityEditForm", actor)
		repairWorkflowSnapshot(request, data["form"].(map[string]any), map[string]any{"activity_type": "7"}, true)
		data["type_vocabulary"] = vocab
	case "web/activity_safe_exit.html":
		data["activity"] = map[string]any{"id": 29, "pk": 29, "title": "Supplied <activity>", "owner": "PRIVATE-owner", "meeting_point": "PRIVATE-logistics"}
		data["is_member"] = true
		data["is_owner"] = false
		data["safe_exit_only"] = true
		data["my_guardians"] = []map[string]any{{"id": 1, "username": "Supplied guardian", "display_name": ""}}
	case "web/activity_detail.html":
		data["activity"] = activity
		data["is_member"] = true
		data["is_owner"] = true
		data["is_organizer"] = true
		data["can_manage_organizers"] = true
		data["is_open"] = true
		data["is_completed"] = false
		data["can_create_group"] = true
		data["my_membership"] = map[string]any{"id": 2, "role": "owner", "state": "member", "attendance_intent": "unknown"}
		data["members"] = []map[string]any{{"id": 2, "role": "owner", "state": "member", "user": user}}
		data["pending"] = []map[string]any{}
		data["my_guardians"] = []map[string]any{}
		data["conn_related_ids"] = []int64{}
		data["meetup_brief"] = []any{[]string{"Supplied label", "Supplied brief"}}
		data["rsvp_summary"] = map[string]any{"going": 2, "total": 3, "min_to_go": 3, "remaining_needed": 1, "met_minimum": false}
		data["met_summary"] = map[string]any{"total": 3, "confirmed": 1}
		data["share_kind"] = "activity"
		data["share_obj_id"] = 29
		data["share_targets"] = []map[string]any{{"pk": 59, "title": "Supplied share target", "starts_at": stamp}}
		data["announcements"] = []map[string]any{}
		data["posts"] = []map[string]any{}
		data["photos"] = []map[string]any{}
		data["has_older"] = false
		data["post_form"] = workflowFormTestSnapshot("PostForm", actor)
		repairWorkflowSnapshot(request, data["post_form"].(map[string]any), nil, false)
		data["reaction_emojis"] = socialFacets()
		data["show_dissent_concern"] = true
		data["thread_audience"] = map[string]any{"is_group": false, "peer_count": 2}
		data["media_capabilities_supplied"] = true
		data["attachments_enabled"] = false
		data["file_enabled"] = false
		data["video_enabled"] = false
		data["attachment_accept"] = ""
		data["ephemeral_options"] = []any{}
		data["video_max_seconds"] = 90
		i18n := map[string]string{}
		for _, key := range []string{"reply", "react", "edited", "replyingTo", "messageSent", "newAnnouncement", "newMessages", "livePaused", "typingOne", "typingTwo", "typingMany", "justNow", "sharedImage", "videoProcessing", "videoFailed", "attachmentExpired", "attachmentDisappears", "attachmentBlocked", "pdfDownloads"} {
			i18n[key] = "Generated " + key
		}
		data["thread_chat_config"] = map[string]any{"threadId": 41, "meId": actor.ID, "reactUrlTemplate": routeURL("activity_post_react", 29, int64(987654321)), "reactionFacets": socialFacets(), "i18n": i18n}
		data["presend_nudge"] = map[string]any{"rules": socialPresendRules, "message": "Supplied safety nudge"}
	case "web/place_propose.html":
		data["form"] = workflowFormTestSnapshot("PlaceProposeForm", actor)
		data["return_to"] = "organize"
	case "web/place_claim.html":
		data["form"] = workflowFormTestSnapshot("PlaceClaimForm", actor)
		data["place"] = activity["place"]
	case "web/places_pending.html":
		data["proposals"] = []map[string]any{{"id": int64(9007199254740993), "place": map[string]any{"id": 31, "name": "", "address_city": "Supplied city"}, "confirmations_count": 1, "required_confirmations": 3}}
	case "web/messages.html":
		me := map[string]any{"public_id": actor.PublicID, "username": actor.Username, "display_name": actor.DisplayName, "avatar": avatar}
		data["messaging_config"] = map[string]any{"me": me, "connections": []map[string]any{}, "reaction_emojis": []string{"👍", "❤️", "🎉", "👏", "🙏"}}
		data["can_create_group"] = false
	}
	return NewRenderer(root), request, data
}

func TestNativeWorkflowsAllDestinationsAndBrowserHooks(t *testing.T) {
	for _, name := range []string{"web/activity_form.html", "web/activity_edit.html", "web/activity_detail.html", "web/activity_safe_exit.html", "web/place_propose.html", "web/place_claim.html", "web/places_pending.html", "web/messages.html"} {
		t.Run(name, func(t *testing.T) {
			r, request, data := workflowsFixture(t, name)
			before, _ := json.Marshal(data)
			raw, err := r.renderWorkflows(request, name, data)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(data)
			if !bytes.Equal(before, after) {
				t.Fatal("mutated producer snapshot")
			}
			body := string(raw)
			if !strings.Contains(body, `/static/css/workflows-native.css`) || strings.Contains(body, "PRIVATE-workflow-source") || strings.Contains(body, "<activity>") {
				t.Fatal("shell, escaping or selected projection lost")
			}
			switch name {
			case "web/activity_form.html", "web/activity_edit.html":
				for _, hook := range []string{`data-wizard`, `data-wizard-panel`, `id="place-picker"`, `id="map"`, `data-places-url="/api/places/"`, `id="activity-type-vocabulary"`, `/static/vendor/leaflet/leaflet.js`, `/static/js/place-picker.js`, `/static/js/concept-combobox.js`, `/static/js/form-wizard.js`, `data-combobox="multiple"`, `name="csrfmiddlewaretoken"`} {
					if !strings.Contains(body, hook) {
						t.Fatalf("lost hook %s", hook)
					}
				}
				if strings.Contains(body, "<script>vocab") {
					t.Fatal("JSON script escaped incorrectly")
				}
			case "web/activity_detail.html":
				nudge := strings.Index(body, `src="/static/js/presend-nudge.js"`)
				chat := strings.Index(body, `src="/static/js/thread-chat.js"`)
				if nudge < 0 || chat < nudge {
					t.Fatal("safety nudge must register before live send")
				}
				for _, hook := range []string{`id="thread-list"`, `id="compose"`, `id="thread-jump"`, `id="typing-indicator"`, `id="live-status"`, `name="intent" value="going"`, `name="listed" value="1"`, `name="target"`, `name="reason" maxlength="200"`} {
					if !strings.Contains(body, hook) {
						t.Fatalf("lost activity control %s", hook)
					}
				}
				if strings.Contains(body, `name="attachment"`) {
					t.Fatal("disabled attachment capability rendered")
				}
			case "web/activity_safe_exit.html":
				for _, private := range []string{"PRIVATE-owner", "PRIVATE-logistics", "id=\"thread-list\"", "name=\"attachment\"", "Supplied place"} {
					if strings.Contains(body, private) {
						t.Fatalf("safe exit leaked %s", private)
					}
				}
				if !strings.Contains(body, `action="`+routeURL("activity_unsafe", 29)+`"`) || !strings.Contains(body, `action="`+routeURL("activity_leave", 29)+`"`) {
					t.Fatal("safe exit actions missing")
				}
			case "web/place_propose.html":
				if !strings.Contains(body, `name="return_to" value="organize"`) || !strings.Contains(body, `name="allow_nearby"`) {
					t.Fatal("proposal retry controls missing")
				}
			case "web/place_claim.html":
				for _, field := range []string{"org_name", "kind", "official_website", "contact_email", "cui", "evidence"} {
					if !strings.Contains(body, `name="`+field+`"`) {
						t.Fatalf("claim field %s missing", field)
					}
				}
			case "web/places_pending.html":
				if !strings.Contains(body, routeURL("place_confirm", int64(9007199254740993))) || !strings.Contains(body, "Unnamed place") || !strings.Contains(body, "1 of 3 confirmations.") {
					t.Fatal("proposal identity/fallback/count changed")
				}
			case "web/messages.html":
				for _, hook := range []string{`id="mz-config"`, `id="mz-new-form"`, `name="usernames"`, `name="kind" value="direct"`, `id="mz-conversations"`, `id="mz-guardian-section"`, `id="mz-backup"`, `id="mz-composer"`, `id="mz-msg-search"`, `id="mz-timer"`, `src="/static/js/e2ee-messaging.js"`} {
					if !strings.Contains(body, hook) {
						t.Fatalf("encrypted client hook %s missing", hook)
					}
				}
				if strings.Contains(body, `href="/groups/new/"`) {
					t.Fatal("disabled standing group control rendered")
				}
			}
		})
	}
}

func TestNativeWorkflowsActivityActionsMentionsAndMedia(t *testing.T) {
	name := "web/activity_detail.html"
	r, request, data := workflowsFixture(t, name)
	actor, _ := platform.ActorFrom(request)
	avatar := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	body := "@generated-workflow <script>post</script>"
	post := map[string]any{"id": 61, "pk": 61, "author_id": actor.ID, "author": map[string]any{"id": actor.ID, "username": actor.Username, "display_name": actor.DisplayName, "public_id": actor.PublicID, "avatar_uri": avatar}, "created_at": time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC), "body": body, "body_html": pongo2.AsSafeValue(BodyMarkup(body, map[string]bool{actor.Username: true}, true)), "workflow_body_mentions": []string{actor.Username}, "is_edited": true, "dissent_mine": false, "concern_mine": false, "sentiment_lines": []string{"Supplied sentiment"}, "reaction_mine": []string{"helped_me"}, "replies": map[string]any{"all": []map[string]any{}}, "attachment_list": []map[string]any{{"kind": "image", "blocked": false, "processing": false, "failed": false, "expired": false, "original_filename": "", "url": "/api/media/attachments/71/", "thumb_url": "/api/media/attachments/71/thumb/"}}}
	data["posts"] = []map[string]any{post}
	data["attachments_enabled"] = true
	data["file_enabled"] = true
	data["video_enabled"] = true
	data["attachment_accept"] = "image/png,image/jpeg,image/webp,application/pdf,video/mp4,video/quicktime,video/webm"
	data["ephemeral_options"] = []any{[]string{"3600", "1 hour"}}
	data["photos"] = []map[string]any{{"url": "/api/media/photos/81/", "thumb_url": "/api/media/photos/81/thumb/"}}
	raw, err := r.renderWorkflows(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{`<span class="mention">@generated-workflow</span>`, `&lt;script&gt;post&lt;/script&gt;`, routeURL("activity_post_react", 29, 61), routeURL("activity_post_edit", 29, 61), routeURL("activity_post_delete", 29, 61), `name="attachment"`, `name="disappear"`, `value="3600"`, `name="image"`, `/api/media/photos/81/thumb/`, `name="csrfmiddlewaretoken"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("lost post/media control %s", want)
		}
	}
	if strings.Contains(html, "<script>post") || strings.Contains(html, "/groups/29/") {
		t.Fatal("unsafe body or incorrect group destinations")
	}
	post["body_html"] = pongo2.AsSafeValue(`<img src=x onerror=payload>`)
	if raw, err = r.renderWorkflows(request, name, data); err == nil || len(raw) != 0 {
		t.Fatal("unverified safeHTML admitted")
	}
}

func TestNativeWorkflowsCapabilitiesAndCopiedMessagingConfig(t *testing.T) {
	name := "web/activity_detail.html"
	r, request, data := workflowsFixture(t, name)
	data["is_member"] = false
	data["is_owner"] = false
	data["is_organizer"] = false
	data["can_manage_organizers"] = false
	data["can_create_group"] = false
	data["my_membership"] = nil
	data["can_join"] = true
	data["members"] = []map[string]any{}
	data["share_targets"] = []map[string]any{}
	raw, err := r.renderWorkflows(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, private := range []string{"Supplied first-time note", "Supplied meeting point", "Supplied water", "Supplied organizer note", `id="compose"`, `id="thread-list"`, `thread-chat.js`} {
		if strings.Contains(html, private) {
			t.Fatalf("nonmember control/detail rendered %s", private)
		}
	}
	if !strings.Contains(html, routeURL("activity_join", 29)) {
		t.Fatal("supplied can_join lost")
	}
	name = "web/messages.html"
	r, request, data = workflowsFixture(t, name)
	config := data["messaging_config"].(map[string]any)
	config["connections"] = []map[string]any{{"public_id": "55555555-5555-4555-8555-555555555982", "username": "<script>connection</script>", "display_name": "<img src=x>", "avatar": config["me"].(map[string]any)["avatar"]}}
	page, err := r.workflowPage(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page.MessagingJSON), "<script>") || !strings.Contains(string(page.MessagingJSON), `\u003cscript\u003e`) {
		t.Fatal("configuration is not safe JSON data")
	}
	config["connections"].([]map[string]any)[0]["username"] = "changed"
	if strings.Contains(string(page.MessagingJSON), "changed") {
		t.Fatal("retained mutable config")
	}
}

func TestNativeWorkflowsRejectMalformedBeforeOutput(t *testing.T) {
	for key, mutate := range map[string]func(pongo2.Context){"identity": func(d pongo2.Context) { d["user"].(map[string]any)["id"] = 2 }, "form-type": func(d pongo2.Context) { d["form"] = "safe legacy HTML" }, "vocabulary-type": func(d pongo2.Context) { d["type_vocabulary"] = []any{false} }, "request-path": func(d pongo2.Context) { d["request"].(map[string]any)["path"] = "/foreign/" }, "unsafe-canonical": func(d pongo2.Context) { d["canonical_url"] = "javascript:payload" }, "csrf-type": func(d pongo2.Context) { d["csrf"] = true }} {
		t.Run(key, func(t *testing.T) {
			r, request, data := workflowsFixture(t, "web/activity_form.html")
			mutate(data)
			if raw, err := r.renderWorkflows(request, "web/activity_form.html", data); err == nil || len(raw) != 0 {
				t.Fatal("malformed snapshot produced bytes")
			}
		})
	}
	r, request, data := workflowsFixture(t, "web/messages.html")
	data["messaging_config"].(map[string]any)["me"].(map[string]any)["public_id"] = "other"
	if raw, err := r.renderWorkflows(request, "web/messages.html", data); err == nil || len(raw) != 0 {
		t.Fatal("foreign messaging identity admitted")
	}
	r, request, data = workflowsFixture(t, "web/activity_detail.html")
	data["thread_chat_config"].(map[string]any)["meId"] = 2
	if raw, err := r.renderWorkflows(request, "web/activity_detail.html", data); err == nil || len(raw) != 0 {
		t.Fatal("foreign thread identity admitted")
	}
	if raw, err := r.renderWorkflows(httptest.NewRequest("GET", "/", nil), "web/messages.html", data); !errors.Is(err, platform.ErrForbidden) || len(raw) != 0 {
		t.Fatal("anonymous direct render admitted")
	}
	if raw, err := r.renderWorkflows(request, "web/saved_searches.html", data); !errors.Is(err, errWorkflowSnapshot) || len(raw) != 0 {
		t.Fatal("unselected SPA page admitted")
	}
}

func TestNativeWorkflowsCachedConcurrencyAndFailedExecution(t *testing.T) {
	first, err := loadWorkflowTemplates()
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadWorkflowTemplates()
	if err != nil || first != second {
		t.Fatal("template cache changed")
	}
	r, request, data := workflowsFixture(t, "web/messages.html")
	page, err := r.workflowPage(request, "web/messages.html", data)
	if err != nil {
		t.Fatal(err)
	}
	want, err := executeWorkflowTemplate(first, page)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			got, err := executeWorkflowTemplate(first, page)
			if err != nil {
				failures <- err
			} else if !bytes.Equal(got, want) {
				failures <- errors.New("concurrent bytes changed")
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	broken, err := template.New("workflows").Parse(`{{define "workflows"}}partial{{.MissingField}}{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := executeWorkflowTemplate(broken, page); err == nil || len(got) != 0 {
		t.Fatal("partial bytes returned after execution failure")
	}
}

func TestNativeWorkflowsRendererDispatchKeepsCompatibilityFallback(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "apps/web/templates/web")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow-sentinel.html"), []byte("<p>compatibility sentinel</p>"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"web/activity_form.html", "web/activity_edit.html", "web/activity_detail.html", "web/activity_safe_exit.html", "web/place_propose.html", "web/place_claim.html", "web/places_pending.html", "web/messages.html"} {
		_, request, data := workflowsFixture(t, name)
		delete(data, "request")
		r := NewRenderer(root)
		response := httptest.NewRecorder()
		if err := r.Render(response, request, name, data); err != nil {
			t.Fatal(name, err)
		}
		if !strings.Contains(response.Body.String(), "workflows-native.css") || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.Header().Get("Cache-Control") != "private, no-cache" {
			t.Fatal("selected dispatch/header lost", name)
		}
	}
	_, request, data := workflowsFixture(t, "web/messages.html")
	response := httptest.NewRecorder()
	if err := NewRenderer(root).Render(response, request, "web/workflow-sentinel.html", data); err != nil {
		t.Fatal(err)
	}
	if response.Body.String() != "<p>compatibility sentinel</p>" {
		t.Fatal("unselected compatibility dispatch changed")
	}
}

func TestNativeWorkflowsEnglishPluralCountRendering(t *testing.T) {
	for _, n := range []int{1, 2} {
		name := "web/activity_detail.html"
		r, request, data := workflowsFixture(t, name)
		actor, _ := platform.ActorFrom(request)
		rows := []map[string]any{}
		for i := 0; i < n; i++ {
			rows = append(rows, map[string]any{"id": 101 + i, "author_id": actor.ID, "author": socialActor(actor), "body": "Supplied search result", "created_at": time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC)})
		}
		data["digest"] = map[string]any{"has_content": true, "member_count": n, "going": 1, "total": 2, "announcements": []map[string]any{}, "logistical": []map[string]any{}, "recent": rows[:1]}
		data["thread_query"] = "needle"
		data["thread_results"] = rows
		data["thread_audience"].(map[string]any)["peer_count"] = n
		raw, err := r.renderWorkflows(request, name, data)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		want := []string{"1 of 2 coming &middot; 1 member.", "1 message matches.", "1 other person can see this."}
		if n == 2 {
			want = []string{"1 of 2 coming &middot; 2 members.", "2 messages match.", "2 other people can see this."}
		}
		for _, text := range want {
			if !strings.Contains(body, text) {
				t.Fatalf("English count %d lost exact plural arm %q", n, text)
			}
		}
	}
}
