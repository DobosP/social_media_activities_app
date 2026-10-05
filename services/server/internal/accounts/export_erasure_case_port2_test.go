package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func casePort2Export(t *testing.T, s *Service, a platform.Actor) map[string]any {
	t.Helper()
	payload, err := s.Export(context.Background(), a, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func casePort2Link(t *testing.T, s *Service, guardian, ward platform.Actor) {
	t.Helper()
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,status,relationship,consent_id,created_at,updated_at) VALUES($1,$2,'active','parent',NULL,now(),now())`, guardian.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
}

func casePort2Exists(t *testing.T, s *Service, a platform.Actor, want bool) {
	t.Helper()
	var exists bool
	if err := s.DB.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM accounts_user WHERE id=$1)`, a.ID).Scan(&exists); err != nil || exists != want {
		t.Fatal("account erasure authority/result mismatch", err)
	}
}

func TestCasePort2ErasureAuthorityAndActualDeleteHandlers(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	for _, scenario := range []string{"stranger-adult", "stranger-minor", "guardian-service", "self-delete", "ward-delete", "non-guardian-ward-delete"} {
		t.Run(scenario, func(t *testing.T) {
			a := accountUser(t, s, "case2-erase-actor-"+scenario, "adult", "adult")
			band, cohort := "adult", "adult"
			if scenario != "stranger-adult" && scenario != "self-delete" {
				band, cohort = "under_16", "child"
			}
			b := accountUser(t, s, "case2-erase-target-"+scenario, band, cohort)
			if scenario == "self-delete" {
				b = a
			}
			if scenario == "guardian-service" || scenario == "ward-delete" {
				casePort2Link(t, s, a, b)
			}
			denied := strings.HasPrefix(scenario, "stranger-") || scenario == "non-guardian-ward-delete"
			switch scenario {
			case "self-delete":
				if out := accountRequest(s, a, "DELETE", "/api/accounts/me/", ""); out.Code != 204 {
					t.Fatal("self deletion handler failed", out.Code)
				}
			case "ward-delete", "non-guardian-ward-delete":
				want := 204
				if denied {
					want = 403
				}
				if out := accountRequest(s, a, "DELETE", "/api/accounts/wards/"+b.PublicID+"/", ""); out.Code != want {
					t.Fatal("ward deletion handler authority mismatch", out.Code)
				}
			default:
				err := s.Erase(ctx, a, b)
				if denied && !errors.Is(err, platform.ErrForbidden) || !denied && err != nil {
					t.Fatal("erasure service authority mismatch", err)
				}
			}
			casePort2Exists(t, s, b, denied)
			if a.ID != b.ID {
				casePort2Exists(t, s, a, true)
			}
			var count int
			if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event='account.erased' AND data->>'erased_public_id'=$1`, b.PublicID).Scan(&count); err != nil || count != map[bool]int{true: 0, false: 1}[denied] {
				t.Fatal("denied/committed erasure audit mismatch", err)
			}
			if scenario == "guardian-service" {
				var actor int64
				if err := s.DB.QueryRow(ctx, `SELECT actor_id FROM safety_auditlog WHERE event='account.erased' AND data->>'erased_public_id'=$1`, b.PublicID).Scan(&actor); err != nil || actor != a.ID {
					t.Fatal("ward erasure audit lost guardian actor", err)
				}
			}
		})
	}
}

func TestCasePort2ErasingGuardianRevokesOrphanConsent(t *testing.T) {
	s := accountFixture(t)
	s.Config.AllowMinorOnboarding = true
	ctx := context.Background()
	g := accountUser(t, s, "case2-orphan-guardian", "adult", "adult")
	w := accountUser(t, s, "case2-orphan-ward", "under_16", "child")
	token := casePortGuardianInvite(t, s, g, w)
	if out := accountRequest(s, w, "POST", "/api/accounts/guardian-links/"+token+"/accept/", `{}`); out.Code != 200 {
		t.Fatal("ward ceremony failed")
	}
	if out := accountRequest(s, g, "POST", "/api/accounts/wards/"+w.PublicID+"/consent/", `{}`); out.Code != 201 {
		t.Fatal("current guardian grant failed")
	}
	if err := platform.Participate(ctx, s.DB, w); err != nil {
		t.Fatal("grant lacked initial eligibility", err)
	}
	if err := s.Erase(ctx, g, g); err != nil {
		t.Fatal(err)
	}
	casePort2Exists(t, s, g, false)
	casePort2Exists(t, s, w, true)
	if err := platform.Participate(ctx, s.DB, w); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("erased guardian's string grant retained eligibility", err)
	}
	var active int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_parentalconsent WHERE minor_id=$1 AND status='active'`, w.ID).Scan(&active); err != nil || active != 0 {
		t.Fatal("orphan active consent survived", err)
	}
}

func TestCasePort2ExportShapeOptionalPrivacyAndOwnBlocks(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	a := accountUser(t, s, "case2-export-owner", "adult", "adult")
	b := accountUser(t, s, "case2-export-blocked", "adult", "adult")
	c := accountUser(t, s, "case2-export-other", "adult", "adult")
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'dev','fixture','adult',now(),NULL,'{}','')`, a.ID); err != nil {
		t.Fatal(err)
	}
	payload := casePort2Export(t, s, a)
	want := []string{"schema_version", "generated_at", "profile", "age_assurance", "consents", "guardianships", "memberships", "owned_activities", "owned_groups", "group_memberships", "thread_posts", "donations", "api_access", "safety_record", "blocks", "privacy_settings", "own_sentiment_actions"}
	got := make([]string, 0, len(payload))
	for key := range payload {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) || payload["schema_version"] != float64(5) {
		t.Fatal("export schema/allowlist mismatch")
	}
	profile := payload["profile"].(map[string]any)
	if profile["username"] != a.Username || profile["cohort"] != "adult" || profile["age_band"] != "adult" {
		t.Fatal("export profile scope mismatch")
	}
	if payload["age_assurance"].([]any)[0].(map[string]any)["provider"] != "dev" {
		t.Fatal("own assurance provider missing")
	}
	access := payload["api_access"].(map[string]any)
	if !reflect.DeepEqual(access, map[string]any{"api_token_issued": false, "issued_at": nil}) {
		t.Fatal("token metadata-only empty shape drift")
	}
	if !reflect.DeepEqual(payload["own_sentiment_actions"], map[string]any{"reactions": []any{}, "dissents": []any{}, "concerns": []any{}}) {
		t.Fatal("empty own sentiment shape drift")
	}
	if !reflect.DeepEqual(payload["thread_posts"], map[string]any{"items": []any{}, "total": float64(0), "truncated": false}) {
		t.Fatal("never-posted export was not empty")
	}
	raw, _ := json.Marshal(payload)
	if strings.Contains(string(raw), "birth_date") {
		t.Fatal("export exposed birth date")
	}
	privacy := payload["privacy_settings"].(map[string]any)
	if !reflect.DeepEqual(privacy["muted_notification_kinds"], []any{}) || privacy["access_preferences"] != nil {
		t.Fatal("unset access preference was invented")
	}
	if out := accountRequest(s, a, "PUT", "/api/accounts/me/settings/", `{"muted_kinds":["event_reminder"],"access":{"needs_step_free":true}}`); out.Code != 200 {
		t.Fatal("fixture privacy setting failed", out.Code)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now()),($3,$1,now())`, a.ID, b.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	payload = casePort2Export(t, s, a)
	privacy = payload["privacy_settings"].(map[string]any)
	if !reflect.DeepEqual(privacy["muted_notification_kinds"], []any{"event_reminder"}) || privacy["access_preferences"].(map[string]any)["needs_step_free"] != true {
		t.Fatal("own privacy preferences omitted")
	}
	blocks := payload["blocks"].([]any)
	if len(blocks) != 1 {
		t.Fatal("export included other actor's blocks")
	}
	block := blocks[0].(map[string]any)
	if block["blocked"] != b.DisplayName || block["blocked_public_id"] != b.PublicID || block["created_at"] == nil {
		t.Fatal("own block projection lost reviewed fields")
	}
}

func TestCasePort2ExportGuardianAndAPIWalls(t *testing.T) {
	s := accountFixture(t)
	s.Config.AllowMinorOnboarding = true
	g := accountUser(t, s, "case2-export-guardian", "adult", "adult")
	w := accountUser(t, s, "case2-export-ward", "under_16", "child")
	stranger := accountUser(t, s, "case2-export-stranger", "adult", "adult")
	token := casePortGuardianInvite(t, s, g, w)
	if out := accountRequest(s, w, "POST", "/api/accounts/guardian-links/"+token+"/accept/", `{}`); out.Code != 200 {
		t.Fatal("ward ceremony failed")
	}
	if out := accountRequest(s, g, "POST", "/api/accounts/wards/"+w.PublicID+"/consent/", `{}`); out.Code != 201 {
		t.Fatal("guardian consent failed")
	}
	ward := casePort2Export(t, s, w)
	if ward["consents"].(map[string]any)["as_minor"].([]any)[0].(map[string]any)["status"] != "active" || ward["guardianships"].(map[string]any)["guarded_by"].([]any)[0].(map[string]any)["guardian_public_id"] != g.PublicID {
		t.Fatal("ward's own consent/link metadata omitted")
	}
	guardian := casePort2Export(t, s, g)
	if guardian["guardianships"].(map[string]any)["as_guardian_of"].([]any)[0].(map[string]any)["ward_public_id"] != w.PublicID {
		t.Fatal("guardian's own link metadata omitted")
	}
	for _, scenario := range []struct {
		name, path string
		actor      platform.Actor
		status     int
		username   string
	}{
		{"anonymous-self", "/api/accounts/me/export/", platform.Actor{}, 401, ""},
		{"self", "/api/accounts/me/export/", g, 200, g.Username},
		{"guardian-ward", "/api/accounts/wards/" + w.PublicID + "/export/", g, 200, w.Username},
		{"non-guardian", "/api/accounts/wards/" + w.PublicID + "/export/", stranger, 403, ""},
		{"unknown-ward", "/api/accounts/wards/00000000-0000-0000-0000-000000000042/export/", g, 404, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			out := accountRequest(s, scenario.actor, "GET", scenario.path, "")
			if out.Code != scenario.status {
				t.Fatal("export authority/status mismatch", out.Code)
			}
			if scenario.status == 200 && accountJSON(t, out)["profile"].(map[string]any)["username"] != scenario.username {
				t.Fatal("export transport disclosed wrong subject")
			}
		})
	}
}

