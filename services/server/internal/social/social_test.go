package social

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed testdata/django_serializers.json
var djangoSerializers []byte

func canonicalTimes(v any) any {
	switch x := v.(type) {
	case string:
		if stamp, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return stamp.UTC().Format(time.RFC3339Nano)
		}
		return x
	case map[string]any:
		for k, value := range x {
			x[k] = canonicalTimes(value)
		}
		return x
	case []any:
		for i, value := range x {
			x[i] = canonicalTimes(value)
		}
		return x
	default:
		return x
	}
}
func compareGolden(t *testing.T, name string, actual json.RawMessage, overrides map[string]any) {
	t.Helper()
	var goldens map[string]json.RawMessage
	if err := json.Unmarshal(djangoSerializers, &goldens); err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(goldens[name], &want); err != nil {
		t.Fatal(err)
	}
	for k, v := range overrides {
		want[k] = v
	}
	got := decodeObject(t, actual)
	if !reflect.DeepEqual(canonicalTimes(got), canonicalTimes(want)) {
		t.Fatalf("%s projection mismatch\ngot=%s\nwant=%v", name, actual, want)
	}
}

func TestVenueDedupMatchesDjango(t *testing.T) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(djangoSerializers, &raw); err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		A     string  `json:"a"`
		B     string  `json:"b"`
		Ratio float64 `json:"ratio"`
	}
	if err := json.Unmarshal(raw["dedup"], &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := sequenceRatio(c.A, c.B); math.Abs(got-c.Ratio) > 1e-12 {
			t.Fatalf("names=%q,%q ratio=%v want=%v", c.A, c.B, got, c.Ratio)
		}
	}
}

var socialTestDSN = flag.String("social-test-dsn", "", "Explicit disposable PostgreSQL fixture server; no environment credential discovery")

