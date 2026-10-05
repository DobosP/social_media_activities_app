package web

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestWebCasePort2TopicsAndGuardianControls(t *testing.T) {
	s, guardian, _, _, mux := webCasePortFixture(t)
	ctx := context.Background()
	for _, row := range []struct{ slug, name string }{{"case-port2-sport", "Case sports"}, {"case-port2-read", "Case reading"}} {
		if _, err := s.DB.Exec(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,parent_id,description,created_at,updated_at) VALUES($1,$2,NULL,'',now(),now())`, row.slug, row.name); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("self_topics_get_save_and_checked_reload", func(t *testing.T) {
		body := webCasePortHTML(t, mux, guardian, "/topics/")
		webCasePortContains(t, body, "Your topics", `value="case-port2-sport"`)
		w := webCasePort2Post(t, mux, guardian, "/topics/", "/topics/", url.Values{"topics": {"case-port2-sport"}})
		if w.Code != 302 {
			t.Fatal("self topics singleton POST", w.Code)
		}
		slugs, err := s.Recommendations.TopicSlugs(ctx, guardian)
		if err != nil || !reflect.DeepEqual(slugs, []string{"case-port2-sport"}) {
			t.Fatal("own topics not stored", err, slugs)
		}
		webCasePortContains(t, webCasePortHTML(t, mux, guardian, "/topics/"), "checked")
	})
	ward := testdb.Actor(t, s.DB, "case-port2-child-ward", "child")
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, guardian.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
	t.Run("guardian_capabilities_and_ward_legibility", func(t *testing.T) {
		body := webCasePortHTML(t, mux, guardian, "/wards/")
		webCasePortContains(t, body, "What this guardianship lets you do", "End guardianship", "I've arrived", "Suggested topics for their feed", fmt.Sprintf("/wards/%d/topics/", ward.ID))
		body = webCasePortHTML(t, mux, ward, "/guardianship/")
		webCasePortContains(t, body, guardian.Username, "What they can see about you", "doesn't have a")
		webCasePortAbsent(t, body, "/revoke/")
	})
	t.Run("child_topic_steering_and_non_guardian_preservation", func(t *testing.T) {
		path := fmt.Sprintf("/wards/%d/topics/", ward.ID)
		w := webCasePort2Post(t, mux, guardian, "/wards/", path, url.Values{"topics": {"case-port2-sport"}})
		if w.Code != 302 {
			t.Fatal("guardian topic POST", w.Code)
		}
		slugs, err := s.Recommendations.TopicSlugs(ctx, ward)
		if err != nil || !reflect.DeepEqual(slugs, []string{"case-port2-sport"}) {
			t.Fatal("ward topics not stored", err, slugs)
		}
		if _, err := s.Recommendations.SetTopics(ctx, ward, []string{"case-port2-read"}); err != nil {
			t.Fatal(err)
		}
		stranger := testdb.Actor(t, s.DB, "case-port2-guardian-stranger", "adult")
		w = webCasePort2Post(t, mux, stranger, "/wards/", path, url.Values{"topics": {"case-port2-sport"}})
		if w.Code != 302 {
			t.Fatal("non-guardian topic refusal redirect", w.Code)
		}
		slugs, err = s.Recommendations.TopicSlugs(ctx, ward)
		if err != nil || !reflect.DeepEqual(slugs, []string{"case-port2-read"}) {
			t.Fatal("non-guardian changed child feed steering", err, slugs)
		}
	})
	t.Run("non_child_topic_steering_is_refused", func(t *testing.T) {
		teen := testdb.Actor(t, s.DB, "case-port2-teen-ward", "teen")
		if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, guardian.ID, teen.ID); err != nil {
			t.Fatal(err)
		}
		w := webCasePort2Post(t, mux, guardian, "/wards/", fmt.Sprintf("/wards/%d/topics/", teen.ID), url.Values{"topics": {"case-port2-sport"}})
		if w.Code != 302 {
			t.Fatal("teen steering refusal redirect", w.Code)
		}
		slugs, err := s.Recommendations.TopicSlugs(ctx, teen)
		if err != nil || len(slugs) != 0 {
			t.Fatal("guardian changed non-child topics", err, slugs)
		}
	})
	t.Run("only_guardian_can_end_relationship", func(t *testing.T) {
		stranger := testdb.Actor(t, s.DB, "case-port2-revoke-stranger", "adult")
		path := fmt.Sprintf("/wards/%d/revoke/", ward.ID)
		w := webCasePort2Post(t, mux, stranger, "/wards/", path, url.Values{})
		if w.Code != 302 {
			t.Fatal("non-guardian revoke refusal redirect", w.Code)
		}
		var active bool
		query := `SELECT status='active' FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2`
		if err := s.DB.QueryRow(ctx, query, guardian.ID, ward.ID).Scan(&active); err != nil || !active {
			t.Fatal("stranger revoked relationship", err)
		}
		w = webCasePort2Post(t, mux, guardian, "/wards/", path, url.Values{})
		if w.Code != 302 {
			t.Fatal("own relationship revoke redirect", w.Code)
		}
		if err := s.DB.QueryRow(ctx, query, guardian.ID, ward.ID).Scan(&active); err != nil || active {
			t.Fatal("guardian relationship stayed active", err)
		}
		webCasePortAbsent(t, strings.ToLower(w.Body.String()), "permission denied")
	})
}
