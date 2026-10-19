package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/DobosP/social_media_activities_app/services/server/internal/web"
	"github.com/jackc/pgx/v5"
)

func groupDetailAppLogin(t *testing.T, f groupNativeAppFixture, actor platform.Actor) familyAppCredential {
	t.Helper()
	const password = "synthetic-group-detail-http-password-only"
	hash, err := authcore.HashPassword(context.Background(), password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.DB.Exec(context.Background(), `UPDATE accounts_user SET password=$2 WHERE id=$1`, actor.ID, hash); err != nil {
		t.Fatal(err)
	}
	return familyAppLogin(t, f.app, actor.Username, password)
}
func groupDetailAppSeed(t *testing.T, f groupNativeAppFixture, title string) (int64, string) {
	t.Helper()
	id, err := f.app.Social.CreateGroup(context.Background(), f.owner, social.GroupInput{City: "Synthetic Detail City", ActivityType: &f.typeID, Title: title, Description: "Generated standing group detail"})
	if err != nil {
		t.Fatal(err)
	}
	return id, fmt.Sprintf("/groups/%d/", id)
}
func groupDetailAppHTML(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || w.Header().Get("Cache-Control") != "private, no-cache" || !strings.Contains(w.Header().Get("Vary"), "Cookie") || w.Header().Get("X-Social-Runtime") != "go" || !strings.Contains(w.Header().Get("Content-Security-Policy-Report-Only"), "frame-ancestors 'none'") || w.Header().Get("Reporting-Endpoints") == "" {
		t.Fatal("native group detail lost actual private App transport", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `body class="group-detail-page"`) || !strings.Contains(body, `/static/css/group-detail-native.css`) {
		t.Fatal("registered group GET did not use complete native view")
	}
	return body
}
func groupDetailAppState(t *testing.T, f groupNativeAppFixture, group int64) string {
	t.Helper()
	var value string
	if err := f.app.DB.QueryRow(context.Background(), `SELECT jsonb_build_object('status',g.status,'listed',g.is_publicly_listed,'members',(SELECT jsonb_agg(jsonb_build_array(user_id,role,state) ORDER BY user_id) FROM social_groupmembership WHERE group_id=g.id),'posts',(SELECT count(*) FROM social_post p JOIN social_thread th ON th.id=p.thread_id WHERE th.group_id=g.id),'audit',(SELECT count(*) FROM safety_auditlog WHERE target_ref=$2))::text FROM social_group g WHERE g.id=$1`, group, fmt.Sprintf("social.group:%d", group)).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestNativeGroupDetailScopedStaticAppTransport(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := web.StaticHandler(filepath.Join(root, "static"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Config: Config{AllowedHosts: []string{"social.fixture.test"}}, Static: handler}
	const expected = ".group-detail-page .btn { transition: transform .04s ease; }\n:root .group-detail-page .site-header { background: var(--card); }\n\n.group-detail-page .post .meta a, .group-detail-page .reply-quote a { text-decoration: underline; text-underline-offset: .16em; }\n"
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest(method, "https://social.fixture.test/static/css/group-detail-native.css", nil))
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") || w.Header().Get("Cache-Control") != "public, max-age=3600" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Social-Runtime") != "go" || w.Header().Get("ETag") == "" || len(w.Result().Cookies()) != 0 {
			t.Fatal("scoped native detail CSS lost real App delivery", method, w.Code)
		}
		if method == http.MethodGet && w.Body.String() != expected || method == http.MethodHead && w.Body.Len() != 0 {
			t.Fatal("native detail CSS body differs", method)
		}
	}
}

func TestNativeGroupDetailAssembledMembershipOwnerActionsAndCurrentAuthority(t *testing.T) {
	f := newGroupNativeAppFixture(t)
	ctx := context.Background()
	group, path := groupDetailAppSeed(t, f, "Generated native detail group")
	peer := groupDetailAppLogin(t, f, f.peer)
	post, err := f.app.Social.WritePost(ctx, f.owner, "group", group, social.PostInput{Body: "PRIVATE-existing-member-thread"}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("nonmember-metadata-without-private-thread-roster-or-owner-controls", func(t *testing.T) {
		body := groupDetailAppHTML(t, familyAppRequest(t, f.app, peer, "GET", path, "", nil, false))
		if !strings.Contains(body, `action="`+path+`join/"`) || strings.Contains(body, "PRIVATE-existing-member-thread") || strings.Contains(body, "Members (") || strings.Contains(body, `id="compose"`) || strings.Contains(body, `action="`+path+`archive/"`) || strings.Contains(body, `thread-chat-config`) {
			t.Fatal("nonmember group metadata widened authority or private reads")
		}
	})
	t.Run("real-CSRF-Origin-and-read-only-method-refuse-without-writes", func(t *testing.T) {
		before := groupDetailAppState(t, f, group)
		for _, probe := range []struct {
			proof  bool
			origin string
		}{{false, "https://app.example"}, {true, "https://outside.invalid"}} {
			w := familyAppRequest(t, f.app, peer, "POST", path+"join/", probe.origin, nil, probe.proof)
			if w.Code != http.StatusForbidden || groupDetailAppState(t, f, group) != before {
				t.Fatal("group detail CSRF/Origin failed before mutation")
			}
		}
		w := familyAppRequest(t, f.app, peer, "GET", path+"join/", "", nil, false)
		if w.Code != http.StatusMethodNotAllowed || groupDetailAppState(t, f, group) != before {
			t.Fatal("GET performed a group mutation")
		}
	})
	t.Run("idempotent-real-join-roster-live-config-and-own-reply", func(t *testing.T) {
		for range 2 {
			if w := familyAppRequest(t, f.app, peer, "POST", path+"join/", "https://app.example", nil, true); w.Code != http.StatusFound || w.Header().Get("Location") != path {
				t.Fatal("existing join redirect changed", w.Code)
			}
		}
		var count, audits int
		if err := f.app.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM social_groupmembership WHERE group_id=$1 AND user_id=$2 AND state='member'),(SELECT count(*) FROM safety_auditlog WHERE event='group.joined' AND actor_id=$2 AND target_ref=$3)`, group, f.peer.ID, fmt.Sprintf("social.group:%d", group)).Scan(&count, &audits); err != nil || count != 1 || audits != 1 {
			t.Fatal("repeat join was not idempotent/audited exactly once", err, count, audits)
		}
		body := groupDetailAppHTML(t, familyAppRequest(t, f.app, peer, "GET", path+"?reply_to="+strconv.FormatInt(post, 10), "", nil, false))
		for _, want := range []string{"PRIVATE-existing-member-thread", "Members (2)", `action="` + path + `leave/"`, `id="compose"`, `value="` + strconv.FormatInt(post, 10) + `"`, `id="thread-chat-config"`, `/static/js/presend-nudge.js`, `/static/js/thread-chat.js`} {
			if !strings.Contains(body, want) {
				t.Fatal("admitted member capability missing", want)
			}
		}
		if !strings.Contains(body, `/report/?type=post&amp;id=`+strconv.FormatInt(post, 10)) {
			t.Fatal("member lost other-author Report link")
		}
		w := familyAppRequest(t, f.app, peer, "POST", path+"post/", "https://app.example", url.Values{"body": {"Generated current reply"}, "reply_to": {strconv.FormatInt(post, 10)}}, true)
		var reply int64
		if err := f.app.DB.QueryRow(ctx, `SELECT reply_to_id FROM social_post WHERE author_id=$1 AND body='Generated current reply'`, f.peer.ID).Scan(&reply); err != nil || reply != post || w.Code != http.StatusFound {
			t.Fatal("actual group reply lost authorized parent", err, w.Code)
		}
	})
	t.Run("owner-listing-nonowner-refusal-and-owner-leave-refusal", func(t *testing.T) {
		for _, listed := range []string{"1", "0"} {
			w := familyAppRequest(t, f.app, f.credentials, "POST", path+"listing/", "https://app.example", url.Values{"listed": {listed}, "next": {"//outside.invalid/private"}}, true)
			var actual bool
			if err := f.app.DB.QueryRow(ctx, `SELECT is_publicly_listed FROM social_group WHERE id=$1`, group).Scan(&actual); err != nil || actual != (listed == "1") || w.Code != http.StatusFound || w.Header().Get("Location") != path {
				t.Fatal("adult owner listing or fixed redirect changed", err, w.Code)
			}
		}
		before := groupDetailAppState(t, f, group)
		for _, probe := range []struct {
			credentials familyAppCredential
			action      string
			values      url.Values
		}{{peer, "listing/", url.Values{"listed": {"1"}}}, {f.credentials, "leave/", nil}} {
			w := familyAppRequest(t, f.app, probe.credentials, "POST", path+probe.action, "https://app.example", probe.values, true)
			groupDetailAppHTML(t, w)
			if groupDetailAppState(t, f, group) != before {
				t.Fatal("nonowner listing or owner leave changed group state")
			}
		}
	})
	t.Run("withdrawn-assurance-keeps-thread-closed-but-privacy-leave-works", func(t *testing.T) {
		if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=false WHERE id=$1`, f.peer.ID); err != nil {
			t.Fatal(err)
		}
		body := groupDetailAppHTML(t, familyAppRequest(t, f.app, peer, "GET", path, "", nil, false))
		if strings.Contains(body, "PRIVATE-existing-member-thread") || strings.Contains(body, "Members (") || strings.Contains(body, `id="compose"`) {
			t.Fatal("current assurance withdrawal retained private read")
		}
		w := familyAppRequest(t, f.app, peer, "POST", path+"leave/", "https://app.example", nil, true)
		var state string
		if err := f.app.DB.QueryRow(ctx, `SELECT state FROM social_groupmembership WHERE group_id=$1 AND user_id=$2`, group, f.peer.ID).Scan(&state); err != nil || state != "left" || w.Code != http.StatusFound {
			t.Fatal("privacy withdrawal acquired a new participation requirement", err, w.Code)
		}
		if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=true WHERE id=$1`, f.peer.ID); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("mutual-owner-block-and-current-cohort-refuse-detail", func(t *testing.T) {
		if _, err := f.app.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, f.owner.ID, f.peer.ID); err != nil {
			t.Fatal(err)
		}
		before := groupDetailAppState(t, f, group)
		for _, method := range []string{"GET", "POST"} {
			target := path
			if method == "POST" {
				target += "join/"
			}
			w := familyAppRequest(t, f.app, peer, method, target, "https://app.example", nil, true)
			if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "PRIVATE-existing-member-thread") || groupDetailAppState(t, f, group) != before {
				t.Fatal("blocked group became visible or mutable", method, w.Code)
			}
		}
		if _, err := f.app.DB.Exec(ctx, `DELETE FROM safety_block WHERE blocker_id=$1 AND blocked_id=$2`, f.owner.ID, f.peer.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET cohort='teen',age_band='16_17' WHERE id=$1`, f.peer.ID); err != nil {
			t.Fatal(err)
		}
		if w := familyAppRequest(t, f.app, peer, "GET", path, "", nil, false); w.Code != http.StatusNotFound {
			t.Fatal("current session cohort reload widened group access", w.Code)
		}
		if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, f.peer.ID); err != nil {
			t.Fatal(err)
		}
		if w := familyAppRequest(t, f.app, peer, "GET", path, "", nil, false); w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/login/") {
			t.Fatal("current inactive session retained native detail access", w.Code)
		}
	})
	t.Run("owner-archive-freezes-existing-thread", func(t *testing.T) {
		w := familyAppRequest(t, f.app, f.credentials, "POST", path+"archive/", "https://app.example", nil, true)
		var state string
		if err := f.app.DB.QueryRow(ctx, `SELECT status FROM social_group WHERE id=$1`, group).Scan(&state); err != nil || state != "archived" || w.Code != http.StatusFound || w.Header().Get("Location") != "/communities/" {
			t.Fatal("owner archive or existing redirect changed", err, w.Code)
		}
		if _, err := f.app.Social.WritePost(ctx, f.owner, "group", group, social.PostInput{Body: "Must not revive archived thread"}, false); err == nil {
			t.Fatal("archived group accepted a new post")
		}
	})
}