func p(n int) *int { return &n }
func TestGuardrailsIntersectWithoutWidening(t *testing.T) {
	rail, err := combineGuardrails([]Guardrail{{Supervised: true, Latest: p(20), Earliest: p(10), Cap: p(4), Weekdays: "123", Categories: []string{"sport", "culture"}}, {Latest: p(18), Earliest: p(12), Cap: p(2), Weekdays: "34", Categories: []string{"sport"}}, {Weekdays: "", Categories: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	if !rail.Supervised || *rail.Latest != 18 || *rail.Earliest != 12 || *rail.Cap != 2 || len(rail.Weekdays) != 1 || !rail.Weekdays[3] || len(rail.Categories) != 1 || !rail.Categories["sport"] {
		t.Fatalf("strictest=%+v", rail)
	}
	conflict, err := combineGuardrails([]Guardrail{{Weekdays: "12", Categories: []string{"sport"}}, {Weekdays: "34", Categories: []string{"culture"}}})
	if err != nil || conflict.Weekdays == nil || len(conflict.Weekdays) != 0 || conflict.Categories == nil || len(conflict.Categories) != 0 {
		t.Fatalf("conflicts must be empty allowlists, not unrestricted: %+v %v", conflict, err)
	}
	if _, err := combineGuardrails([]Guardrail{{Weekdays: "123x"}}); err == nil {
		t.Fatal("malformed safety rails widened access")
	}
}
func TestSeriesWallClockAndMonthlyAnchor(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatal(err)
	}
	march := time.Date(2026, 3, 22, 18, 0, 0, 0, loc)
	next, err := AdvanceSlot(march, "weekly", 22, loc)
	if err != nil || next.Hour() != 18 || next.Day() != 29 || next.Sub(march) != 167*time.Hour {
		t.Fatalf("DST slot=%v duration=%v err=%v", next, next.Sub(march), err)
	}
	jan := time.Date(2026, 1, 31, 18, 0, 0, 0, loc)
	feb, err := AdvanceSlot(jan, "monthly", 31, loc)
	if err != nil || feb.Day() != 28 {
		t.Fatal(feb, err)
	}
	mar, err := AdvanceSlot(feb, "monthly", 31, loc)
	if err != nil || mar.Day() != 31 {
		t.Fatal(mar, err)
	}
}
func TestCrossCohortAndUnassignedProfilesHaveNoTier(t *testing.T) {
	adult := Actor{ID: 1, IsActive: true, Cohort: "adult"}
	for _, peer := range []Actor{{ID: 1, IsActive: true, Cohort: "adult"}, {ID: 2, IsActive: true, Cohort: "child"}, {ID: 2, IsActive: true, Cohort: "unassigned"}, {ID: 2, IsActive: false, Cohort: "adult"}} {
		if pairVisible(adult, peer) {
			t.Fatalf("vetoed pair accepted: %+v", peer)
		}
	}
}
func TestAllSocialRoutesRequireAuthentication(t *testing.T) {
	s := New(nil, nil)
	mux := http.NewServeMux()
	s.Register(mux)
	for _, path := range []string{"/api/v1/social/activities/", "/api/social/groups/", "/api/v1/social/activities/1/posts/", "/api/v1/social/memberships/1/vote/", "/api/connections/connections/search/", "/api/v1/connections/people/aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa/", "/api/v1/communities/communities/graph/"} {
		method := "GET"
		if strings.HasSuffix(path, "/vote/") {
			method = "POST"
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		if rec.Code != 401 {
			t.Fatalf("%s status=%d", path, rec.Code)
		}
	}
}

// Tests clone schema shape into their own namespace. LIKE INCLUDING ALL retains
// columns, checks, primary/unique/index constraints while avoiding writes to
// other workers' fixtures. The end-to-end parity gate uses the full baseline.
func testStore(t *testing.T) (*Service, func()) {
	db := testdb.New(t, *socialTestDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	return New(db, platform.RecordAudit), func() {}
}
func fixtureUser(t *testing.T, s *Service, name, cohort string) Actor {
	t.Helper()
	ctx := context.Background()
	a := Actor{Username: name, DisplayName: name, Cohort: cohort, AgeBand: "adult", Role: "user", IsActive: true, IdentityVerified: true}
	if cohort == "child" {
		a.AgeBand = "under_16"
	}
	if cohort == "teen" {
		a.AgeBand = "16_17"
	}
	err := s.DB.QueryRow(ctx, `INSERT INTO accounts_user(password,last_login,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES('!',NULL,false,gen_random_uuid(),$1,$1,$2,$3,true,now(),'user',true,false,now()) RETURNING id,public_id::text`, name, a.AgeBand, cohort).Scan(&a.ID, &a.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if cohort == "child" {
		if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_parentalconsent(minor_id,guardian_identifier,status,scope,granted_at,expires_at,revoked_at,renewal_notice,created_at,updated_at) VALUES($1,'synthetic-fixture','active','',now(),now()+interval '1 year',NULL,'',now(),now())`, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	return a
}
func fixtureActivity(t *testing.T, s *Service, owner Actor, capacity *int) int64 {
	t.Helper()
	ctx := context.Background()
	var place, typeID int64
	err := s.DB.QueryRow(ctx, `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES('Synthetic fixture hall','osm','',NULL,'',ST_SetSRID(ST_MakePoint(23.6,46.77),4326),'{"amenity":"library"}','', '', 'Cluj-Napoca','','RO','','{}','','',now(),now(),'','','') RETURNING id`).Scan(&place)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateActivity(ctx, owner, ActivityInput{Place: place, ActivityType: typeID, Title: "Synthetic meetup", StartsAt: time.Now().Add(24 * time.Hour), Capacity: capacity})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func decodeObject(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPostgresActivitiesVoteThreadAndSafety(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "go-social-owner", "adult")
	joiner := fixtureUser(t, s, "go-social-joiner", "adult")
	outsider := fixtureUser(t, s, "go-social-outsider", "adult")
	child := fixtureUser(t, s, "go-social-child", "child")
	id := fixtureActivity(t, s, owner, p(3))
	if _, err := s.Activity(ctx, child, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-cohort activity read=%v", err)
	}
	if _, _, err := s.Posts(ctx, outsider, "activity", id, 0, 100); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("nonmember private thread=%v", err)
	}
	mid, err := s.Join(ctx, joiner, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Vote(ctx, joiner, mid, true, false); !errors.Is(err, platform.ErrInvalid) {
		t.Fatalf("self-vote accepted=%v", err)
	}
	if err = s.Vote(ctx, owner, mid, true, false); err != nil {
		t.Fatal(err)
	}
	membership, err := s.Membership(ctx, joiner, mid)
	if err != nil || decodeObject(t, membership)["state"] != "member" {
		t.Fatalf("admission=%s %v", membership, err)
	}
	pid, err := s.WritePost(ctx, joiner, "activity", id, PostInput{Body: " Original body "}, false)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := s.WritePost(ctx, owner, "activity", id, PostInput{Body: "Reply", ReplyTo: &pid}, false)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := s.WritePost(ctx, joiner, "activity", id, PostInput{Body: "Nested reply", ReplyTo: &reply}, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.Post(ctx, owner, nested)
	if err != nil || int64(decodeObject(t, raw)["reply_to"].(float64)) != pid {
		t.Fatalf("reply depth=%s %v", raw, err)
	}
	if err = s.EditPost(ctx, owner, pid, "Impersonation"); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("foreign author edit=%v", err)
	}
	if _, err = s.ToggleSentiment(ctx, joiner, pid, "reaction", "helped_me"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ToggleSentiment(ctx, joiner, pid, "reaction", "custom-vanity"); !errors.Is(err, platform.ErrInvalid) {
		t.Fatalf("unknownfacet=%v", err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, owner.ID, joiner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.WritePost(ctx, joiner, "activity", id, PostInput{Body: "Blocked"}, false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("blocked write=%v", err)
	}
	if _, _, err = s.Posts(ctx, joiner, "activity", id, 0, 100); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("blocked private read=%v", err)
	}
	var n int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM social_post`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("deniedwrites createdpost count=%d err=%v", n, err)
	}
}
func TestPostgresMutationAndAuditRollbackTogether(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "go-social-rollback", "adult")
	id := fixtureActivity(t, s, owner, p(2))
	s.Audit = func(context.Context, pgx.Tx, Actor, string, string, any) error {
		return errors.New("synthetic audit failure")
	}
	if err := s.SetActivityListing(ctx, owner, id, true); err == nil {
		t.Fatal("audit failure accepted")
	}
	var listed bool
	if err := s.DB.QueryRow(ctx, `SELECT is_publicly_listed FROM social_activity WHERE id=$1`, id).Scan(&listed); err != nil || listed {
		t.Fatalf("mutation survived failedaudit listed=%v err=%v", listed, err)
	}
}
func TestPostgresCapacityAdmissionsSerialize(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "go-social-capacity-owner", "adult")
	a := fixtureUser(t, s, "go-social-capacity-a", "adult")
	b := fixtureUser(t, s, "go-social-capacity-b", "adult")
	id := fixtureActivity(t, s, owner, p(2))
	ma, err := s.Join(ctx, a, id)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := s.Join(ctx, b, id)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, mid := range []int64{ma, mb} {
		wg.Add(1)
		go func(mid int64) { defer wg.Done(); errs <- s.Vote(ctx, owner, mid, true, true) }(mid)
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, platform.ErrInvalid) {
			t.Fatal(err)
		}
	}
	n, err := participantCount(ctx, s.DB, id)
	if err != nil || success != 1 || n != 2 {
		t.Fatalf("admissions=%d seats=%d err=%v", success, n, err)
	}
}
func TestPostgresChildVenueAndGuardianEnvelope(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	child := fixtureUser(t, s, "go-social-ward", "child")
	id := fixtureActivity(t, s, child, p(4))
	var placeID, typeID int64
	if err := s.DB.QueryRow(ctx, `SELECT place_id,activity_type_id FROM social_activity WHERE id=$1`, id).Scan(&placeID, &typeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET source='roedu',raw_tags='{}' WHERE id=$1`, placeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateActivity(ctx, child, ActivityInput{Place: placeID, ActivityType: typeID, Title: "Unsafe unknown", StartsAt: time.Now().Add(time.Hour)}); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("unknownchildvenue=%v", err)
	}
	guardian := fixtureUser(t, s, "go-social-guardian", "adult")
	var rel int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now()) RETURNING id`, guardian.ID, child.ID).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianguardrail(relationship_id,supervised_only,latest_start_hour,max_open_joins,allowed_weekdays,earliest_start_hour,allowed_categories,created_at,updated_at) VALUES($1,false,NULL,NULL,'',NULL,ARRAY['culture'],now(),now())`, rel); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET source='osm',raw_tags='{"amenity":"library"}' WHERE id=$1`, placeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateActivity(ctx, child, ActivityInput{Place: placeID, ActivityType: typeID, Title: "Category outside envelope", StartsAt: time.Now().Add(time.Hour)}); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("categoryenvelope=%v", err)
	}
}

func TestPostgresSerializerGoldenParity(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "native_owner", "adult")
	member := fixtureUser(t, s, "native_member", "adult")
	third := fixtureUser(t, s, "native_third", "adult")
	owner.DisplayName = "Native Owner"
	member.DisplayName = "Native Member"
	owner.PublicID = "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"
	member.PublicID = "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb"
	for _, a := range []Actor{owner, member} {
		if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET display_name=$2,public_id=$3 WHERE id=$1`, a.ID, a.DisplayName, a.PublicID); err != nil {
			t.Fatal(err)
		}
	}
	id := fixtureActivity(t, s, owner, p(5))
	mid, err := s.Join(ctx, member, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Vote(ctx, owner, mid, true, true); err != nil {
		t.Fatal(err)
	}
	thirdID, err := s.Join(ctx, third, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Vote(ctx, owner, thirdID, true, true); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET title='Native meetup',description='A descriptive note.',starts_at=$2,created_at=$2,min_to_go=2 WHERE id=$1`, id, stamp); err != nil {
		t.Fatal(err)
	}
	var place, typeID int64
	if err := s.DB.QueryRow(ctx, `SELECT place_id,activity_type_id FROM social_activity WHERE id=$1`, id).Scan(&place, &typeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE taxonomy_activitytype SET name='Basketball' WHERE id=$1`, typeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE taxonomy_activitycategory SET name='Sport' WHERE id=(SELECT category_id FROM taxonomy_activitytype WHERE id=$1)`, typeID); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Activity(ctx, owner, id)
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "activity", raw, map[string]any{"id": float64(id), "place": float64(place)})
	if _, err := s.DB.Exec(ctx, `UPDATE social_membership SET created_at=$2,decided_at=$2 WHERE id=$1`, mid, stamp); err != nil {
		t.Fatal(err)
	}
	raw, err = s.Membership(ctx, member, mid)
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "membership", raw, map[string]any{"id": float64(mid), "activity": float64(id)})
	pid, err := s.WritePost(ctx, member, "activity", id, PostInput{Body: "Message"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE social_post SET created_at=$2,updated_at=$2 WHERE id=$1`, pid, stamp); err != nil {
		t.Fatal(err)
	}
	raw, err = s.Post(ctx, owner, pid)
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "post", raw, map[string]any{"id": float64(pid)})
	s.AllowUserGroups = true
	gid, err := s.CreateGroup(ctx, owner, GroupInput{City: "Cluj-Napoca", ActivityType: &typeID, Title: "Native group", Description: "A standing group."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE social_group SET created_at=$2 WHERE id=$1`, gid, stamp); err != nil {
		t.Fatal(err)
	}
	raw, err = s.Group(ctx, owner, gid)
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "group", raw, map[string]any{"id": float64(gid)})
	sid, err := s.CreateSeries(ctx, owner, SeriesInput{ActivityInput: ActivityInput{Place: place, ActivityType: typeID, Title: "Native series", Capacity: p(5)}, Cadence: "weekly", FirstStartsAt: stamp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE social_activityseries SET created_at=$2 WHERE id=$1`, sid, stamp); err != nil {
		t.Fatal(err)
	}
	raw, err = s.Series(ctx, owner, sid)
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "series", raw, map[string]any{"id": float64(sid), "place": float64(place)})
	cid, err := s.RequestConnection(ctx, owner, member.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE connections_connection SET created_at=$2 WHERE id=$1`, cid, stamp); err != nil {
		t.Fatal(err)
	}
	raw, err = s.Connection(ctx, owner, cid)
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "connection", raw, map[string]any{"id": float64(cid)})
}

