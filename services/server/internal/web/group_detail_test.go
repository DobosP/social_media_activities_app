package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/avatars"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func groupDetailFixturePerson(id int64, name string) map[string]any {
	return map[string]any{"id": id, "display_name": name, "username": name, "public_id": "00000000-0000-4000-8000-000000000012", "avatar_uri": avatars.DataURI(avatars.ActivityAccentSVG("group-detail-fixture"))}
}
func groupDetailFixturePost(id int64, author map[string]any, body, cohort string) map[string]any {
	return map[string]any{"id": id, "author_id": author["id"], "author": author, "body": body, "body_html": pongo2.AsSafeValue(BodyMarkup(body, map[string]bool{}, cohort == "adult")), "created_at": time.Date(2026, 10, 19, 12, 30, 0, 0, time.UTC), "is_edited": true, "dissent_mine": false, "concern_mine": false, "reaction_mine": []any{"helped_me"}, "sentiment_lines": []string{"A few people felt welcome."}, "replies": map[string]any{"all": []map[string]any{}}, "attachment_list": []map[string]any{}}
}
func groupDetailFixtureConfig(group, thread, viewer int64) map[string]any {
	i18n := map[string]string{}
	for _, key := range []string{"reply", "react", "edited", "replyingTo", "messageSent", "newAnnouncement", "newMessages", "livePaused", "typingOne", "typingTwo", "typingMany", "justNow", "sharedImage", "videoProcessing", "videoFailed", "attachmentExpired", "attachmentDisappears", "attachmentBlocked", "pdfDownloads"} {
		i18n[key] = "synthetic " + key
	}
	return map[string]any{"threadId": thread, "meId": viewer, "reactUrlTemplate": routeURL("group_post_react", group, int64(987654321)), "reactionFacets": socialFacets(), "i18n": i18n}
}
func groupDetailProjectionFixture(t testing.TB) (*Renderer, *http.Request, pongo2.Context) {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	actor := platform.Actor{ID: 11, Username: "generated-group-owner", DisplayName: "Generated group owner", PublicID: "00000000-0000-4000-8000-000000000011", AgeBand: "adult", Cohort: "adult", Role: "user", IsActive: true, IdentityVerified: true}
	request := platform.WithActor(httptest.NewRequest("GET", "https://fixture.invalid/groups/41/", nil), actor)
	owner, other := groupDetailFixturePerson(actor.ID, actor.DisplayName), groupDetailFixturePerson(12, "Generated member")
	post := groupDetailFixturePost(61, owner, "**Current body** @member https://fixture.invalid/shared\n<script>escaped</script>", "adult")
	post["snippet"] = map[string]any{"pk": int64(58), "author": "Generated old author", "text": "(message removed)"}
	post["replies"] = map[string]any{"all": []map[string]any{groupDetailFixturePost(62, other, "Current reply", "adult")}}
	post["share"] = map[string]any{"kind": "place", "obj": map[string]any{"id": 5, "name": "Generated public place", "address_city": "Cluj-Napoca"}}
	post["attachment_list"] = []map[string]any{{"kind": "image", "blocked": false, "processing": false, "failed": false, "expired": false, "original_filename": "fixture.jpg", "url": "/api/media/file/synthetic-image/", "thumb_url": "/api/media/file/synthetic-thumb/", "poster_url": ""}}
	data := pongo2.Context{
		"language": "en", "display_theme": "light", "display_text": "normal", "display_motion": "reduce", "site_name": "Synthetic Social", "csrf": "synthetic-group-detail-csrf", "request": map[string]any{"csp_nonce": "synthetic-group-detail-nonce"}, "user": socialActor(actor),
		"group":    map[string]any{"id": int64(41), "owner_id": actor.ID, "owner_is_staff": false, "title": "Generated standing group", "description": "Generated description", "cohort": "adult", "status": "active", "is_staff_curated": false, "is_publicly_listed": false, "area": map[string]any{"name": "Cluj-Napoca"}, "category": map[string]any{"name": "Sports"}, "activity_type": map[string]any{"name": "Basketball"}, "thread": map[string]any{"id": int64(51)}},
		"is_owner": true, "is_member": true, "show_thread": true, "can_post": true, "can_announce": true, "can_ask": false, "show_dissent_concern": true, "connections_enabled": true,
		"roster": []map[string]any{owner, other}, "posts": []map[string]any{post}, "announcements": []map[string]any{groupDetailFixturePost(60, owner, "Own announcement\nBring a book", "adult")},
		"post_form": map[string]any{"body": map[string]any{"value": ""}}, "reply_target": nil,
		"thread_chat_config": groupDetailFixtureConfig(41, 51, actor.ID), "reaction_emojis": socialFacets(), "presend_nudge": map[string]any{"rules": socialPresendRules, "message": "Synthetic existing nudge message"},
		"feed": []map[string]any{{"id": int64(71), "title": "Generated upcoming activity", "description": "A generated existing activity", "activity_type": map[string]any{"name": "Basketball", "slug": "basketball"}, "secondary_types": map[string]any{"all": []map[string]any{{"name": "Reading"}}}, "starts_at": time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), "place": map[string]any{"display_name": "Public Library", "address_city": "Cluj-Napoca"}, "cost_band": "free", "get_cost_band_display": "Free", "difficulty": "easy", "get_difficulty_display": "Easy", "status": "open", "beginners_welcome": true, "guardian_accompanied": false}},
	}
	return NewRenderer(root), request, data
}

