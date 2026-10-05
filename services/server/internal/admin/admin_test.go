package admin

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

var adminDSN = flag.String("admin-test-dsn", "", "explicit disposable native operator fixture database")

func fixture(t *testing.T) (*Service, platform.Actor) {
	t.Helper()
	db := testdb.New(t, *adminDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	// The console actor is an administrator preset (ADR-0035, 2026-10-05).
	a := testdb.Actor(t, db, "fixture-staff", "adult")
	a.Role, a.IsStaff, a.IsSuperuser = "admin", true, true
	if _, err := db.Exec(context.Background(), `UPDATE accounts_user SET role='admin',is_staff=true,is_superuser=true WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	soc := social.New(db, platform.RecordAudit)
	safe := safety.New(db, safety.Config{})
	return New(db, catalog.New(db), soc, safe, nil), a
}
func rawFields(input map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range input {
		out[k], _ = json.Marshal(v)
	}
	return out
}
func TestNativeOperatorAllSourceModelSummariesAndFreshStaffGate(t *testing.T) {
	s, a := fixture(t)
	ctx := context.Background()
	inventory, err := s.Models(ctx, a)
	if err != nil || len(inventory) < 50 {
		t.Fatal(len(inventory), err)
	}
	for _, m := range inventory {
		if strings.Contains(m.Fields, "password") || strings.Contains(m.Fields, "ciphertext") || strings.Contains(m.Fields, "storage_key") {
			t.Fatal("sensitive projection", m)
		}
		if _, err = s.List(ctx, a, m.Name, 10, 0); err != nil {
			t.Errorf("%s: %v", m.Name, err)
		}
	}
	if _, err = s.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=false WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Models(ctx, a); err == nil {
		t.Fatal("stale staff capability survived")
	}
}

// The console is administrator-only (ADR-0035, 2026-10-05). Each refused call
// targets rows the administrator then acts on, so only the gate refuses it.
func TestNativeConsoleRequiresAdministrator(t *testing.T) {
	s, administrator := fixture(t)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	operator := testdb.Actor(t, s.DB, "fixture-operator", "adult")
	operator.IsStaff = true
	exec(`UPDATE accounts_user SET is_staff=true WHERE id=$1`, operator.ID)
	moderator := testdb.Actor(t, s.DB, "fixture-moderator", "adult")
	moderator.Role = "moderator"
	exec(`UPDATE accounts_user SET role='moderator' WHERE id=$1`, moderator.ID)
	// Legacy Django grants on the ban ledger are not native authorization inputs.
	var bannedType int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM django_content_type WHERE app_label='accounts' AND model='bannedidentity'`).Scan(&bannedType); err != nil {
		t.Fatal(err)
	}
	for _, codename := range []string{"view_bannedidentity", "change_bannedidentity"} {
		var permission int64
		if err := s.DB.QueryRow(ctx, `INSERT INTO auth_permission(name,content_type_id,codename) VALUES($1,$2,$3) RETURNING id`, "Synthetic "+codename, bannedType, codename).Scan(&permission); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO accounts_user_user_permissions(user_id,permission_id) VALUES($1,$2)`, operator.ID, permission)
	}
	subject := testdb.Actor(t, s.DB, "fixture-console-subject", "adult")
	reported := testdb.Actor(t, s.DB, "fixture-console-reported", "adult")
	var binding, ban, report int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO accounts_identitybinding(holder_hash,user_id,created_at,released_at) VALUES('synthetic-console-binding',$1,now(),NULL) RETURNING id`, subject.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `INSERT INTO accounts_bannedidentity(holder_hash,created_at) VALUES('synthetic-console-ban',now()) RETURNING id`).Scan(&ban); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `INSERT INTO safety_report(target_type_id,target_id,reporter_id,reason,detail,status,resolution,created_at,handled_by_id,handled_at) SELECT id,$1,$2,'spam','Synthetic console report','open','',now(),NULL,NULL FROM django_content_type WHERE app_label='accounts' AND model='user' RETURNING id`, reported.ID, subject.ID).Scan(&report); err != nil {
		t.Fatal(err)
	}
	place := testdb.Place(t, s.DB, "Console gate fixture venue", "osm")
	event, err := s.Save(ctx, administrator, "events.event", 0, rawFields(map[string]any{"title": "Console gate fixture event", "starts_at": "2030-01-01T18:00:00+02:00", "place_id": place, "license_name": "CC BY 4.0", "attribution": "Synthetic owner"}))
	if err != nil {
		t.Fatal(err)
	}
	audits := func() int {
		t.Helper()
		var n int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := audits()
	actions := []struct {
		model, action string
		id            int64
	}{{"accounts.bannedidentity", "lift_bans", ban}, {"accounts.identitybinding", "release_bindings", binding}, {"safety.report", "ban_target", report}}
	forged := operator
	forged.Role, forged.IsSuperuser = "admin", true
	for _, refused := range []struct {
		name  string
		actor platform.Actor
	}{{"operator", operator}, {"forged-superuser", forged}, {"moderator", moderator}} {
		t.Run(refused.name, func(t *testing.T) {
			a := refused.actor
			forbidden := func(call string, err error) {
				t.Helper()
				if !errors.Is(err, platform.ErrForbidden) {
					t.Fatalf("%s reached %s: %v", refused.name, call, err)
				}
			}
			forbidden("Gate", s.Gate(ctx, a))
			_, err := s.Models(ctx, a)
			forbidden("Models", err)
			for _, model := range []string{"safety.auditlog", "accounts.parentalconsent", "accounts.bannedidentity", "places.placeclaim", "taxonomy.activitytype"} {
				_, err = s.List(ctx, a, model, 10, 0)
				forbidden("List "+model, err)
			}
			for _, action := range actions {
				_, err = s.Execute(ctx, a, action.model, action.action, []int64{action.id}, "Synthetic refused review")
				forbidden("Execute "+action.action, err)
			}
			_, err = s.Save(ctx, a, "taxonomy.activitycategory", 0, rawFields(map[string]any{"name": "Refused console category", "slug": "refused-console-category"}))
			forbidden("Save", err)
			forbidden("ReviewEvent", s.ReviewEvent(ctx, a, event, false, "Synthetic refused hold"))
		})
	}
	var banned, bound, active, held bool
	var status string
	var categories, moderation int
	if err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_bannedidentity WHERE id=$1),EXISTS(SELECT 1 FROM accounts_identitybinding WHERE id=$2 AND released_at IS NULL),(SELECT is_active FROM accounts_user WHERE id=$3),(SELECT status FROM safety_report WHERE id=$4),(SELECT is_import_held FROM events_event WHERE id=$5),(SELECT count(*) FROM taxonomy_activitycategory WHERE slug='refused-console-category'),(SELECT count(*) FROM safety_moderationaction)`, ban, binding, reported.ID, report, event).Scan(&banned, &bound, &active, &status, &held, &categories, &moderation); err != nil {
		t.Fatal(err)
	}
	if !banned || !bound || !active || status != "open" || held || categories != 0 || moderation != 0 || audits() != before {
		t.Fatal("refused console call changed state", banned, bound, active, status, held, categories, moderation)
	}
	inventory, err := s.Models(ctx, administrator)
	if err != nil || len(inventory) < 50 {
		t.Fatal(len(inventory), err)
	}
	for _, model := range []string{"safety.auditlog", "accounts.bannedidentity"} {
		if rows, err := s.List(ctx, administrator, model, 10, 0); err != nil || len(rows) == 0 {
			t.Fatal(model, len(rows), err)
		}
	}
	for _, action := range actions {
		results, err := s.Execute(ctx, administrator, action.model, action.action, []int64{action.id}, "Synthetic administrator review")
		if err != nil || len(results) != 1 || !results[0].Applied {
			t.Fatal(action.action, results, err)
		}
	}
	if _, err = s.Save(ctx, administrator, "taxonomy.activitycategory", 0, rawFields(map[string]any{"name": "Administrator console category", "slug": "administrator-console-category"})); err != nil {
		t.Fatal(err)
	}
	if err = s.ReviewEvent(ctx, administrator, event, false, "Synthetic administrator hold"); err != nil {
		t.Fatal(err)
	}
	// A captured administrator demoted in the database is refused on its next call.
	exec(`UPDATE accounts_user SET is_superuser=false WHERE id=$1`, administrator.ID)
	if err = s.Gate(ctx, administrator); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("demoted administrator kept the console", err)
	}
	if _, err = s.Models(ctx, administrator); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("demoted administrator kept model summaries", err)
	}
	if _, err = s.Save(ctx, administrator, "taxonomy.activitycategory", 0, rawFields(map[string]any{"name": "Demoted console category", "slug": "demoted-console-category"})); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("demoted administrator kept curated writes", err)
	}
}
func TestNativeOperatorCuratedDataAndPublicationForeignKeys(t *testing.T) {
	s, a := fixture(t)
	ctx := context.Background()
	partner, err := s.Save(ctx, a, "places.partner", 0, rawFields(map[string]any{"name": "Licensed fixture partner", "kind": "civic", "is_verified": false}))
	if err != nil {
		t.Fatal(err)
	}
	input := rawFields(map[string]any{"title": "Civic fixture campaign", "slug": "fixture-campaign", "goal_cents": 1000, "partner_id": partner})
	if _, err = s.Save(ctx, a, "donations.campaign", 0, input); err == nil {
		t.Fatal("inactive/unverified partner credited")
	}
	if _, err = s.Save(ctx, a, "places.partner", partner, rawFields(map[string]any{"is_verified": true})); err != nil {
		t.Fatal(err)
	}
	campaign, err := s.Save(ctx, a, "donations.campaign", 0, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(ctx, a, "donations.spendentry", 0, rawFields(map[string]any{"category": "Room rental", "amount_cents": 100, "campaign_id": campaign})); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(ctx, a, "accounts.user", a.ID, rawFields(map[string]any{"cohort": "child", "is_identity_verified": true})); err == nil {
		t.Fatal("identity bypass")
	}
	if _, err = s.Save(ctx, a, "donations.donation", 0, rawFields(map[string]any{"status": "completed"})); err == nil {
		t.Fatal("payment settlement bypass")
	}
	if _, err = s.Save(ctx, a, "media.photo", 1, rawFields(map[string]any{"scan_status": "clean"})); err == nil {
		t.Fatal("scanner bypass")
	}
	if _, err = s.Save(ctx, a, "places.partner", partner, rawFields(map[string]any{"contact_email": "private@fixture.local"})); err == nil {
		t.Fatal("undeclared field")
	}
	category, err := s.Save(ctx, a, "taxonomy.activitycategory", 0, rawFields(map[string]any{"name": "Fixture public category", "slug": "fixture-public-category"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(ctx, a, "taxonomy.activitycategory", category, rawFields(map[string]any{"parent_id": category})); err == nil {
		t.Fatal("taxonomy self cycle")
	}
	var logs int
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event='admin.curated_saved'`).Scan(&logs); err != nil || logs < 5 {
		t.Fatal(logs, err)
	}
}
func TestNativeOperatorReviewedCatalogActionsAndImmutableIdentityLedgers(t *testing.T) {
	s, a := fixture(t)
	ctx := context.Background()
	place := testdb.Place(t, s.DB, "Operator fixture", "osm")
	user := testdb.Actor(t, s.DB, "fixture-contributor", "adult")
	correction, err := s.Catalog.ProposeCorrection(ctx, user, place, "name", "Crowd fixture corrected")
	if err != nil {
		t.Fatal(err)
	}
	results, err := s.Execute(ctx, a, "places.placecorrection", "publish_corrections", []int64{correction}, "Reviewed against physical fixture")
	if err != nil || !results[0].Applied {
		t.Fatal(results, err)
	}
	var status string
	if err = s.DB.QueryRow(ctx, `SELECT status FROM places_placecorrection WHERE id=$1`, correction).Scan(&status); err != nil || status != "published" {
		t.Fatal(status, err)
	}
	var binding, ban int64
	if err = s.DB.QueryRow(ctx, `INSERT INTO accounts_identitybinding(holder_hash,user_id,created_at,released_at) VALUES('synthetic-fixture-hash',$1,now(),NULL) RETURNING id`, user.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(ctx, `INSERT INTO accounts_bannedidentity(holder_hash,created_at) VALUES('synthetic-fixture-hash',now()) RETURNING id`).Scan(&ban); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []struct {
		model, action string
		id            int64
	}{{"accounts.identitybinding", "release_bindings", binding}, {"accounts.bannedidentity", "lift_bans", ban}} {
		results, err = s.Execute(ctx, a, operation.model, operation.action, []int64{operation.id}, "Synthetic review")
		if err != nil || !results[0].Applied {
			t.Fatal(results, err)
		}
	}
	if _, err = s.Execute(ctx, a, "accounts.parentalconsent", "activate", []int64{1}, ""); err == nil {
		t.Fatal("consent activation bypass")
	}
	if _, err = s.Save(ctx, a, "accounts.identitybinding", binding, rawFields(map[string]any{"released_at": nil})); err == nil {
		t.Fatal("identity ledger edit")
	}
}

func TestNativeOperatorProposalPublishRejectAreAudited(t *testing.T) {
	s, a := fixture(t)
	ctx := context.Background()
	contributor := testdb.Actor(t, s.DB, "fixture-proposal-contributor", "adult")
	var typ int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='reading'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	for i, action := range []string{"publish_selected", "reject_selected"} {
		id, err := s.Social.ProposePlace(ctx, contributor, social.PlaceProposalInput{Name: fmt.Sprintf("Fixture native proposal %d", i), Lon: 23.8 + float64(i)*.01, Lat: 46.8, ActivityType: typ, AllowNearby: true})
		if err != nil {
			t.Fatal(err)
		}
		results, err := s.Execute(ctx, a, "social.userplaceproposal", action, []int64{id}, "Synthetic reviewed proposal")
		if err != nil || !results[0].Applied {
			t.Fatal(action, results, err)
		}
		var status string
		if err = s.DB.QueryRow(ctx, `SELECT status FROM social_userplaceproposal WHERE id=$1`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		want := "published"
		if action == "reject_selected" {
			want = "rejected"
		}
		if status != want {
			t.Fatal(action, status)
		}
	}
	var count int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event IN('place.staff_published','place.staff_rejected')`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}

func TestNativeOperatorManualEventsKeepImportedFactsImmutable(t *testing.T) {
	s, a := fixture(t)
	ctx := context.Background()
	place := testdb.Place(t, s.DB, "Manual event fixture venue", "osm")
	id, err := s.Save(ctx, a, "events.event", 0, rawFields(map[string]any{"title": "Manual fixture event", "starts_at": "2030-01-01T18:00:00+02:00", "place_id": place, "license_name": "CC BY 4.0", "attribution": "Synthetic owner"}))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReviewEvent(ctx, a, id, false, "Hold for synthetic review"); err != nil {
		t.Fatal(err)
	}
	if err = s.ReviewEvent(ctx, a, id, true, "Verified public fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, `UPDATE events_event SET source='roedu',source_pack_id='roedu:social_media_activities_app:events_places:v1',source_confidence=0.8,is_import_held=true WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = s.ReviewEvent(ctx, a, id, true, "Low-confidence fixture stays held"); err == nil {
		t.Fatal("low confidence published")
	}
	if _, err = s.Save(ctx, a, "events.event", id, rawFields(map[string]any{"title": "Overwrite canonical imported facts"})); err == nil {
		t.Fatal("imported facts overwritten")
	}
	if _, err = s.DB.Exec(ctx, `UPDATE events_event SET source_confidence=1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = s.ReviewEvent(ctx, a, id, true, "Verified exact canonical fixture"); err != nil {
		t.Fatal(err)
	}
}