func TestPostgresMinorGroupAndSupervisionGates(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	ward := fixtureUser(t, s, "go-minor-group-ward", "child")
	peer := fixtureUser(t, s, "go-minor-group-peer", "child")
	staff := fixtureUser(t, s, "go-minor-group-curator", "adult")
	staff.IsStaff = true
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1`, staff.ID); err != nil {
		t.Fatal(err)
	}
	id := fixtureActivity(t, s, ward, p(2))
	var typ, threadID int64
	if err := s.DB.QueryRow(ctx, `SELECT activity_type_id,(SELECT id FROM social_thread WHERE activity_id=$1) FROM social_activity WHERE id=$1`, id).Scan(&typ, &threadID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSupervision(ctx, ward, id, true); err != nil {
		t.Fatal(err)
	}
	mid, err := s.Join(ctx, peer, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Vote(ctx, ward, mid, true, false); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Membership(ctx, ward, mid)
	if err != nil || decodeObject(t, raw)["state"] != "requested" {
		t.Fatalf("supervised join settled without guardian: %s %v", raw, err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, staff.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddGuardian(ctx, ward, id, staff.ID); err != nil {
		t.Fatal(err)
	}
	raw, err = s.Membership(ctx, ward, mid)
	if err != nil || decodeObject(t, raw)["state"] != "member" {
		t.Fatalf("qualified pending join not settled: %s %v", raw, err)
	}
	if yes, err := s.CanReadThread(ctx, s.DB, staff, threadID); err != nil || yes {
		t.Fatalf("adult supervisor read minor peer thread: %v %v", yes, err)
	}
	if yes, err := s.CanWriteThread(ctx, s.DB, staff, threadID); err != nil || yes {
		t.Fatalf("adult supervisor wrote minor peer thread: %v %v", yes, err)
	}
	input := GroupInput{City: "Cluj-Napoca", ActivityType: &typ, Title: "Curated minor group", Cohort: "child"}
	if _, err := s.CreateGroup(ctx, staff, input); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("minor creation activated unexpectedly=%v", err)
	}
	s.MinorOnboardingEnabled = true
	gid, err := s.CreateGroup(ctx, staff, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.JoinGroup(ctx, ward, gid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WritePost(ctx, ward, "group", gid, PostInput{Body: "Enumerating minor author"}, false); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("minor group ordinary post=%v", err)
	}
	pid, err := s.WritePost(ctx, staff, "group", gid, PostInput{Body: "Group-wide logistics"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Post(ctx, staff, pid); err != nil {
		t.Fatal("curator's own announcement result", err)
	}
	if _, _, err := s.Posts(ctx, staff, "group", gid, 0, 100); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("staff history carveout=%v", err)
	}
	if _, err := s.GroupRoster(ctx, ward, gid); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("minor roster exposed=%v", err)
	}
	if _, err := s.AskGroup(ctx, ward, gid, "my private address"); !errors.Is(err, platform.ErrInvalid) {
		t.Fatalf("arbitrary child free text accepted=%v", err)
	}
	if sent, err := s.AskGroup(ctx, ward, gid, "where"); err != nil || !sent {
		t.Fatal(sent, err)
	}
}
func TestPostgresGaugesNeverCopyMembershipAndPlaceQuorumIsIndependent(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "go-gauge-owner", "adult")
	a := fixtureUser(t, s, "go-gauge-a", "adult")
	b := fixtureUser(t, s, "go-gauge-b", "adult")
	c := fixtureUser(t, s, "go-gauge-c", "adult")
	base := fixtureActivity(t, s, owner, nil)
	var place, typ int64
	if err := s.DB.QueryRow(ctx, `SELECT place_id,activity_type_id FROM social_activity WHERE id=$1`, base).Scan(&place, &typ); err != nil {
		t.Fatal(err)
	}
	gid, err := s.ProposeGauge(ctx, owner, GaugeInput{Place: place, ActivityType: typ, CoarseWindow: "weekend_daytime"})
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range []Actor{a, b, a} {
		if err := s.MarkGauge(ctx, peer, gid, true); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := s.Gauge(ctx, owner, gid)
	if err != nil {
		t.Fatal(err)
	}
	body := decodeObject(t, raw)
	if body["ready"] != true || body["remaining"].(float64) != 0 {
		t.Fatalf("gauge signal=%s", raw)
	}
	for _, key := range []string{"interested_users", "interested_count", "members", "who"} {
		if _, ok := body[key]; ok {
			t.Fatal("identity/count leak", key)
		}
	}
	id, err := s.ConvertGauge(ctx, owner, gid, GaugeConversion{Title: "Signal to meetup", StartsAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	members, err := participantCount(ctx, s.DB, id)
	if err != nil || members != 1 {
		t.Fatalf("signal copied a roster: %d %v", members, err)
	}
	if _, err := s.ConvertGauge(ctx, owner, gid, GaugeConversion{Title: "Duplicated signal", StartsAt: time.Now().Add(time.Hour)}); !errors.Is(err, platform.ErrInvalid) {
		t.Fatalf("double conversion=%v", err)
	}
	pid, err := s.ProposePlace(ctx, owner, PlaceProposalInput{Name: "Distant proposed venue", Lon: 24.4, Lat: 47.7, ActivityType: typ})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmPlace(ctx, owner, pid); !errors.Is(err, platform.ErrInvalid) {
		t.Fatalf("self-confirmation=%v", err)
	}
	for _, peer := range []Actor{a, a, b} {
		if err := s.ConfirmPlace(ctx, peer, pid); err != nil {
			t.Fatal(err)
		}
	}
	raw, err = s.Proposal(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if decodeObject(t, raw)["status"] != "pending" {
		t.Fatal("duplicate confirmation satisfied quorum")
	}
	if err := s.ConfirmPlace(ctx, c, pid); err != nil {
		t.Fatal(err)
	}
	raw, err = s.Proposal(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if decodeObject(t, raw)["status"] != "published" {
		t.Fatal("independent quorum not published")
	}
}

func TestPostgresCommunityMaterializationUsesCohortPeerFloor(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "go-community-owner", "adult")
	first := fixtureActivity(t, s, owner, nil)
	for n := 0; n < 4; n++ {
		peer := fixtureUser(t, s, fmt.Sprintf("go-community-peer-%d", n), "adult")
		mid, err := s.Join(ctx, peer, first)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Vote(ctx, owner, mid, true, true); err != nil {
			t.Fatal(err)
		}
	}
	_ = fixtureActivity(t, s, owner, nil)
	third := fixtureActivity(t, s, owner, nil)
	if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET starts_at=starts_at+interval '1 day' WHERE id=$1`, third); err != nil {
		t.Fatal(err)
	}
	child := fixtureUser(t, s, "go-community-thin-child", "child")
	_ = fixtureActivity(t, s, child, nil)
	result, err := s.GenerateCommunities(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Published != 2 || result.Deactivated != 0 {
		t.Fatalf("published=%+v", result)
	}
	var childRows int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM communities_community WHERE cohort='child'`).Scan(&childRows); err != nil || childRows != 0 {
		t.Fatalf("thin minor existence disclosed: %d %v", childRows, err)
	}
	var slug string
	if err := s.DB.QueryRow(ctx, `SELECT slug FROM communities_community WHERE tier='type' AND cohort='adult'`).Scan(&slug); err != nil || slug != "cluj-napoca-t-basketball-adult" {
		t.Fatalf("slug=%q %v", slug, err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET is_hidden=true WHERE cohort='adult'`); err != nil {
		t.Fatal(err)
	}
	result, err = s.GenerateCommunities(ctx, time.Now())
	if err != nil || result.Published != 0 || result.Deactivated != 2 {
		t.Fatalf("deactivation=%+v %v", result, err)
	}
	var retained int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM communities_community`).Scan(&retained); err != nil || retained != 2 {
		t.Fatal("deactivation deleted stable labels", retained, err)
	}
}
func TestPostgresSentimentIsBatchedAndMinorCountless(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "go-sentiment-owner", "adult")
	id := fixtureActivity(t, s, owner, nil)
	peers := []Actor{}
	for n := 0; n < 3; n++ {
		a := fixtureUser(t, s, fmt.Sprintf("go-sentiment-peer-%d", n), "adult")
		mid, err := s.Join(ctx, a, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Vote(ctx, owner, mid, true, true); err != nil {
			t.Fatal(err)
		}
		peers = append(peers, a)
	}
	pid, err := s.WritePost(ctx, owner, "activity", id, PostInput{Body: "A helpful post"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range peers[:2] {
		if _, err := s.ToggleSentiment(ctx, a, pid, "reaction", "helped_me"); err != nil {
			t.Fatal(err)
		}
	}
	s.Sentiment.AdultK = 2
	s.Sentiment.DissentK = 2
	s.Sentiment.DissentAudience = 4
	before, err := s.SentimentFooters(ctx, owner, "activity", id, []int64{pid})
	if err != nil || len(before[pid]) != 0 {
		t.Fatal("per-reaction immediate footer leak", before, err)
	}
	now := time.Now()
	out, err := s.RecomputeSentiment(ctx, now)
	if err != nil || out.Latched != 1 {
		t.Fatal(out, err)
	}
	out, err = s.RecomputeSentiment(ctx, now.Add(time.Hour))
	if err != nil || !out.Skipped {
		t.Fatal("daily claim not idempotent", out, err)
	}
	for _, viewer := range []Actor{owner, peers[0]} {
		lines, err := s.SentimentFooters(ctx, viewer, "activity", id, []int64{pid})
		if err != nil || !reflect.DeepEqual(lines[pid], []string{"People found this helpful."}) {
			t.Fatal("author/viewer footer differs", lines, err)
		}
	}
	if _, err := s.ToggleSentiment(ctx, peers[0], pid, "reaction", "helped_me"); err != nil {
		t.Fatal(err)
	}
	lines, err := s.SentimentFooters(ctx, owner, "activity", id, []int64{pid})
	if err != nil || len(lines[pid]) != 1 {
		t.Fatal("reaction removal leaked live attribution", lines, err)
	}
	out, err = s.RecomputeSentiment(ctx, now.Add(25*time.Hour))
	if err != nil || out.Unlatched != 1 {
		t.Fatal("surviving-row rederive failed", out, err)
	}
	footer := Footer{Pairs: [][2]string{{"helped_me", "2026-01-01"}, {"felt_welcome", "2026-01-01"}}, Permanent: []string{"made_me_smile"}, Dissent: true}
	if got := FooterLines(footer, "child", "child", false); len(got) != 0 {
		t.Fatal("child footer", got)
	}
	if got := FooterLines(footer, "teen", "teen", false); len(got) != 2 || got[0] != "People found this helpful." || got[1] != "People felt welcome here." {
		t.Fatal("teen footer contains dissent/ranking", got)
	}
}

func TestPostgresConcernCapsMutingAndProtectiveSensors(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "go-concern-owner", "adult")
	id := fixtureActivity(t, s, owner, nil)
	peers := []Actor{}
	for n := 0; n < 7; n++ {
		a := fixtureUser(t, s, fmt.Sprintf("go-concern-peer-%d", n), "adult")
		mid, err := s.Join(ctx, a, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Vote(ctx, owner, mid, true, true); err != nil {
			t.Fatal(err)
		}
		peers = append(peers, a)
	}
	posts := []int64{}
	for n := 0; n < 2; n++ {
		pid, err := s.WritePost(ctx, owner, "activity", id, PostInput{Body: fmt.Sprintf("Post %d", n)}, false)
		if err != nil {
			t.Fatal(err)
		}
		posts = append(posts, pid)
		for _, a := range peers[:2] {
			if _, err := s.ToggleSentiment(ctx, a, pid, "concern", ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	now := time.Now()
	out, err := s.EvaluateConcerns(ctx, now)
	if err != nil || out.Notes != 1 || out.Failed != 0 {
		t.Fatalf("one-note author cap %+v %v", out, err)
	}
	again, err := s.EvaluateConcerns(ctx, now.Add(time.Hour))
	if err != nil || !again.Skipped {
		t.Fatal(again, err)
	}
	var notes int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM notifications_notification WHERE kind='formative_note' AND recipient_id=$1`, owner.ID).Scan(&notes); err != nil || notes != 1 {
		t.Fatal(notes, err)
	}
	muted := peers[3]
	if _, err := s.DB.Exec(ctx, `INSERT INTO notifications_notificationpreference(user_id,muted_kinds) VALUES($1,ARRAY['formative_note'])`, muted.ID); err != nil {
		t.Fatal(err)
	}
	mutedPost, err := s.WritePost(ctx, muted, "activity", id, PostInput{Body: "Muted recipient's own post"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range peers[:2] {
		if _, err := s.ToggleSentiment(ctx, a, mutedPost, "concern", ""); err != nil {
			t.Fatal(err)
		}
	}
	out, err = s.EvaluateConcerns(ctx, now.Add(25*time.Hour))
	if err != nil || out.Failed != 0 {
		t.Fatal(out, err)
	}
	var attempted bool
	if err := s.DB.QueryRow(ctx, `SELECT note_sent_at IS NOT NULL FROM social_postconcernstate WHERE post_id=$1`, mutedPost).Scan(&attempted); err != nil || !attempted {
		t.Fatal("mute bypassed lifetime cap", attempted, err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM notifications_notification WHERE kind='formative_note' AND recipient_id=$1`, muted.ID).Scan(&notes); err != nil || notes != 0 {
		t.Fatal("muted note delivered", notes, err)
	}
	third, err := s.WritePost(ctx, owner, "activity", id, PostInput{Body: "Third targeted post"}, false)
	if err != nil {
		t.Fatal(err)
	}
	posts = append(posts, third)
	for _, a := range peers[:2] {
		if _, err := s.ToggleSentiment(ctx, a, third, "concern", ""); err != nil {
			t.Fatal(err)
		}
	}
	out, err = s.EvaluateConcerns(ctx, now.Add(50*time.Hour))
	if err != nil || out.Pileon != 1 || out.Coordinated != 1 || out.Failed != 0 {
		t.Fatalf("protective sensors %+v %v", out, err)
	}
	rows, err := s.DB.Query(ctx, `SELECT kind,payload FROM safety_concernreview WHERE kind IN ('sensor_pileon','sensor_coordinated')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var raw []byte
		if err := rows.Scan(&kind, &raw); err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"flagger_ids", "user_ids", "flaggers", "members"} {
			if _, exists := payload[forbidden]; exists {
				t.Fatal("sensor stored flagger identity history", kind, payload)
			}
		}
	}
}

func TestPostgresNudgesStayOneShotAndSupervisionRequiresVotes(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "go-nudge-owner", "adult")
	id := fixtureActivity(t, s, owner, nil)
	now := time.Now()
	sent, err := s.NudgeOrganizers(ctx, now)
	if err != nil || sent != 1 {
		t.Fatal(sent, err)
	}
	sent, err = s.NudgeOrganizers(ctx, now)
	if err != nil || sent != 0 {
		t.Fatal("repeat prep nudge", sent, err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET starts_at=$2 WHERE id=$1`, id, now); err != nil {
		t.Fatal(err)
	}
	sent, err = s.NudgeRSVP(ctx, now)
	if err != nil || sent != 1 {
		t.Fatal(sent, err)
	}
	sent, err = s.NudgeRSVP(ctx, now)
	if err != nil || sent != 0 {
		t.Fatal("repeat RSVP nudge", sent, err)
	}
	ward := fixtureUser(t, s, "go-nudge-ward", "child")
	peer := fixtureUser(t, s, "go-nudge-ward-peer", "child")
	guardian := fixtureUser(t, s, "go-nudge-guardian", "adult")
	childID := fixtureActivity(t, s, ward, nil)
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, guardian.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSupervision(ctx, ward, childID, true); err != nil {
		t.Fatal(err)
	}
	mid, err := s.Join(ctx, peer, childID)
	if err != nil {
		t.Fatal(err)
	}
	sent, err = s.NudgeSupervisors(ctx, now)
	if err != nil || sent != 0 {
		t.Fatal("unvoted pending request summoned an adult", sent, err)
	}
	if err := s.Vote(ctx, ward, mid, true, false); err != nil {
		t.Fatal(err)
	}
	sent, err = s.NudgeSupervisors(ctx, now)
	if err != nil || sent != 1 {
		t.Fatal(sent, err)
	}
	sent, err = s.NudgeSupervisors(ctx, now)
	if err != nil || sent != 0 {
		t.Fatal("repeat supervisor nudge", sent, err)
	}
}

func TestPostgresAttachedPostAtomicityAndEmptyBodyGate(t *testing.T) {
	s, cleanup := testStore(t)
	defer cleanup()
	ctx := context.Background()
	author := fixtureUser(t, s, "attachment-author", "adult")
	activity := fixtureActivity(t, s, author, nil)
	if _, err := s.WritePost(ctx, author, "activity", activity, PostInput{}, false); err == nil {
		t.Fatal("empty plain post")
	}
	count := func(table string) int {
		var value int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	posts, audits := count("social_post"), count("safety_auditlog")
	for _, callback := range []AttachPostFunc{func(ctx context.Context, tx pgx.Tx, id int64) error { return nil }, func(ctx context.Context, tx pgx.Tx, id int64) error { return errors.New("synthetic scanner rejection") }} {
		if id, err := s.WritePostAttached(ctx, author, "activity", activity, PostInput{}, false, callback); err == nil || id != 0 {
			t.Fatal("attachment failure committed", id, err)
		}
		if count("social_post") != posts || count("safety_auditlog") != audits {
			t.Fatal("partial post or audit survived attachment failure")
		}
	}
	attach := func(ctx context.Context, tx pgx.Tx, id int64) error {
		_, err := tx.Exec(ctx, `INSERT INTO media_attachment(post_id,uploader_id,kind,storage_key,content_type,byte_size,sha256,original_filename,width,height,exif_stripped,created_at,expires_at,purged_at,duration_seconds,poster_content_type,poster_storage_key,processing_attempts,processing_started_at,source_storage_key,status,thumb_storage_key) VALUES($1,$2,'image','synthetic/test.webp','image/webp',1,$3,'',1,1,true,now(),NULL,NULL,0,'','',0,NULL,'','ready','')`, id, author.ID, strings.Repeat("a", 64))
		return err
	}
	id, err := s.WritePostAttached(ctx, author, "activity", activity, PostInput{}, false, attach)
	if err != nil || id < 1 || count("media_attachment") != 1 || count("social_post") != posts+1 {
		t.Fatal("valid attachment-only post failed", id, err)
	}
	s.Audit = func(context.Context, pgx.Tx, Actor, string, string, any) error {
		return errors.New("synthetic audit failure")
	}
	if id, err := s.WritePostAttached(ctx, author, "activity", activity, PostInput{Body: "text and file"}, false, attach); err == nil || id != 0 {
		t.Fatal("audit failure committed attached post", id, err)
	}
	if count("media_attachment") != 1 || count("social_post") != posts+1 {
		t.Fatal("attachment survived audit rollback")
	}
}

func TestPostgresGuardianPreviewMatchesJoinGates(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	guardian := fixtureUser(t, s, "preview-guardian", "adult")
	ward := fixtureUser(t, s, "preview-ward", "child")
	owner := fixtureUser(t, s, "preview-peer", "child")
	stranger := fixtureUser(t, s, "preview-stranger", "adult")
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, guardian.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
	id := fixtureActivity(t, s, owner, nil)
	if allowed, err := s.CanJoin(ctx, ward, id); err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	preview, err := s.GuardrailPreview(ctx, guardian, ward.ID, 50)
	if err != nil || preview["eligible"] != 1 || preview["total"] != 1 {
		t.Fatal(preview, err)
	}
	if _, err := s.GuardrailPreview(ctx, stranger, ward.ID, 50); err == nil {
		t.Fatal("unlinked guardian preview")
	}
	if _, err := s.Join(ctx, ward, id); err != nil {
		t.Fatal(err)
	}
	preview, err = s.GuardrailPreview(ctx, guardian, ward.ID, 50)
	if err != nil || preview["eligible"] != 0 || preview["total"] != 0 {
		t.Fatal("joined was counted as guardrail block", preview, err)
	}
}
