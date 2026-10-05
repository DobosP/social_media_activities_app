package web

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestWebCasePort3ReportSubjectGatesAndLegacyFields(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	activity := socialLegacyActivity(t, s, owner, place, typ, "Case port3 reportable activity")
	peer := webCasePort3Member(t, s, owner, activity, "case-port3-report-member")
	stranger := testdb.Actor(t, s.DB, "case-port3-report-nonmember", "adult")
	minor := testdb.Actor(t, s.DB, "case-port3-report-other-cohort", "child")
	post, err := s.Social.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: "private report-target body sentinel"}, false)
	if err != nil {
		t.Fatal(err)
	}
	activityForm := fmt.Sprintf("/report/?type=activity&id=%d", activity)
	postForm := fmt.Sprintf("/report/?type=post&id=%d", post)
	t.Run("authorized_page_exact_subject_and_fixed_reason_choices", func(t *testing.T) {
		body := webCasePortHTML(t, mux, peer, activityForm)
		webCasePortContains(t, body, "Case port3 reportable activity", `name="type" value="activity"`, fmt.Sprintf(`name="id" value="%d"`, activity), `<select id="id_reason" name="reason" required>`, `value="spam"`, "Harassment / bullying")
		webCasePortAbsent(t, body, "private report-target body sentinel", "Member-only north gate")
		body = webCasePortHTML(t, mux, peer, postForm)
		webCasePortContains(t, body, owner.DisplayName, `name="type" value="post"`)
		webCasePortAbsent(t, body, "private report-target body sentinel", "post(")
	})
	t.Run("malformed_cohort_and_unreadable_post_are_404", func(t *testing.T) {
		for _, test := range []struct {
			actor platform.Actor
			path  string
		}{
			{peer, "/report/?type=activity&id=invalid"},
			{peer, "/report/?type=activity&id=-1"},
			{peer, "/report/?type=message&id=1"},
			{peer, "/report/?type=activity&id=9223372036854775808"},
			{minor, activityForm},
			{stranger, postForm},
		} {
			w := webCasePortRead(mux, test.actor, test.path)
			if w.Code != 404 || strings.Contains(w.Body.String(), "Case port3 reportable activity") || strings.Contains(w.Body.String(), "private report-target body sentinel") {
				t.Fatal("report subject admission disclosed target", test.path, w.Code)
			}
		}
	})
	t.Run("owner_block_and_hidden_activity_stay_reportable", func(t *testing.T) {
		// Reporting is the DSA Art-16 channel, not a read: an organiser who
		// blocks the member first cannot pre-empt it, and hiding keeps it open.
		if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, owner.ID, peer.ID); err != nil {
			t.Fatal(err)
		}
		// Eligibility is wider than read access, so the label is not: a title or
		// name the read gate would hide across a block is never shown.
		body := webCasePortHTML(t, mux, peer, activityForm)
		webCasePortContains(t, body, "this activity")
		webCasePortAbsent(t, body, "Case port3 reportable activity")
		body = webCasePortHTML(t, mux, peer, postForm)
		webCasePortContains(t, body, "A member")
		webCasePortAbsent(t, body, owner.DisplayName, "private report-target body sentinel")
		w := webCasePort2Post(t, mux, peer, "/profile/", "/report/", url.Values{"type": {"activity"}, "id": {fmt.Sprint(activity)}, "reason": {"spam"}})
		if w.Code != 302 {
			t.Fatal("current block refused the report POST", w.Code)
		}
		if _, err := s.DB.Exec(ctx, `DELETE FROM safety_block WHERE blocker_id=$1 AND blocked_id=$2`, owner.ID, peer.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET is_hidden=true WHERE id=$1`, activity); err != nil {
			t.Fatal(err)
		}
		body = webCasePortHTML(t, mux, peer, activityForm)
		webCasePortContains(t, body, "this activity")
		webCasePortAbsent(t, body, "Case port3 reportable activity")
		if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET is_hidden=false WHERE id=$1`, activity); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("legacy_target_bound_query_precedes_post_api_fields_ignored", func(t *testing.T) {
		w := webCasePort2Post(t, mux, peer, activityForm, activityForm, url.Values{"type": {"user"}, "id": {fmt.Sprint(owner.ID)}, "target_type": {"user"}, "target_id": {fmt.Sprint(owner.ID)}, "reporter_id": {fmt.Sprint(owner.ID)}, "reason": {"spam"}, "detail": {"  Typed report detail  "}})
		if w.Code != 302 || w.Header().Get("Location") != fmt.Sprintf("/activities/%d/", activity) {
			t.Fatal("source activity report redirect", w.Code, w.Header().Get("Location"))
		}
		var count int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_report r JOIN django_content_type c ON c.id=r.target_type_id WHERE r.reporter_id=$1 AND c.app_label='social' AND c.model='activity' AND r.target_id=$2 AND r.detail='Typed report detail'`, peer.ID, activity).Scan(&count); err != nil || count != 1 {
			t.Fatal("legacy report subject/actor fields overwritten", err, count)
		}
	})
	t.Run("invalid_reason_keeps_form_and_no_extra_report", func(t *testing.T) {
		w := webCasePort2Post(t, mux, peer, activityForm, "/report/", url.Values{"type": {"activity"}, "id": {fmt.Sprint(activity)}, "reason": {"<script>invalid</script>"}, "detail": {"never stored"}})
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Choose a valid report reason.") || strings.Contains(w.Body.String(), "<script>invalid</script>") {
			t.Fatal("invalid report reason did not preserve safe form", w.Code)
		}
		var count int
		// The block subtest's report plus the legacy-field report above.
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_report WHERE reporter_id=$1`, peer.ID).Scan(&count); err != nil || count != 2 {
			t.Fatal("invalid reason produced report", err, count)
		}
	})
	t.Run("user_report_page_lookups_are_budgeted", func(t *testing.T) {
		// Owner decision 2026-10-05: the user report page is metered like profile
		// cards, refused lookups included, so names cannot be walked by id.
		s.Safety.RatePolicies = map[string]budgets.Policy{"report_lookup": {Limit: 2, Window: time.Hour}}
		defer func() { s.Safety.RatePolicies = nil }()
		if w := webCasePortRead(mux, stranger, fmt.Sprintf("/report/?type=user&id=%d", minor.ID)); w.Code != 404 {
			t.Fatal("cross-cohort user report page", w.Code)
		}
		if w := webCasePortRead(mux, stranger, fmt.Sprintf("/report/?type=user&id=%d", owner.ID)); w.Code != 200 {
			t.Fatal("same-cohort user report page", w.Code)
		}
		if w := webCasePortRead(mux, stranger, fmt.Sprintf("/report/?type=user&id=%d", owner.ID)); w.Code != 429 || strings.Contains(w.Body.String(), owner.DisplayName) {
			t.Fatal("user report page lookups were not budgeted", w.Code)
		}
	})
}
