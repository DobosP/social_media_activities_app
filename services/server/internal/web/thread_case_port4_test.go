package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func webCasePort4Follow(t *testing.T, mux *http.ServeMux, actor platform.Actor, response *httptest.ResponseRecorder) string {
	t.Helper()
	if response.Code == http.StatusFound {
		// Like the source client's follow=True, retain response cookies across
		// the302 before rendering the authorized GET destination.
		r := platform.WithActor(httptest.NewRequest("GET", "https://fixture.local"+response.Header().Get("Location"), nil), actor)
		for _, cookie := range response.Result().Cookies() {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("source redirect destination", w.Code)
		}
		return w.Body.String()
	}
	if response.Code != http.StatusOK {
		t.Fatal("source followed form response", response.Code)
	}
	return response.Body.String()
}

func TestWebCasePort4ThreadReplyAuthorMutationsAndPrivateCursor(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	activity := socialLegacyActivity(t, s, owner, place, typ, "Case port4 thread")
	member := webCasePort3Member(t, s, owner, activity, "case-port4-thread-member")
	stranger := testdb.Actor(t, s.DB, "case-port4-thread-stranger", "adult")
	parent, err := s.Social.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: "where do we meet?"}, false)
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/activities/%d/", activity)
	t.Run("member_reply_and_exact_compose_target", func(t *testing.T) {
		body := webCasePortHTML(t, mux, member, fmt.Sprintf("%s?reply_to=%d", path, parent))
		webCasePortContains(t, body, "Replying to", fmt.Sprintf(`value="%d"`, parent))
		w := webCasePort2Post(t, mux, member, path, path+"post/", url.Values{"body": {"north gate"}, "reply_to": {fmt.Sprint(parent)}})
		if w.Code != 302 {
			t.Fatal("source reply redirect", w.Code, w.Body.String())
		}
		var target int64
		if err := s.DB.QueryRow(ctx, `SELECT reply_to_id FROM social_post WHERE author_id=$1 AND body='north gate'`, member.ID).Scan(&target); err != nil || target != parent {
			t.Fatal("registered reply did not bind exact parent", err, target)
		}
	})
	t.Run("author_edits_then_soft_deletes_own_post", func(t *testing.T) {
		post, err := s.Social.WritePost(ctx, member, "activity", activity, social.PostInput{Body: "helo"}, false)
		if err != nil {
			t.Fatal(err)
		}
		w := webCasePort2Post(t, mux, member, path, fmt.Sprintf("%spost/%d/edit/", path, post), url.Values{"body": {"hello"}})
		var body string
		if err := s.DB.QueryRow(ctx, `SELECT body FROM social_post WHERE id=$1`, post).Scan(&body); err != nil || body != "hello" || w.Code != 302 {
			t.Fatal("source author edit", err, body, w.Code)
		}
		w = webCasePort2Post(t, mux, member, path, fmt.Sprintf("%spost/%d/delete/", path, post), url.Values{})
		var hidden bool
		if err := s.DB.QueryRow(ctx, `SELECT is_hidden FROM social_post WHERE id=$1`, post).Scan(&hidden); err != nil || !hidden || w.Code != 302 {
			t.Fatal("source author soft delete", err, hidden, w.Code)
		}
	})
	t.Run("non_author_cannot_edit", func(t *testing.T) {
		webCasePort2Post(t, mux, member, path, fmt.Sprintf("%spost/%d/edit/", path, parent), url.Values{"body": {"hijack"}})
		var body string
		if err := s.DB.QueryRow(ctx, `SELECT body FROM social_post WHERE id=$1`, parent).Scan(&body); err != nil || body != "where do we meet?" {
			t.Fatal("nonauthor changed owner post", err, body)
		}
	})
	t.Run("nonmember_cursor_permalink_and_reply_query_preserve_wall", func(t *testing.T) {
		body := webCasePortHTML(t, mux, stranger, fmt.Sprintf("%s?before=%d&reply_to=%d#post-%d", path, parent, parent, parent))
		webCasePortContains(t, body, "private to members")
		webCasePortAbsent(t, body, "where do we meet?", "north gate", "Replying to")
	})
}

func TestWebCasePort4ThreadBoundedOlderLinkAndMalformedCursor(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	s.Social.Policy.ThreadPostLimit = 3
	activity := socialLegacyActivity(t, s, owner, place, typ, "Case port4 bounded thread")
	for i := range 5 {
		if _, err := s.Social.WritePost(context.Background(), owner, "activity", activity, social.PostInput{Body: fmt.Sprintf("bounded msg%d", i)}, false); err != nil {
			t.Fatal(err)
		}
	}
	path := fmt.Sprintf("/activities/%d/", activity)
	webCasePortContains(t, webCasePortHTML(t, mux, owner, path), "Older messages", "before=")
	if _, err := s.Social.WritePost(context.Background(), owner, "activity", activity, social.PostInput{Body: "hi"}, false); err != nil {
		t.Fatal(err)
	}
	webCasePortContains(t, webCasePortHTML(t, mux, owner, path+"?before=not-a-number"), "hi")
}