func TestNativeGroupDetailWholeViewPreservesExistingActionsAndThread(t *testing.T) {
	r, request, data := groupDetailProjectionFixture(t)
	raw, err := r.renderGroupDetail(request, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Generated standing group", "Generated description", "Members (2)", "Generated member", `action="/groups/41/archive/"`, `action="/groups/41/listing/"`, `name="listed" value="1"`, `data-confirm="Archive this group? Its thread will be frozen."`, `action="/groups/41/announce/"`, `id="thread-list"`, `id="post-61"`, `data-author="11"`, `class="replies" data-parent="61"`, `id="post-62"`, `id="compose" action="/groups/41/post/"`, `name="reply_to"`, "<strong>Current body</strong>", "@member", `href="https://fixture.invalid/shared"`, "&lt;script&gt;escaped&lt;/script&gt;", "(message removed)", "A few people felt welcome.", "Generated public place", `class="post-media"`, `src="/api/media/file/synthetic-thumb/"`, `action="/groups/41/posts/61/edit/"`, `action="/groups/41/posts/61/delete/"`, `action="/groups/41/posts/61/react/"`, `action="/groups/41/posts/61/dissent/"`, `action="/groups/41/posts/61/concern/"`, `/report/?type=post&amp;id=62`, "Generated upcoming activity", "Public Library", `src="/static/js/hovercard.js"`, `id="site-js"`, `data-meetups-owner="00000000-0000-4000-8000-000000000011"`, `/static/css/group-detail-native.css`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatal("whole existing group view lost a capability", want)
		}
	}
	for _, absent := range []string{`href="/report/?type=post&amp;id=61"`, `action="/groups/41/join/"`, `action="/groups/41/leave/"`, `name="owner"`, `name="cohort"`, `href="javascript:`, `<script>escaped</script>`, `<span class="mention">@member</span>`} {
		if bytes.Contains(raw, []byte(absent)) {
			t.Fatal("authority, report or markup boundary widened", absent)
		}
	}
	if bytes.Index(raw, []byte(`/static/js/presend-nudge.js`)) >= bytes.Index(raw, []byte(`/static/js/thread-chat.js`)) {
		t.Fatal("existing presend guard no longer loads before live composer")
	}
	page, err := r.groupDetailPage(request, data)
	if err != nil {
		t.Fatal(err)
	}
	page.Posts[0].Facets[0].Label = "PRIVATE-shared-state"
	again, err := r.groupDetailPage(request, data)
	if err != nil || again.Posts[0].Facets[0].Label == "PRIVATE-shared-state" {
		t.Fatal("native projection retains mutable per-request view state", err)
	}
}

func TestNativeGroupDetailOutsideCohortCuratorKeepsOnlyOwnStaticBroadcasts(t *testing.T) {
	r, request, data := groupDetailProjectionFixture(t)
	actor, _ := platform.ActorFrom(request)
	actor.IsStaff = true
	request = platform.WithActor(request, actor)
	data["user"] = socialActor(actor)
	g := data["group"].(map[string]any)
	g["cohort"], g["is_staff_curated"], g["owner_is_staff"] = "child", true, true
	data["is_member"], data["can_post"], data["show_dissent_concern"], data["roster"], data["posts"], data["feed"] = false, false, false, nil, nil, []map[string]any{}
	delete(data, "thread_chat_config")
	delete(data, "presend_nudge")
	raw, err := r.renderGroupDetail(request, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Own announcement", "Bring a book", "Post an announcement", `action="/groups/41/announce/"`, `action="/groups/41/archive/"`, `/static/js/hovercard.js`, `/static/js/site.js`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatal("curator lost existing own management/broadcast", want)
		}
	}
	for _, absent := range []string{"Members (", "Current body", "Current reply", "Generated upcoming activity", `id="compose"`, `thread-chat-config`, `/static/js/thread-chat.js`, `/static/js/presend-nudge.js`, `name="listed"`} {
		if bytes.Contains(raw, []byte(absent)) {
			t.Fatal("metadata bypass created peer access or unusable live client", absent)
		}
	}
	data["posts"] = []map[string]any{groupDetailFixturePost(99, groupDetailFixturePerson(14, "PRIVATE-minor-peer"), "PRIVATE-minor-body", "child")}
	if _, err = r.renderGroupDetail(request, data); err == nil {
		t.Fatal("cross-cohort peer rows admitted by typed metadata fallback")
	}
	data["posts"] = nil
	data["announcements"] = []map[string]any{groupDetailFixturePost(98, groupDetailFixturePerson(15, "PRIVATE-other-curator"), "PRIVATE-other-broadcast", "adult")}
	if _, err = r.renderGroupDetail(request, data); err == nil {
		t.Fatal("outside-cohort fallback admitted another author's broadcast")
	}
}

