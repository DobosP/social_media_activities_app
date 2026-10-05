package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/discovery"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// Compare populated small/large fixtures through the actual services and HTML
// presentation adapters. Every pool connection records counts only; neither SQL
// arguments nor private fixture payloads are retained by the instrumentation.
func TestRetirementPostgresWebQueryGrowth(t *testing.T) {
	s, owner, place, typ := socialLegacyFixture(t)
	db, trace := testdb.TracedPool(t, s.DB)
	s.DB = db
	s.Social = social.New(db, platform.RecordAudit)
	s.Social.Avatar = accounts.Avatar
	s.Social.BodyMarkup = BodyMarkup
	s.Media = media.NewService(db, nil, nil, media.TokenCodec{Key: bytes.Repeat([]byte{9}, 32)}, s.Social)
	s.Social.ActivityVisuals = s.Media.ActivityVisuals
	s.Social.ActivityVisual = s.Media.ActivityVisual
	s.Catalog = catalog.New(db)
	s.Catalog.PlaceVisuals = s.Media.PlaceVisuals
	s.Recommendations = recommendations.New(db, s.Catalog, s.Social)
	s.Social.AfterActivitySave = s.Recommendations.RecomputeEmbeddingTx
	s.Discovery = discovery.New(db, s.Catalog, s.Social, s.Recommendations)
	s.Safety = safety.New(db, safety.Config{CanSeeUser: s.Social.CanSeeUser, Messaging: s.Messaging})
	s.Accounts = accounts.New(db, nil, "synthetic-query-growth-binding", accounts.Config{})
	viewer := testdb.Actor(t, db, "query-growth-viewer", "adult")
	threadActivity := socialLegacyActivity(t, s, owner, place, typ, "Query growth thread")
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,'member','member','unknown','none',false,now(),now(),now())`, threadActivity, viewer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE places_place SET address_city='Query growth city' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	// Exercise genuine distinct authors and interest nodes instead of repeating
	// one cached user, and genuine published correction overlays on many venues.
	seed := func(begin, end int) {
		for i := begin; i < end; i++ {
			activity, err := s.Social.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: fmt.Sprintf("Query growth meetup %02d", i), StartsAt: time.Now().Add(24 * time.Hour), BeginnersWelcome: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(ctx, `UPDATE social_activity SET is_publicly_listed=true WHERE id=$1`, activity); err != nil {
				t.Fatal(err)
			}
			peer := testdb.Actor(t, db, fmt.Sprintf("query-growth-author-%02d", i), "adult")
			if _, err = db.Exec(ctx, `INSERT INTO recommendations_userinterest(user_id,activity_type_id,created_at) VALUES($1,$2,now())`, peer.ID, typ); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,'member','member','unknown','none',false,now(),now(),now())`, threadActivity, peer.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Social.WritePost(ctx, peer, "activity", threadActivity, social.PostInput{Body: fmt.Sprintf("Query growth post %02d", i)}, false); err != nil {
				t.Fatal(err)
			}
			ownPost, writeErr := s.Social.WritePost(ctx, owner, "activity", threadActivity, social.PostInput{Body: fmt.Sprintf("Query growth own portability post %02d", i)}, false)
			if writeErr != nil {
				t.Fatal(writeErr)
			}
			if moderationHidden, err := s.Social.DeletePost(ctx, owner, ownPost); err != nil || moderationHidden {
				t.Fatal("ordinary author deletion acquired moderation state", err)
			}
			var deleted bool
			if err := db.QueryRow(ctx, `SELECT is_hidden AND is_author_deleted AND author_id=$2 FROM social_post WHERE id=$1`, ownPost, owner.ID).Scan(&deleted); err != nil || !deleted {
				t.Fatal("fixture did not create an actual inactive author-owned post", err)
			}
			venue := testdb.Place(t, db, fmt.Sprintf("Query growth venue %02d", i), "osm")
			if _, err = db.Exec(ctx, `UPDATE places_place SET address_city='Query growth city' WHERE id=$1`, venue); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(ctx, `INSERT INTO places_placecorrection(place_id,proposer_id,field,proposed_value,required_confirmations,status,created_at,published_at) VALUES($1,$2,'name',$3,3,'published',now(),now())`, venue, owner.ID, fmt.Sprintf("Corrected query venue %02d", i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	type check struct {
		name  string
		read  func() error
		small int64
	}
	expectedRows := 4
	checks := []check{
		{name: "organizer_domain", read: func() error {
			v, e := s.Social.OrganizerConsole(ctx, owner)
			if e == nil && len(v["activities"].([]json.RawMessage)) < expectedRows {
				t.Fatal("organizer fixture empty")
			}
			return e
		}},
		{name: "organizer_html", read: func() error {
			r := socialLegacyRequest("GET", "/organize/", owner, 0, nil)
			_, _, _, e := s.SocialView(r, owner, "organize")
			return e
		}},
		{name: "home_feed", read: func() error {
			v, e := s.Discovery.HomeFeed(ctx, viewer, catalog.Near{})
			if e == nil && len(v["recommended"].([]map[string]any)) < min(expectedRows, 8) {
				t.Fatal("growing feed silently dropped recommended fixtures")
			}
			return e
		}},
		{name: "discovery_cards", read: func() error {
			v, e := s.Discovery.ActivityCards(ctx, viewer, catalog.Near{}, "", false, false, nil, nil, 100)
			if e == nil && len(v) < expectedRows {
				t.Fatal("discovery fixture empty")
			}
			return e
		}},
		{name: "thread_domain", read: func() error {
			v, _, e := s.Social.Posts(ctx, viewer, "activity", threadActivity, 0, 100)
			if e == nil && len(v) < expectedRows {
				t.Fatal("thread fixture empty")
			}
			return e
		}},
		{name: "thread_html", read: func() error {
			r := socialLegacyRequest("GET", fmt.Sprintf("/activities/%d/", threadActivity), viewer, threadActivity, nil)
			_, _, _, e := s.SocialView(r, viewer, "activity_detail")
			return e
		}},
		{name: "corrections_public_venues", read: func() error {
			r := socialLegacyRequest("GET", "/places/list/?city=Query+growth+city", viewer, 0, nil)
			v, _, e := s.publicPlaces(r, viewer, "", "", 100)
			if e == nil && len(v) < expectedRows {
				t.Fatal("venue fixture empty")
			}
			return e
		}},
		{name: "portability_export", read: func() error {
			v, e := s.Accounts.Export(ctx, owner, true)
			if e == nil && len(v["thread_posts"].(map[string]any)["items"].([]json.RawMessage)) != expectedRows {
				t.Fatal("growing subject export dropped author-deleted posts")
			}
			return e
		}},
	}
	seed(0, 4)
	for i := range checks {
		trace.Reset()
		if err := checks[i].read(); err != nil {
			t.Fatal(checks[i].name, err)
		}
		checks[i].small = trace.Count()
		if checks[i].small == 0 {
			t.Fatal("untraced fixture read", checks[i].name)
		}
	}
	seed(4, 28)
	expectedRows = 28
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			trace.Reset()
			if err := c.read(); err != nil {
				t.Fatal(err)
			}
			large := trace.Count()
			t.Logf("4 -> 28 rows: queries %d -> %d", c.small, large)
			if large > c.small+2 {
				t.Fatalf("per-record query growth: small=%d large=%d", c.small, large)
			}
		})
	}
}