func TestWebCasePort4DeleteModeratedPostExplainsRestorationLimitBothThreads(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	mod := webCasePort3Moderator(t, s)
	activity := socialLegacyActivity(t, s, owner, place, typ, "Case port4 moderated activity")
	member := webCasePort3Member(t, s, owner, activity, "case-port4-moderated-member")
	groupOwner := testdb.Actor(t, s.DB, "case-port4-group-curator", "adult")
	groupOwner.IsStaff = true
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1`, groupOwner.ID); err != nil {
		t.Fatal(err)
	}
	group, err := s.Social.CreateGroup(ctx, groupOwner, social.GroupInput{City: "Cluj-Napoca", ActivityType: &typ, Title: "Case port4 curated group"})
	if err != nil {
		t.Fatal(err)
	}
	groupMember := testdb.Actor(t, s.DB, "case-port4-moderated-group-member", "adult")
	if err := s.Social.JoinGroup(ctx, groupMember, group); err != nil {
		t.Fatal(err)
	}
	for _, thread := range []struct {
		kind, path string
		id         int64
		author     platform.Actor
	}{{"activity", fmt.Sprintf("/activities/%d/", activity), activity, member}, {"group", fmt.Sprintf("/groups/%d/", group), group, groupMember}} {
		t.Run(thread.kind, func(t *testing.T) {
			post, err := s.Social.WritePost(ctx, thread.author, thread.kind, thread.id, social.PostInput{Body: "mine in " + thread.kind}, false)
			if err != nil {
				t.Fatal(err)
			}
			webCasePort3Moderate(t, s, mod, "social", "post", post, "remove", "other", "")
			segment := "post"
			if thread.kind == "group" {
				segment = "posts"
			}
			w := webCasePort2Post(t, mux, thread.author, thread.path, fmt.Sprintf("%s%s/%d/delete/", thread.path, segment, post), url.Values{})
			if w.Code != 302 {
				t.Fatal("source moderated deletion redirect", w.Code)
			}
			body := webCasePort4Follow(t, mux, thread.author, w)
			webCasePortContains(t, body, "This message had already been hidden by a moderation decision.")
			if thread.kind == "activity" {
				webCasePortContains(t, body, "review the decision in your safety record")
			}
			var deleted bool
			if err := s.DB.QueryRow(ctx, `SELECT is_author_deleted FROM social_post WHERE id=$1`, post).Scan(&deleted); err != nil || !deleted {
				t.Fatal("moderated delete failed to stamp author provenance", err, deleted)
			}
		})
	}
}

func TestWebCasePort4DeleteManualHideRepeatAndPendingAppeal(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	activity := socialLegacyActivity(t, s, owner, place, typ, "Case port4 deletion states")
	member := webCasePort3Member(t, s, owner, activity, "case-port4-delete-member")
	mod := webCasePort3Moderator(t, s)
	path := fmt.Sprintf("/activities/%d/", activity)
	write := func(body string) int64 {
		post, err := s.Social.WritePost(ctx, member, "activity", activity, social.PostInput{Body: body}, false)
		if err != nil {
			t.Fatal(err)
		}
		return post
	}
	remove := func(post int64) string {
		w := webCasePort2Post(t, mux, member, path, fmt.Sprintf("%spost/%d/delete/", path, post), url.Values{})
		return webCasePort4Follow(t, mux, member, w)
	}
	t.Run("admin_manual_hide_has_no_moderation_claim", func(t *testing.T) {
		post := write("manual hidden own post")
		if _, err := s.DB.Exec(ctx, `UPDATE social_post SET is_hidden=true WHERE id=$1`, post); err != nil {
			t.Fatal(err)
		}
		webCasePortAbsent(t, remove(post), "hidden by a moderation decision")
		var deleted bool
		if err := s.DB.QueryRow(ctx, `SELECT is_author_deleted FROM social_post WHERE id=$1`, post).Scan(&deleted); err != nil || !deleted {
			t.Fatal("manual hide author deletion", err, deleted)
		}
	})
	t.Run("repeat_ordinary_self_delete_is_silent", func(t *testing.T) {
		post := write("repeat self deletion")
		remove(post)
		webCasePortAbsent(t, remove(post), "already been hidden by a moderation decision")
	})
	t.Run("pending_contest_refusal_keeps_conditional_remedy", func(t *testing.T) {
		post := write("pending contest own post")
		action := webCasePort3Moderate(t, s, mod, "social", "post", post, "remove", "other", "")
		appeal, err := s.Safety.FileAppeal(ctx, member, action, "the removal was wrong")
		if err != nil {
			t.Fatal(err)
		}
		body := remove(post)
		webCasePortContains(t, body, "contesting the moderation decision that hid this message", "nothing else is holding the message")
		var deleted bool
		var status string
		if err := s.DB.QueryRow(ctx, `SELECT p.is_author_deleted,ap.status FROM social_post p CROSS JOIN safety_moderationappeal ap WHERE p.id=$1 AND ap.id=$2`, post, appeal).Scan(&deleted, &status); err != nil || deleted || status != "pending" {
			t.Fatal("pending remedy was changed by refused deletion", err, deleted, status)
		}
	})
}

func TestWebCasePort4MaterializedSentimentFootersHaveBoundedHTMLQueries(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	db, trace := testdb.TracedPool(t, s.DB)
	s.DB, s.Social.DB, s.Catalog.DB = db, db, db
	s.Accounts.DB, s.Recommendations.DB, s.Discovery.DB, s.Messaging.DB, s.Safety.DB, s.Notifications.DB = db, db, db, db, db, db
	s.Media = media.NewService(db, nil, nil, media.TokenCodec{Key: []byte("synthetic-case-port4-traced-media-32bytes")}, s.Social)
	// API method closures must capture the traced media instance as well as
	// the domain services whose public pool pointers were replaced above.
	api := http.NewServeMux()
	s.Social.Register(api)
	s.Catalog.Register(api)
	s.Media.Register(api)
	s.Accounts.Register(api)
	s.Notifications.Register(api)
	s.Donations.Register(api)
	s.Messaging.Register(api)
	s.Recommendations.Register(api)
	s.Safety.Register(api)
	s.API = api
	s.Social.Sentiment.AdultK = 2
	ctx := context.Background()
	activity := socialLegacyActivity(t, s, owner, place, typ, "Case port4 footer query ceiling")
	members := []platform.Actor{}
	for i := range 4 {
		// This source fixture seeds admitted seats directly; it does not test
		// the growing electorate's join-vote quorum. A single owner vote would
		// leave later requests pending and invalidate the footer audience.
		member := testdb.Actor(t, db, fmt.Sprintf("case-port4-footer-member-%d", i), "adult")
		if _, err := db.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,'member','member','unknown','none',false,now(),now(),now())`, activity, member.ID); err != nil {
			t.Fatal(err)
		}
		members = append(members, member)
	}
	var audience int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM social_membership WHERE activity_id=$1 AND state='member' AND role<>'guardian'`, activity).Scan(&audience); err != nil || audience != 5 {
		t.Fatal("source footer fixture lacks five admitted audience seats", err, audience)
	}
	seed := func(begin, end int) {
		for i := begin; i < end; i++ {
			parent, err := s.Social.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: fmt.Sprintf("footer parent%d", i)}, false)
			if err != nil {
				t.Fatal(err)
			}
			reply, err := s.Social.WritePost(ctx, members[0], "activity", activity, social.PostInput{Body: fmt.Sprintf("footer reply%d", i), ReplyTo: &parent}, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, post := range []int64{parent, reply} {
				for _, member := range members[:2] {
					if _, err := s.Social.ToggleSentiment(ctx, member, post, "reaction", "helped_me"); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	expectedAvatar, err := accounts.Avatar(ctx, db, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	expectedVisiblePosts := 8
	read := func() int64 {
		trace.Reset()
		body := webCasePortHTML(t, mux, owner, fmt.Sprintf("/activities/%d/", activity))
		queries := trace.Count()
		if queries == 0 || queries > 60 || !strings.Contains(body, "People found this helpful.") {
			t.Fatal("source materialized footer content/query ceiling", queries)
		}
		if strings.Count(body, `<article id="post-`) != expectedVisiblePosts || strings.Count(body, "People found this helpful.") != expectedVisiblePosts || !strings.Contains(body, expectedAvatar) {
			t.Fatal("positive stream/footer/viewer base-avatar projection incomplete", expectedVisiblePosts)
		}
		return queries
	}
	seed(0, 4)
	if out, err := s.Social.RecomputeSentiment(ctx, time.Now()); err != nil || out.Latched != 8 {
		t.Fatal("eight actual footers not materialized", out, err)
	}
	small := read()
	seed(4, 12)
	expectedVisiblePosts = 24
	if _, err := s.Social.RecomputeSentiment(ctx, time.Now().Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	large := read()
	if large > small+3 {
		t.Fatal("footer HTML query count grew with8 to24 posts", small, large)
	}
	t.Logf("materialized thread footer HTML queries8->24: %d->%d", small, large)
}
