package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// These are registered web routes plus real domain services/transactions on the
// existing isolated synthetic PostgreSQL fixture, not a fake permission engine.
func TestNativeFamilyHardAndSoftLimitsPreserveTheirDifferentContracts(t *testing.T) {
	s, guardian, _, _, mux := webCasePortFixture(t)
	ctx := context.Background()
	ward := testdb.Actor(t, s.DB, "generated-native-family-limits-child", "child")
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, guardian.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,parent_id,description,created_at,updated_at) VALUES('generated-native-family-sport','Generated sport',NULL,'',now(),now()),('generated-native-family-read','Generated reading',NULL,'',now(),now())`); err != nil {
		t.Fatal(err)
	}
	limits := fmt.Sprintf("/wards/%d/limits/", ward.ID)
	topics := fmt.Sprintf("/wards/%d/topics/", ward.ID)
	snapshot := func() string {
		var raw string
		if err := s.DB.QueryRow(ctx, `SELECT jsonb_build_object('limits',(SELECT to_jsonb(x) FROM accounts_guardianguardrail x JOIN accounts_guardianrelationship g ON g.id=x.relationship_id WHERE g.guardian_id=$1 AND g.ward_id=$2),'topics',(SELECT COALESCE(jsonb_agg(category_id ORDER BY category_id),'[]'::jsonb) FROM recommendations_topicpreference WHERE user_id=$2),'audit',(SELECT count(*) FROM safety_auditlog WHERE event IN('guardian.guardrail_set','recommendations.ward_topics_updated')))::text`, guardian.ID, ward.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	valid := url.Values{"earliest_start_hour": {"0"}, "latest_start_hour": {"23"}, "max_open_joins": {"50"}, "allowed_weekdays": {"7", "1", "1"}, "allowed_categories": {"generated-native-family-sport", "generated-native-family-read"}}
	if w := webCasePort2Post(t, mux, guardian, "/wards/", limits, valid); w.Code != http.StatusFound {
		t.Fatal("valid boundary form rejected", w.Code)
	}
	body := webCasePortHTML(t, mux, guardian, "/wards/")
	webCasePortContains(t, body, `value="0" selected`, `value="7" checked`, `value="1" checked`, `value="50"`)
	before := snapshot()
	for _, bad := range []url.Values{{"earliest_start_hour": {"-1"}}, {"latest_start_hour": {"24"}}, {"max_open_joins": {"0"}}, {"max_open_joins": {"51"}}, {"allowed_weekdays": {"8"}}, {"allowed_categories": {"unknown-hard-category"}}} {
		w := webCasePort2Post(t, mux, guardian, "/wards/", limits, bad)
		if w.Code != http.StatusOK || snapshot() != before || strings.Contains(w.Body.String(), "unknown-hard-category") {
			t.Fatal("invalid hard restriction was stored/echoed or did not render current panel", w.Code)
		}
	}
	tooMany := []string{}
	for i := 0; i < 101; i++ {
		tooMany = append(tooMany, fmt.Sprintf("unknown-hard-%d", i))
	}
	if w := webCasePort2Post(t, mux, guardian, "/wards/", limits, url.Values{"allowed_categories": tooMany}); w.Code != http.StatusOK || snapshot() != before {
		t.Fatal("oversized hard taxonomy selection changed state", w.Code)
	}
	if w := webCasePort2Post(t, mux, guardian, "/wards/", topics, url.Values{"topics": {"generated-native-family-sport", "unknown-soft-topic"}}); w.Code != http.StatusFound {
		t.Fatal("known-topic filtering rejected", w.Code)
	}
	slugs, err := s.Recommendations.TopicSlugs(ctx, ward)
	if err != nil || !reflect.DeepEqual(slugs, []string{"generated-native-family-sport"}) {
		t.Fatal("soft steering did not filter unknown topics", err, slugs)
	}
	if w := webCasePort2Post(t, mux, guardian, "/wards/", topics, url.Values{"topics": {"unknown-soft-topic"}}); w.Code != http.StatusFound {
		t.Fatal("all-unknown steering clear rejected", w.Code)
	}
	slugs, err = s.Recommendations.TopicSlugs(ctx, ward)
	if err != nil || len(slugs) != 0 {
		t.Fatal("all-unknown steering did not clear soft preference", err, slugs)
	}
	if w := webCasePort2Post(t, mux, guardian, "/wards/", limits, url.Values{"earliest_start_hour": {""}, "latest_start_hour": {""}, "max_open_joins": {""}}); w.Code != http.StatusFound {
		t.Fatal("explicit optional blank clear rejected", w.Code)
	}
	var clear bool
	if err := s.DB.QueryRow(ctx, `SELECT r.earliest_start_hour IS NULL AND r.latest_start_hour IS NULL AND r.max_open_joins IS NULL AND r.allowed_weekdays='' AND cardinality(r.allowed_categories)=0 FROM accounts_guardianguardrail r JOIN accounts_guardianrelationship g ON g.id=r.relationship_id WHERE g.guardian_id=$1 AND g.ward_id=$2`, guardian.ID, ward.ID).Scan(&clear); err != nil || !clear {
		t.Fatal("blank limits became zero/default or retained previous restrictions", err)
	}
}

func TestNativeFamilyMandatoryAuditFailureRollsBackAllThreeMutations(t *testing.T) {
	s, guardian, _, _, mux := webCasePortFixture(t)
	ctx := context.Background()
	ward := testdb.Actor(t, s.DB, "generated-native-family-rollback-child", "child")
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, guardian.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,parent_id,description,created_at,updated_at) VALUES('generated-native-family-rollback','Generated rollback topic',NULL,'',now(),now())`); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var raw string
		if err := s.DB.QueryRow(ctx, `SELECT jsonb_build_object('relation',(SELECT to_jsonb(g) FROM accounts_guardianrelationship g WHERE g.guardian_id=$1 AND g.ward_id=$2),'consent',(SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY c.id),'[]'::jsonb) FROM accounts_parentalconsent c WHERE c.minor_id=$2),'limits',(SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.id),'[]'::jsonb) FROM accounts_guardianguardrail r JOIN accounts_guardianrelationship g ON g.id=r.relationship_id WHERE g.guardian_id=$1 AND g.ward_id=$2),'topics',(SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.id),'[]'::jsonb) FROM recommendations_topicpreference p WHERE p.user_id=$2),'audit',(SELECT count(*) FROM safety_auditlog))::text`, guardian.ID, ward.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if _, err := s.DB.Exec(ctx, `CREATE FUNCTION generated_native_family_audit_refusal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event IN ('guardian.guardrail_set','recommendations.ward_topics_updated','guardian.revoked') THEN RAISE EXCEPTION 'PRIVATE-SYNTHETIC-AUDIT-REFUSAL'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := s.DB.Exec(context.Background(), `DROP TRIGGER IF EXISTS generated_native_family_audit_refusal ON safety_auditlog; DROP FUNCTION IF EXISTS generated_native_family_audit_refusal()`); err != nil {
			t.Error(err)
		}
	})
	if _, err := s.DB.Exec(ctx, `CREATE TRIGGER generated_native_family_audit_refusal BEFORE INSERT ON safety_auditlog FOR EACH ROW EXECUTE FUNCTION generated_native_family_audit_refusal()`); err != nil {
		t.Fatal(err)
	}
	before := snapshot()
	for _, probe := range []struct {
		action string
		values url.Values
	}{{"limits", url.Values{"max_open_joins": {"2"}}}, {"topics", url.Values{"topics": {"generated-native-family-rollback"}}}, {"revoke", url.Values{}}} {
		w := webCasePort2Post(t, mux, guardian, "/wards/", fmt.Sprintf("/wards/%d/%s/", ward.ID, probe.action), probe.values)
		if w.Code != http.StatusOK || snapshot() != before || strings.Contains(w.Body.String(), "PRIVATE-SYNTHETIC-AUDIT-REFUSAL") || strings.Contains(w.Body.String(), "SQLSTATE") {
			t.Fatal("mandatory audit failure leaked or committed a partial mutation", probe.action, w.Code)
		}
	}
}
