package recommendations

import (
	"context"
	"flag"
	"math"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

var recommendationDSN = flag.String("recommendations-test-dsn", "", "Explicit disposable PostgreSQL fixture server")

func TestHashEmbeddingNormalizedAndDeterministic(t *testing.T) {
	v := HashEmbed([]string{"type:basketball", "cat:team_sport", "cat:sport"})
	norm := 0.0
	for _, x := range v {
		norm += x * x
	}
	if math.Abs(norm-1) > 1e-12 {
		t.Fatal(norm)
	}
	if v != HashEmbed([]string{"type:basketball", "cat:team_sport", "cat:sport"}) {
		t.Fatal("nondeterministic taxonomy embedding")
	}
	if HashEmbed(nil) != [64]float64{} {
		t.Fatal("empty signal became perfect match")
	}
}
func TestDistanceAndAccessAreSoftOnly(t *testing.T) {
	near, far := 1000.0, 5000.0
	if Score(0.2, &near, false) <= Score(0.2, &far, false) {
		t.Fatal("distance inversion")
	}
	if Score(1.5, &far, false) != 0 {
		t.Fatal("negative similarity reversed distance")
	}
	if Score(0.2, &near, true) <= Score(0.2, &near, false) {
		t.Fatal("access match not a soft lift")
	}
}
func TestPostgresPreferencesColdStartAndRankedSafety(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t, *recommendationDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	cat := catalog.New(db)
	soc := social.New(db, platform.RecordAudit)
	s := New(db, cat, soc)
	soc.AfterActivitySave = s.RecomputeEmbeddingTx
	viewer := testdb.Actor(t, db, "go-rec-viewer", "adult")
	owner := testdb.Actor(t, db, "go-rec-owner", "adult")
	minor := testdb.Actor(t, db, "go-rec-minor", "child")
	place := testdb.Place(t, db, "Recommendation venue", "osm")
	var typ int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	id, err := soc.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Visible upcoming", StartsAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = soc.CreateActivity(ctx, minor, social.ActivityInput{Place: place, ActivityType: typ, Title: "Child cohort", StartsAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	cold, err := s.Recommend(ctx, viewer, 20, catalog.Near{}, true)
	if err != nil || len(cold) != 1 || cold[0].Cosine != nil || cold[0].Reason != "soonest first" {
		t.Fatalf("cold=%+v %v", cold, err)
	}
	known, err := s.SetInterests(ctx, viewer, []string{"basketball", "unknown"})
	if err != nil || len(known) != 1 || known[0] != "basketball" {
		t.Fatal(known, err)
	}
	var embeddings int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM recommendations_activityembedding WHERE activity_id=$1`, id).Scan(&embeddings); err != nil || embeddings != 1 {
		t.Fatal("post-save signal missing", embeddings, err)
	}
	if count, err := s.RecomputeEmbeddings(ctx); err != nil || count != 2 {
		t.Fatal("batched backfill", count, err)
	}
	ranked, err := s.Recommend(ctx, viewer, 20, catalog.Near{}, true)
	if err != nil || len(ranked) != 1 || ranked[0].Cosine == nil || math.Abs(*ranked[0].Cosine) > 1e-5 {
		t.Fatalf("ranked=%+v %v", ranked, err)
	}
	if ranked[0].Reason != "matches your interest in Basketball" {
		t.Fatal(ranked[0].Reason)
	}
	// Independent Django ORM oracle uses two separate EXISTS predicates for
	// exclude(memberships__user=user,memberships__state=MEMBER). A pending request
	// therefore excludes a meetup that already has its owner seated.
	if _, err := soc.Join(ctx, viewer, id); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Recommend(ctx, viewer, 20, catalog.Near{}, true)
	if err != nil || len(pending) != 0 {
		t.Fatal("source ORM exclusion changed", pending, err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, owner.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	ranked, err = s.Recommend(ctx, viewer, 20, catalog.Near{}, true)
	if err != nil || len(ranked) != 0 {
		t.Fatalf("ranking widened block/cohort=%+v %v", ranked, err)
	}
}

func TestPostgresSavedSearchOwnershipAndOnceOnlyMatching(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t, *recommendationDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	cat := catalog.New(db)
	soc := social.New(db, platform.RecordAudit)
	s := New(db, cat, soc)
	soc.AfterActivitySave = s.RecomputeEmbeddingTx
	viewer := testdb.Actor(t, db, "saved-viewer", "adult")
	other := testdb.Actor(t, db, "saved-other", "adult")
	place := testdb.Place(t, db, "Saved venue", "osm")
	var typ int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateSavedSearch(ctx, viewer, SavedSearchInput{ActivityType: &typ, City: "Cluj-Napoca"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SavedSearch(ctx, other, id); err == nil {
		t.Fatal("other user read search")
	}
	if err = s.DeleteSavedSearch(ctx, other, id); err == nil {
		t.Fatal("other user deleted search")
	}
	if _, err = s.CreateSavedSearch(ctx, viewer, SavedSearchInput{ActivityType: &typ, City: "Cluj-Napoca"}); err == nil {
		t.Fatal("duplicate search")
	}
	_, err = soc.CreateActivity(ctx, other, social.ActivityInput{Place: place, ActivityType: typ, Title: "Saved match", StartsAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.MatchSavedSearches(ctx, time.Now())
	if err != nil || first.Scanned != 1 || first.Notified != 1 || first.Skipped != 0 {
		t.Fatal(first, err)
	}
	if err := s.DeleteSavedSearch(ctx, viewer, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateSavedSearch(ctx, viewer, SavedSearchInput{ActivityType: &typ, City: "Cluj-Napoca"}); err != nil {
		t.Fatal(err)
	}
	second, err := s.MatchSavedSearches(ctx, time.Now())
	if err != nil || second.Scanned != 0 {
		t.Fatal("replayed after recreation", second, err)
	}
}

func TestPostgresGuardianTopicSteeringIsLinkedChildOnly(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t, *recommendationDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	cat := catalog.New(db)
	soc := social.New(db, platform.RecordAudit)
	s := New(db, cat, soc)
	guardian := testdb.Actor(t, db, "topic-guardian", "adult")
	ward := testdb.Actor(t, db, "topic-ward", "child")
	stranger := testdb.Actor(t, db, "topic-stranger", "adult")
	if _, err := db.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, guardian.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWardTopics(ctx, stranger, ward.ID, []string{"sport"}); err == nil {
		t.Fatal("stranger changed ward topics")
	}
	known, err := s.SetWardTopics(ctx, guardian, ward.ID, []string{"sport", "team_sport", "unknown"})
	if err != nil || len(known) != 1 || known[0] != "sport" {
		t.Fatal(known, err)
	}
	if _, err := db.Exec(ctx, `UPDATE accounts_user SET cohort='teen' WHERE id=$1`, ward.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWardTopics(ctx, guardian, ward.ID, nil); err == nil {
		t.Fatal("guardian changed self-managing teen topics")
	}
}