func TestNativeGroupDetailNonmemberAndAdmittedEmptyRosterStayDistinct(t *testing.T) {
	r, request, data := groupDetailProjectionFixture(t)
	data["roster"] = []map[string]any{}
	page, err := r.groupDetailPage(request, data)
	if err != nil || !page.RosterVisible || page.RosterHeading != "Members (0)" {
		t.Fatal("admitted empty roster disappeared", err)
	}
	actor, _ := platform.ActorFrom(request)
	actor.ID = 12
	request = platform.WithActor(request, actor)
	data["user"] = socialActor(actor)
	data["is_owner"], data["is_member"], data["show_thread"], data["can_post"], data["can_announce"], data["show_dissent_concern"] = false, false, false, false, false, false
	data["roster"], data["posts"], data["announcements"] = nil, nil, nil
	delete(data, "thread_chat_config")
	delete(data, "presend_nudge")
	raw, err := r.renderGroupDetail(request, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`action="/groups/41/join/"`, "Generated upcoming activity"} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatal("existing nonmember capability lost", want)
		}
	}
	for _, absent := range []string{"Members (", "Current body", "Own announcement", `class="thread"`, `id="compose"`, `class="respond-menu"`, `/static/js/thread-chat.js`, `action="/groups/41/archive/"`} {
		if bytes.Contains(raw, []byte(absent)) {
			t.Fatal("nonmember received private or owner surface", absent)
		}
	}
}

func TestNativeGroupDetailSameCohortStaffReadWithdrawalDoesNotInventLiveAuthority(t *testing.T) {
	r, request, data := groupDetailProjectionFixture(t)
	actor, _ := platform.ActorFrom(request)
	actor.IsStaff = true
	actor.IdentityVerified = false
	request = platform.WithActor(request, actor)
	data["user"] = socialActor(actor)
	data["is_member"], data["can_post"], data["show_dissent_concern"], data["roster"], data["posts"] = false, false, false, nil, nil
	delete(data, "thread_chat_config")
	delete(data, "presend_nudge")
	raw, err := r.renderGroupDetail(request, data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("Own announcement")) || !bytes.Contains(raw, []byte("Generated upcoming activity")) || bytes.Contains(raw, []byte("Current body")) || bytes.Contains(raw, []byte(`thread-chat-config`)) || bytes.Contains(raw, []byte(`id="compose"`)) {
		t.Fatal("same-cohort staff fallback lost static projection or gained live/peer authority")
	}
}

func TestNativeGroupDetailKeepsExistingBoundPostAndReplyValueOrdering(t *testing.T) {
	r, request, data := groupDetailProjectionFixture(t)
	request.Method = http.MethodPost
	request.PostForm = url.Values{"body": {"first cleaner value", "last display value"}, "reply_to": {"61", "99"}}
	data["post_form"] = map[string]any{"body": map[string]any{"value": "last display value"}, "reply_to": map[string]any{"value": "99", "widget": pongo2.AsSafeValue("PRIVATE-widget-must-not-be-parsed")}}
	data["reply_target"] = data["posts"].([]map[string]any)[0]
	page, err := r.groupDetailPage(request, data)
	if err != nil || page.ComposeBody != "last display value" || page.ComposeReplyTo != "61" || page.ReplyAuthor != "Generated group owner" {
		t.Fatal("bound body/hidden reply ordering no longer matches existing builder/repair", err)
	}
	raw, err := r.renderGroupDetail(request, data)
	if err != nil || bytes.Contains(raw, []byte("PRIVATE-widget-must-not-be-parsed")) || !bytes.Contains(raw, []byte(`id="id_reply_to" value="61"`)) {
		t.Fatal("native composer trusted widget HTML or lost exact hidden reply value", err)
	}
}

