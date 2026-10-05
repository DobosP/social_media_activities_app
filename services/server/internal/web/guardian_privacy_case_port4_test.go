package web

import (
	"context"
	"reflect"
	"strconv"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func privacyCase4Int(value int) *int { return &value }

func privacyCase4Link(t *testing.T, s *Server, g, w platform.Actor) {
	t.Helper()
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,status,relationship,consent_id,created_at,updated_at) VALUES($1,$2,'active','parent',NULL,now(),now())`, g.ID, w.ID); err != nil {
		t.Fatal(err)
	}
}

func privacyCase4Effective(t *testing.T, s *Server, ward int64) social.EffectiveGuardrail {
	t.Helper()
	rail, err := s.Social.EffectiveGuardrail(context.Background(), ward)
	if err != nil {
		t.Fatal(err)
	}
	return rail
}

func privacyCase4Capabilities(t *testing.T, s *Server, g, w platform.Actor) map[string]any {
	t.Helper()
	caps, err := s.accountCapabilities(context.Background(), g.ID, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	return caps
}

func TestPrivacyCasePort4GuardrailEffectiveDefaultsStrictestAndRevokedExclusion(t *testing.T) {
	s, g1 := accountWebFixture(t)
	ctx := context.Background()
	g2 := testdb.Actor(t, s.DB, "privacy-case4-co-guardian", "adult")
	w := testdb.Actor(t, s.DB, "privacy-case4-calendar-child", "child")
	privacyCase4Link(t, s, g1, w)
	privacyCase4Link(t, s, g2, w)
	if rail := privacyCase4Effective(t, s, w.ID); !reflect.DeepEqual(rail, social.EffectiveGuardrail{}) {
		t.Fatal("absent rail created restrictions")
	}
	if err := s.Accounts.SetGuardianGuardrail(ctx, g1, w.ID, accounts.GuardrailInput{}); err != nil {
		t.Fatal(err)
	}
	if rail := privacyCase4Effective(t, s, w.ID); rail.Supervised || rail.Latest != nil || rail.Earliest != nil || rail.Cap != nil || rail.Weekdays != nil || rail.Categories != nil {
		t.Fatal("explicit empty rail became block-all")
	}
	if err := s.Accounts.SetGuardianGuardrail(ctx, g1, w.ID, accounts.GuardrailInput{Latest: privacyCase4Int(0)}); err != nil {
		t.Fatal(err)
	}
	if rail := privacyCase4Effective(t, s, w.ID); rail.Latest == nil || *rail.Latest != 0 {
		t.Fatal("hourzero was treated as unset")
	}
	if err := s.Accounts.SetGuardianGuardrail(ctx, g1, w.ID, accounts.GuardrailInput{Latest: privacyCase4Int(20), MaxOpen: privacyCase4Int(5)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.SetGuardianGuardrail(ctx, g2, w.ID, accounts.GuardrailInput{SupervisedOnly: true, Latest: privacyCase4Int(18), MaxOpen: privacyCase4Int(3)}); err != nil {
		t.Fatal(err)
	}
	rail := privacyCase4Effective(t, s, w.ID)
	if !rail.Supervised || rail.Latest == nil || *rail.Latest != 18 || rail.Cap == nil || *rail.Cap != 3 || rail.Earliest != nil || rail.Weekdays != nil || rail.Categories != nil {
		t.Fatal("co-guardian intersection did not select strictest source limits")
	}
	if _, err := s.DB.Exec(ctx, `DELETE FROM accounts_guardianguardrail WHERE relationship_id IN(SELECT id FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2)`, g2.ID, w.ID); err != nil {
		t.Fatal(err)
	}
	rail = privacyCase4Effective(t, s, w.ID)
	if rail.Latest == nil || *rail.Latest != 20 {
		t.Fatal("guardian without rail loosened another guardian")
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_guardianrelationship SET status='revoked' WHERE guardian_id=$1 AND ward_id=$2`, g1.ID, w.ID); err != nil {
		t.Fatal(err)
	}
	if rail := privacyCase4Effective(t, s, w.ID); !reflect.DeepEqual(rail, social.EffectiveGuardrail{}) {
		t.Fatal("revoked guardian's persisted rail remained effective")
	}
}