func TestNativeGroupDetailAssembledMandatoryAuditRollback(t *testing.T) {
	f := newGroupNativeAppFixture(t)
	group, path := groupDetailAppSeed(t, f, "Generated rollback detail group")
	before := groupDetailAppState(t, f, group)
	original := f.app.Social.Audit
	calls := 0
	f.app.Social.Audit = func(ctx context.Context, tx pgx.Tx, actor platform.Actor, event, target string, data any) error {
		if event != "group.public_listing" {
			return original(ctx, tx, actor, event, target, data)
		}
		var listed bool
		if err := tx.QueryRow(ctx, `SELECT is_publicly_listed FROM social_group WHERE id=$1`, group).Scan(&listed); err != nil || !listed {
			return errors.New("synthetic staged listing missing")
		}
		calls++
		if err := original(ctx, tx, actor, event, target, data); err != nil {
			return err
		}
		return errors.New("PRIVATE-synthetic-mandatory-audit-failure")
	}
	defer func() { f.app.Social.Audit = original }()
	w := familyAppRequest(t, f.app, f.credentials, "POST", path+"listing/", "https://app.example", url.Values{"listed": {"1"}}, true)
	body := groupDetailAppHTML(t, w)
	if calls != 1 || groupDetailAppState(t, f, group) != before || strings.Contains(body, "PRIVATE-synthetic") || !strings.Contains(body, "This action is temporarily unavailable. Please try again.") {
		t.Fatal("mandatory audit did not roll back listing/audit with fixed public error")
	}
}