func TestNativeGroupDetailEveryMinorRoleUsesExactGroupBodyProducerAndClamps(t *testing.T) {
	for _, cohort := range []string{"teen", "child"} {
		t.Run(cohort, func(t *testing.T) {
			r, request, data := groupDetailProjectionFixture(t)
			actor, _ := platform.ActorFrom(request)
			actor.ID = 14
			actor.Cohort = cohort
			request = platform.WithActor(request, actor)
			data["user"] = socialActor(actor)
			data["group"].(map[string]any)["cohort"] = cohort
			data["is_owner"], data["can_announce"], data["can_post"], data["show_dissent_concern"] = false, false, false, false
			data["roster"], data["announcements"], data["feed"] = nil, nil, []map[string]any{}
			body := "**Current minor body** @member https://fixture.invalid/minor"
			post := groupDetailFixturePost(91, groupDetailFixturePerson(15, "Generated minor peer"), body, cohort)
			data["posts"] = []map[string]any{post}
			data["thread_chat_config"] = groupDetailFixtureConfig(41, 51, actor.ID)
			raw, err := r.renderGroupDetail(request, data)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"<strong>Current minor body</strong>", "@member", "https://fixture.invalid/minor", `/report/?type=post&amp;id=91`, `class="rx-pick"`, `/static/js/thread-chat.js`} {
				if !bytes.Contains(raw, []byte(want)) {
					t.Fatal("minor permitted readonly/reaction/report behavior lost", want)
				}
			}
			for _, absent := range []string{`href="https://fixture.invalid/minor"`, `class="mention"`, `class="dissent-row"`, `class="concern-row"`, `id="compose"`, "Members ("} {
				if bytes.Contains(raw, []byte(absent)) {
					t.Fatal("minor group link/roster/write clamp widened", absent)
				}
			}
			post["body_html"] = pongo2.AsSafeValue(BodyMarkup(body, map[string]bool{}, true))
			if _, err = r.renderGroupDetail(request, data); err == nil {
				t.Fatal("adult-link markup was trusted for a minor role")
			}
		})
	}
}

func TestNativeGroupDetailMalformedProjectionAndExecutableMarkupRefuseBeforeOutput(t *testing.T) {
	probes := map[string]func(pongo2.Context){
		"truthy-member-string": func(data pongo2.Context) { data["is_member"] = "yes" },
		"wrong-owner-flag":     func(data pongo2.Context) { data["is_owner"] = false },
		"null-live-config":     func(data pongo2.Context) { data["thread_chat_config"] = nil },
		"wrong-live-viewer":    func(data pongo2.Context) { data["thread_chat_config"].(map[string]any)["meId"] = int64(999) },
		"arbitrary-safe-body": func(data pongo2.Context) {
			data["posts"].([]map[string]any)[0]["body_html"] = pongo2.AsSafeValue(`<script>PRIVATE-HTML</script>`)
		},
		"hidden-raw-post": func(data pongo2.Context) { data["posts"].([]map[string]any)[0]["is_hidden"] = true },
		"unsafe-avatar": func(data pongo2.Context) {
			data["roster"].([]map[string]any)[0]["avatar_uri"] = "javascript:PRIVATE-avatar"
		},
		"unsafe-media": func(data pongo2.Context) {
			data["posts"].([]map[string]any)[0]["attachment_list"].([]map[string]any)[0]["url"] = "https://outside.invalid/private"
		},
		"unknown-status": func(data pongo2.Context) { data["group"].(map[string]any)["status"] = "unknown" },
	}
	for name, change := range probes {
		t.Run(name, func(t *testing.T) {
			r, request, data := groupDetailProjectionFixture(t)
			change(data)
			body, err := r.renderGroupDetail(request, data)
			if err == nil || len(body) != 0 {
				t.Fatal("unadmitted native projection produced partial or unsafe output", err)
			}
		})
	}
	r, request, data := groupDetailProjectionFixture(t)
	data["thread_chat_config"].(map[string]any)["i18n"].(map[string]string)["reply"] = `</script><script>PRIVATE-script-text</script>`
	data["group"].(map[string]any)["title"] = `<img src=x onerror="PRIVATE-title">`
	data["messages"] = []socialMessage{{"tags": "error", "message": "PRIVATE-SQL-PROVIDER"}}
	data["presend_nudge"] = nil
	raw, err := r.renderGroupDetail(request, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`<img src=x`, `</script><script>PRIVATE-script-text`, "PRIVATE-SQL-PROVIDER"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatal("native HTML/JSON/error escape boundary failed", forbidden)
		}
	}
	if !strings.Contains(string(raw), `\u003c/script\u003e`) || !strings.Contains(string(raw), "Check the form and try again.") {
		t.Fatal("safe current JSON or finite error message disappeared")
	}
}
