package web

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestWebCasePort3CreateJoinVoteAndReportThroughRegisteredForms(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	t.Run("create_then_render_detail", func(t *testing.T) {
		values := url.Values{"place": {fmt.Sprint(place)}, "activity_type": {fmt.Sprint(typ)}, "title": {"Case port3 web game"}, "description": {""}, "starts_at": {"2030-01-01T10:00"}, "ends_at": {""}, "capacity": {""}}
		w := webCasePort2Post(t, mux, owner, "/activities/new/", "/activities/new/", values)
		if w.Code != 302 {
			t.Fatal("source create form redirect", w.Code, w.Body.String())
		}
		webCasePortContains(t, webCasePortHTML(t, mux, owner, w.Header().Get("Location")), "Case port3 web game")
	})
	activity := socialLegacyActivity(t, s, owner, place, typ, "Case port3 join/report")
	peer := testdb.Actor(t, s.DB, "case-port3-joining-peer", "adult")
	t.Run("join_request_then_owner_vote_admits", func(t *testing.T) {
		path := fmt.Sprintf("/activities/%d/", activity)
		w := webCasePort2Post(t, mux, peer, path, path+"join/", url.Values{})
		if w.Code != 302 {
			t.Fatal("source join redirect", w.Code)
		}
		var membership int64
		var state string
		if err := s.DB.QueryRow(ctx, `SELECT id,state FROM social_membership WHERE activity_id=$1 AND user_id=$2`, activity, peer.ID).Scan(&membership, &state); err != nil || state != "requested" {
			t.Fatal("source pending membership", err, state)
		}
		w = webCasePort2Post(t, mux, owner, path, fmt.Sprintf("%smembers/%d/vote/", path, membership), url.Values{"vote": {"approve"}})
		if w.Code != 302 {
			t.Fatal("source owner vote redirect", w.Code)
		}
		if err := s.DB.QueryRow(ctx, `SELECT state FROM social_membership WHERE id=$1`, membership).Scan(&state); err != nil || state != "member" {
			t.Fatal("source owner vote did not admit", err, state)
		}
	})
	t.Run("same_cohort_activity_report", func(t *testing.T) {
		// The original reporter is a same-cohort stranger, not the joining
		// member above. Reporting a visible activity must preserve that floor.
		reporter := testdb.Actor(t, s.DB, "case-port3-nonmember-reporter", "adult")
		form := fmt.Sprintf("/report/?type=activity&id=%d", activity)
		webCasePortHTML(t, mux, reporter, form)
		w := webCasePort2Post(t, mux, reporter, form, "/report/", url.Values{"type": {"activity"}, "id": {fmt.Sprint(activity)}, "reason": {"spam"}, "detail": {"Source report detail"}})
		if w.Code != 302 {
			t.Fatal("source report redirect", w.Code, w.Body.String())
		}
		var count int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_report WHERE reporter_id=$1 AND reason='spam' AND detail='Source report detail'`, reporter.ID).Scan(&count); err != nil || count != 1 {
			t.Fatal("registered form did not create subject report", err, count)
		}
	})
}

func TestWebCasePort3BlockingRoundTripsAndSafeNext(t *testing.T) {
	s, actor, _, _, mux := webCasePortFixture(t)
	ctx := context.Background()
	peer := testdb.Actor(t, s.DB, "case-port3-block-peer", "adult")
	block, unblock := fmt.Sprintf("/users/%d/block/", peer.ID), fmt.Sprintf("/users/%d/unblock/", peer.ID)
	t.Run("block_then_unblock", func(t *testing.T) {
		for _, step := range []struct {
			path string
			want int
		}{{block, 1}, {unblock, 0}} {
			w := webCasePort2Post(t, mux, actor, "/profile/", step.path, url.Values{})
			if w.Code != 302 {
				t.Fatal("source block mutation redirect", w.Code)
			}
			var count int
			if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_block WHERE blocker_id=$1 AND blocked_id=$2`, actor.ID, peer.ID).Scan(&count); err != nil || count != step.want {
				t.Fatal("source block state", err, count)
			}
		}
	})
	for _, test := range []struct{ name, path, next, want string }{{"offsite_block", block, "https://evil.invalid/phish", "/"}, {"safe_relative_block", block, "/activities/", "/activities/"}, {"protocol_relative_unblock", unblock, "//evil.invalid/x", "/profile/"}} {
		t.Run(test.name, func(t *testing.T) {
			w := webCasePort2Post(t, mux, actor, "/profile/", test.path, url.Values{"next": {test.next}})
			if w.Code != 302 || w.Header().Get("Location") != test.want || strings.Contains(w.Header().Get("Location"), "evil.invalid") {
				t.Fatal("source safe return target", w.Code, w.Header().Get("Location"))
			}
		})
	}
}

func TestWebCasePort3SeriesNextNoteOwnerBoundary(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	series, err := s.Social.CreateSeries(ctx, owner, social.SeriesInput{ActivityInput: social.ActivityInput{Place: place, ActivityType: typ, Title: "Case port3 weekly run"}, Cadence: "weekly", FirstStartsAt: time.Now().Add(48 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/activities/series/%d/", series)
	t.Run("owner_stages_exact_note", func(t *testing.T) {
		webCasePortContains(t, webCasePortHTML(t, mux, owner, path), "Heads-up for the next meetup")
		w := webCasePort2Post(t, mux, owner, path, path+"next-note/", url.Values{"next_instance_note": {"Bring cleats this week."}})
		if w.Code != 302 {
			t.Fatal("source next-note redirect", w.Code)
		}
		var note string
		if err := s.DB.QueryRow(ctx, `SELECT next_instance_note FROM social_activityseries WHERE id=$1`, series).Scan(&note); err != nil || note != "Bring cleats this week." {
			t.Fatal("source staged note", err, note)
		}
	})
	t.Run("non_owner_cannot_stage", func(t *testing.T) {
		if _, err := s.DB.Exec(ctx, `UPDATE social_activityseries SET next_instance_note='' WHERE id=$1`, series); err != nil {
			t.Fatal(err)
		}
		stranger := testdb.Actor(t, s.DB, "case-port3-series-stranger", "adult")
		w := webCasePort2Post(t, mux, stranger, "/profile/", path+"next-note/", url.Values{"next_instance_note": {"sneaky"}})
		if w.Code != 404 {
			t.Fatal("source non-owner next-note wall", w.Code)
		}
		var note string
		if err := s.DB.QueryRow(ctx, `SELECT next_instance_note FROM social_activityseries WHERE id=$1`, series).Scan(&note); err != nil || note != "" {
			t.Fatal("stranger changed staged note", err, note)
		}
	})
}