func TestNativeGroupDetailAssembledMinorCuratorAndClosedQuestionBoundaries(t *testing.T) {
	f := newGroupNativeAppFixture(t)
	ctx := context.Background()
	staff := f.owner
	staff.IsStaff = true
	if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1`, staff.ID); err != nil {
		t.Fatal(err)
	}
	f.app.Social.MinorOnboardingEnabled = true
	group, err := f.app.Social.CreateGroup(ctx, staff, social.GroupInput{City: "Synthetic Curated City", ActivityType: &f.typeID, Title: "Generated curated minor group", Cohort: "child"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/groups/%d/", group)
	child := testdb.Actor(t, f.app.DB, "generated-native-detail-child", "child")
	childCredentials := groupDetailAppLogin(t, f, child)
	if err := f.app.Social.JoinGroup(ctx, child, group); err != nil {
		t.Fatal(err)
	}
	announcement, err := f.app.Social.WritePost(ctx, staff, "group", group, social.PostInput{Body: "Generated curator own announcement"}, true)
	if err != nil {
		t.Fatal(err)
	}
	var historical, hidden int64
	for _, row := range []struct {
		body string
		hide bool
		id   *int64
	}{{"PRIVATE-historical-minor-peer", false, &historical}, {"PRIVATE-hidden-minor-body", true, &hidden}} {
		if err := f.app.DB.QueryRow(ctx, `INSERT INTO social_post(thread_id,author_id,body,is_announcement,is_hidden,is_author_deleted,created_at,updated_at) SELECT id,$2,$3,false,$4,false,now(),now() FROM social_thread WHERE group_id=$1 RETURNING id`, group, child.ID, row.body, row.hide).Scan(row.id); err != nil {
			t.Fatal(err)
		}
	}
	curatorBody := groupDetailAppHTML(t, familyAppRequest(t, f.app, f.credentials, "GET", path, "", nil, false))
	for _, want := range []string{"Generated curator own announcement", "Post an announcement", `action="` + path + `announce/"`} {
		if !strings.Contains(curatorBody, want) {
			t.Fatal("curator lost own authorized broadcast", want)
		}
	}
	for _, forbidden := range []string{child.Username, "PRIVATE-historical-minor-peer", "PRIVATE-hidden-minor-body", "Members (", `id="compose"`, `thread-chat-config`, `/static/js/thread-chat.js`, `name="listed"`} {
		if strings.Contains(curatorBody, forbidden) {
			t.Fatal("outside-cohort staff bypass exposed peer data or unusable live client", forbidden)
		}
	}
	childBody := groupDetailAppHTML(t, familyAppRequest(t, f.app, childCredentials, "GET", path, "", nil, false))
	for _, want := range []string{"Generated curator own announcement", "Ask the organiser", `name="prompt"`, `value="next_meetup"`, `value="how_it_works"`, `/static/js/thread-chat.js`} {
		if !strings.Contains(childBody, want) {
			t.Fatal("minor existing readonly/question capability missing", want)
		}
	}
	if !strings.Contains(childBody, `/report/?type=post&amp;id=`+strconv.FormatInt(announcement, 10)) {
		t.Fatal("minor lost other-author announcement Report link")
	}
	for _, forbidden := range []string{"Members (", `id="compose"`, `class="dissent-row"`, `class="concern-row"`, "PRIVATE-hidden-minor-body", `name="listed"`} {
		if strings.Contains(childBody, forbidden) {
			t.Fatal("minor roster/write/hidden-state boundary widened", forbidden)
		}
	}
	postsBefore := 0
	if err := f.app.DB.QueryRow(ctx, `SELECT count(*) FROM social_post p JOIN social_thread th ON th.id=p.thread_id WHERE th.group_id=$1`, group).Scan(&postsBefore); err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"PRIVATE-free-text-address", "next_meetup"} {
		w := familyAppRequest(t, f.app, childCredentials, "POST", path+"ask/", "https://app.example", url.Values{"prompt": {prompt}}, true)
		if prompt == "next_meetup" {
			if w.Code != http.StatusFound {
				t.Fatal("fixed question failed", w.Code)
			}
		} else {
			groupDetailAppHTML(t, w)
		}
	}
	var count, notices, wrongRecipients int
	if err := f.app.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM social_post p JOIN social_thread th ON th.id=p.thread_id WHERE th.group_id=$1),(SELECT count(*) FROM notifications_notification WHERE kind='group_question' AND recipient_id=$2),(SELECT count(*) FROM notifications_notification WHERE kind='group_question' AND recipient_id<>$2)`, group, staff.ID).Scan(&count, &notices, &wrongRecipients); err != nil || count != postsBefore || notices != 1 || wrongRecipients != 0 {
		t.Fatal("closed question stored free text/post or notified peer recipients", err, count, notices, wrongRecipients)
	}
}