func casePort2Activity(t *testing.T, s *Service, a platform.Actor, title string) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	var category, kind, place, activity, thread int64
	slug := fmt.Sprintf("case2-type-%d", a.ID)
	if err := s.DB.QueryRow(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,description,created_at,updated_at,parent_id) VALUES($1,'Synthetic category','',now(),now(),NULL) ON CONFLICT(slug) DO UPDATE SET name=excluded.name RETURNING id`, slug).Scan(&category); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `INSERT INTO taxonomy_activitytype(slug,name,aliases,is_active,created_at,updated_at,category_id,parent_id,family_friendly,wellness) VALUES($1,'Synthetic type','[]',true,now(),now(),$2,NULL,true,false) ON CONFLICT(slug) DO UPDATE SET name=excluded.name RETURNING id`, slug, category).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES('Synthetic hall','osm','',NULL,'',ST_SetSRID(ST_MakePoint(23.6,46.77),4326),'{}','','','Cluj-Napoca','','RO','','{}','','',now(),now(),'','','') RETURNING id`).Scan(&place); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `INSERT INTO social_activity(owner_id,place_id,activity_type_id,title,description,starts_at,ends_at,cohort,join_threshold,capacity,min_to_go,guardian_accompanied,supervised,meeting_point,what_to_bring,organizer_note,first_time_note,cost_band,cost_amount,cost_note,difficulty,accessibility_notes,beginners_welcome,status,is_hidden,is_publicly_listed,owner_can_override,go_confirmed_at,series_id,created_at,updated_at) VALUES($1,$2,$3,$4,'',now()+interval '1 day',NULL,$5,0.66,NULL,NULL,false,false,'','','','','free',NULL,'','easy','',true,'open',false,false,true,NULL,NULL,now(),now()) RETURNING id`, a.ID, place, kind, title, a.Cohort).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,created_at,updated_at,decided_at,arrived_at,attendance_intent,met_confirmed_at,welcomed_at,transit_status,departing_at,brings_support_person) VALUES($1,$2,'owner','member',now(),now(),now(),NULL,'',NULL,NULL,'',NULL,false)`, activity, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `INSERT INTO social_thread(created_at,activity_id,group_id) VALUES(now(),$1,NULL) RETURNING id`, activity).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	return activity, thread
}

func TestCasePort2ExportActivityDonationsAndSharedTargetBoundary(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	a := accountUser(t, s, "case2-portability-owner", "adult", "adult")
	activity, thread := casePort2Activity(t, s, a, "Game")
	if _, err := s.DB.Exec(ctx, `INSERT INTO donations_donation(donor_id,amount_cents,currency,recurring,campaign_id,provider,status,external_ref,created_at,completed_at) VALUES($1,500,'EUR',false,NULL,'stripe','completed','synthetic-external-reference',now(),now())`, a.ID); err != nil {
		t.Fatal(err)
	}
	post := accountPost(t, s, a.ID, thread, "look here", false, false)
	payload := casePort2Export(t, s, a)
	owned := payload["owned_activities"].([]any)
	if len(owned) != 1 || owned[0].(map[string]any)["id"] != float64(activity) || owned[0].(map[string]any)["title"] != "Game" {
		t.Fatal("owned activity portability omitted")
	}
	members := payload["memberships"].([]any)
	if len(members) != 1 || members[0].(map[string]any)["role"] != "owner" {
		t.Fatal("owner membership portability omitted")
	}
	donations := payload["donations"].(map[string]any)
	raw, _ := json.Marshal(donations)
	if donations["completed_count"] != float64(1) || donations["completed_total_cents"] != float64(500) || strings.Contains(strings.ToLower(string(raw)), "card") {
		t.Fatal("donation metadata portability drift")
	}
	target, _ := casePort2Activity(t, s, a, "SHARED_TARGET_SECRET")
	if _, err := s.DB.Exec(ctx, `UPDATE social_post SET shared_activity_id=$2 WHERE id=$1`, post, target); err != nil {
		t.Fatal(err)
	}
	payload = casePort2Export(t, s, a)
	posts := payload["thread_posts"].(map[string]any)
	postRaw, _ := json.Marshal(posts)
	if strings.Contains(string(postRaw), "SHARED_TARGET_SECRET") || !strings.Contains(string(postRaw), "look here") {
		t.Fatal("shared target content entered own-word projection")
	}
}

func TestCasePort2ExportPostRedactionGroupAndCapMatrix(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	a := accountUser(t, s, "case2-marker-owner", "adult", "adult")
	moderator := accountUser(t, s, "case2-private-moderator", "adult", "adult")
	thread, group := accountThread(t, s, a)
	if _, err := s.DB.Exec(ctx, `UPDATE social_group SET title='Cluj Runners' WHERE id=$1`, group); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name                            string
		hidden, deleted, remove, lifted bool
		wantBody, wantStatus            string
	}{
		{"admin-hidden", true, false, false, false, "[removed]", "removed"},
		{"admin-hidden-self-deleted", true, true, false, false, "hidden then withdrawn", "deleted_by_you"},
		{"platform-remove", true, false, true, false, "[removed]", "removed"},
		{"lifted-remove-self-delete", true, true, true, true, "vindicated words", "deleted_by_you"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			body := scenario.wantBody
			if body == "[removed]" {
				body = "synthetic hidden original " + scenario.name
			}
			post := accountPost(t, s, a.ID, thread, body, scenario.hidden, scenario.deleted)
			if scenario.remove {
				if _, err := s.DB.Exec(ctx, `INSERT INTO safety_moderationaction(target_id,action,reason,notes,expires_at,created_at,moderator_id,target_type_id,report_id,lifted_at) VALUES($1,'remove','other','Private moderator notes',NULL,now(),$2,(SELECT id FROM django_content_type WHERE app_label='social' AND model='post'),NULL,CASE WHEN $3::boolean THEN now() ELSE NULL END)`, post, moderator.ID, scenario.lifted); err != nil {
					t.Fatal(err)
				}
			}
			posts, err := s.exportPosts(ctx, a.ID, true)
			if err != nil {
				t.Fatal(err)
			}
			rows := posts["items"].([]json.RawMessage)
			row := map[string]any{}
			if err := json.Unmarshal(rows[len(rows)-1], &row); err != nil {
				t.Fatal(err)
			}
			if row["body"] != scenario.wantBody || row["status"] != scenario.wantStatus || row["thread_kind"] != "group" || row["thread_id"] != float64(group) || row["thread_title"] != "Cluj Runners" {
				t.Fatal("redaction/group export contract mismatch", scenario.name)
			}
			if scenario.lifted {
				var hidden bool
				if err := s.DB.QueryRow(ctx, `SELECT is_hidden FROM social_post WHERE id=$1`, post).Scan(&hidden); err != nil || !hidden {
					t.Fatal("lifting platform action cleared author withdrawal", err)
				}
			}
		})
	}
	if _, err := s.DB.Exec(ctx, `DELETE FROM social_post WHERE thread_id=$1`, thread); err != nil {
		t.Fatal(err)
	}
	accountPost(t, s, a.ID, thread, "group welcome post", false, false)
	posts, err := s.exportPosts(ctx, a.ID, true)
	if err != nil || posts["total"] != 1 || posts["truncated"] != false || len(posts["items"].([]json.RawMessage)) != 1 {
		t.Fatal("under-cap export falsely truncated", err)
	}
	var welcome map[string]any
	if err := json.Unmarshal(posts["items"].([]json.RawMessage)[0], &welcome); err != nil || welcome["body"] != "group welcome post" || welcome["thread_kind"] != "group" || welcome["thread_id"] != float64(group) || welcome["thread_title"] != "Cluj Runners" {
		t.Fatal("visible group name fallback/body projection drift", err)
	}
}

func TestCasePort2ExportSafetyScopesReportsAndModeratorPrivacy(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	a := accountUser(t, s, "case2-safety-subject", "adult", "adult")
	b := accountUser(t, s, "case2-safety-peer", "adult", "adult")
	mod := accountUser(t, s, "case2-moderator-identity-sentinel", "adult", "adult")
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_moderationaction(target_id,action,reason,notes,expires_at,created_at,moderator_id,target_type_id,report_id,lifted_at) VALUES($1,'suspend','spam','Private moderator notes',NULL,now(),$2,(SELECT id FROM django_content_type WHERE app_label='accounts' AND model='user'),NULL,NULL)`, a.ID, mod.ID); err != nil {
		t.Fatal(err)
	}
	for _, reporter := range []platform.Actor{a, b} {
		if _, err := s.DB.Exec(ctx, `INSERT INTO safety_report(reporter_id,target_type_id,target_id,reason,detail,status,handled_by_id,handled_at,resolution,created_at) VALUES($1,(SELECT id FROM django_content_type WHERE app_label='accounts' AND model='user'),$2,'spam','Synthetic report detail','open',NULL,NULL,'',now())`, reporter.ID, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	payload := casePort2Export(t, s, a)
	record := payload["safety_record"].(map[string]any)
	if len(record["decisions"].([]any)) != 1 || len(record["reports"].([]any)) != 1 {
		t.Fatal("export safety record was not subject/reporter scoped")
	}
	raw, _ := json.Marshal(payload)
	for _, private := range []string{mod.Username, mod.PublicID, "Private moderator notes"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("moderator identity/private notes entered subject export")
		}
	}
}