func TestPrivacyCasePort4GuardrailCalendarCategoryIntersectionAndCapabilities(t *testing.T) {
	s, g1 := accountWebFixture(t)
	ctx := context.Background()
	g2 := testdb.Actor(t, s.DB, "privacy-case4-envelope-co-guardian", "adult")
	w := testdb.Actor(t, s.DB, "privacy-case4-envelope-child", "child")
	privacyCase4Link(t, s, g1, w)
	privacyCase4Link(t, s, g2, w)
	for _, slug := range []string{"privacy-case4-sport", "privacy-case4-reading"} {
		if _, err := s.DB.Exec(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,parent_id,description,created_at,updated_at) VALUES($1,$1,NULL,'',now(),now())`, slug); err != nil {
			t.Fatal(err)
		}
	}
	set := func(g platform.Actor, in accounts.GuardrailInput) {
		t.Helper()
		if err := s.Accounts.SetGuardianGuardrail(ctx, g, w.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	set(g1, accounts.GuardrailInput{Weekdays: []string{"3", "1", "3", "5"}, Categories: []string{"privacy-case4-sport", "privacy-case4-reading", "privacy-case4-sport"}})
	rail := privacyCase4Effective(t, s, w.ID)
	if !reflect.DeepEqual(rail.Weekdays, map[int]bool{1: true, 3: true, 5: true}) || !reflect.DeepEqual(rail.Categories, map[string]bool{"privacy-case4-sport": true, "privacy-case4-reading": true}) {
		t.Fatal("canonical effective calendar/category sets drifted")
	}
	caps := privacyCase4Capabilities(t, s, g1, w)
	if !reflect.DeepEqual(caps["guardrail_allowed_weekday_ints"], []int{1, 3, 5}) || !reflect.DeepEqual(caps["guardrail_allowed_categories"], []string{"privacy-case4-reading", "privacy-case4-sport"}) || caps["guardrail_combined_blocks_all"] != false {
		t.Fatal("canonical source capability projection drifted")
	}
	set(g1, accounts.GuardrailInput{Weekdays: []string{"1", "6"}, Earliest: privacyCase4Int(9)})
	caps = privacyCase4Capabilities(t, s, g1, w)
	if !reflect.DeepEqual(caps["guardrail_allowed_weekday_ints"], []int{1, 6}) || caps["guardrail_earliest_start_hour"] != 9 || caps["guardrail_combined_blocks_all"] != false {
		t.Fatal("family-calendar capability fields absent")
	}
	set(g1, accounts.GuardrailInput{Weekdays: []string{"1", "2"}})
	set(g2, accounts.GuardrailInput{Weekdays: []string{"3", "4"}})
	rail = privacyCase4Effective(t, s, w.ID)
	if rail.Weekdays == nil || len(rail.Weekdays) != 0 {
		t.Fatal("disjoint weekdays widened empty intersection")
	}
	for _, g := range []platform.Actor{g1, g2} {
		if privacyCase4Capabilities(t, s, g, w)["guardrail_combined_blocks_all"] != true {
			t.Fatal("each guardian panel hid combined weekday block-all")
		}
	}
	set(g1, accounts.GuardrailInput{Earliest: privacyCase4Int(8)})
	set(g2, accounts.GuardrailInput{Earliest: privacyCase4Int(10)})
	if rail := privacyCase4Effective(t, s, w.ID); rail.Earliest == nil || *rail.Earliest != 10 {
		t.Fatal("earliest hour combination did not take latest minimum")
	}
	set(g1, accounts.GuardrailInput{Earliest: privacyCase4Int(20)})
	set(g2, accounts.GuardrailInput{Latest: privacyCase4Int(10)})
	if privacyCase4Capabilities(t, s, g1, w)["guardrail_combined_blocks_all"] != true {
		t.Fatal("inverted hour window not legible as block-all")
	}
	set(g1, accounts.GuardrailInput{Categories: []string{"privacy-case4-sport"}})
	set(g2, accounts.GuardrailInput{Categories: []string{"privacy-case4-reading"}})
	if rail := privacyCase4Effective(t, s, w.ID); rail.Categories == nil || len(rail.Categories) != 0 {
		t.Fatal("disjoint categories widened empty intersection")
	}
	if privacyCase4Capabilities(t, s, g1, w)["guardrail_combined_blocks_all"] != true {
		t.Fatal("disjoint category block-all not projected")
	}
	set(g2, accounts.GuardrailInput{})
	if rail := privacyCase4Effective(t, s, w.ID); !reflect.DeepEqual(rail.Categories, map[string]bool{"privacy-case4-sport": true}) {
		t.Fatal("guardian without categories loosened restricted peer")
	}
	set(g1, accounts.GuardrailInput{})
	if rail := privacyCase4Effective(t, s, w.ID); rail.Weekdays != nil || rail.Categories != nil {
		t.Fatal("empty category/weekday lists became block-all")
	}
}

func TestPrivacyCasePort4GuardrailChildTeenCapabilitiesAndDurableAudit(t *testing.T) {
	s, g := accountWebFixture(t)
	ctx := context.Background()
	w := testdb.Actor(t, s.DB, "privacy-case4-caps-child", "child")
	teen := testdb.Actor(t, s.DB, "privacy-case4-caps-teen", "teen")
	privacyCase4Link(t, s, g, w)
	privacyCase4Link(t, s, g, teen)
	if err := s.Accounts.SetGuardianGuardrail(ctx, g, w.ID, accounts.GuardrailInput{SupervisedOnly: true, Latest: privacyCase4Int(19), MaxOpen: privacyCase4Int(2)}); err != nil {
		t.Fatal(err)
	}
	caps := privacyCase4Capabilities(t, s, g, w)
	if caps["can_set_guardrails"] != true || caps["guardrail_supervised_only"] != true || caps["guardrail_latest_start_hour"] != 19 || caps["guardrail_max_open_joins"] != 2 {
		t.Fatal("child guardian capability fields drifted")
	}
	caps = privacyCase4Capabilities(t, s, g, teen)
	if caps["can_set_guardrails"] != false || caps["guardrail_supervised_only"] != false || caps["guardrail_latest_start_hour"] != nil {
		t.Fatal("teen gained child-only guardrail affordance")
	}
	var actor int64
	var target string
	if err := s.DB.QueryRow(ctx, `SELECT actor_ref,target_ref FROM safety_auditlog WHERE event='guardian.guardrail_set' ORDER BY id DESC LIMIT 1`).Scan(&actor, &target); err != nil || actor != g.ID || target != fmtTarget(w.ID) {
		t.Fatal("actual guardrail audit actor/target missing", err)
	}
}

func fmtTarget(id int64) string { return "accounts.user:" + strconv.FormatInt(id, 10) }
