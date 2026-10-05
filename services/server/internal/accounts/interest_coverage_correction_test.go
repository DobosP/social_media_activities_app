package accounts_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/avatars"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func correctionInterestFingerprint(seed string, generation, salt, nodes int) string {
	graph := make([]avatars.Node, nodes)
	for i := range graph {
		graph[i] = avatars.Node{Category: "team_sport", Color: "#ff7a45"}
	}
	var edges []avatars.Edge
	if nodes == 2 {
		edges = []avatars.Edge{{0, 1}}
	}
	svg := avatars.RenderGeneration(generation, avatars.SignatureSeed(seed, generation, salt), graph, edges, avatars.Options{PX: avatars.CanonicalPX, UIDOverride: avatars.FingerprintUID})
	return fmt.Sprintf("%x", sha256.Sum256([]byte(svg)))
}

func TestCoverageCorrectionActualInterestSetterRefreshesChosenAndPreservesNoMintAndSalt(t *testing.T) {
	s := correctionAccounts(t)
	ctx := context.Background()
	var category int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitycategory WHERE slug='team_sport'`).Scan(&category); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"correction-interest-one", "correction-interest-two"} {
		if _, err := s.DB.Exec(ctx, `INSERT INTO taxonomy_activitytype(slug,name,aliases,is_active,created_at,updated_at,category_id,parent_id,family_friendly,wellness) VALUES($1,$1,'[]',true,now(),now(),$2,NULL,true,false)`, slug, category); err != nil {
			t.Fatal(err)
		}
	}
	setter := recommendations.New(s.DB, nil, nil)
	chosen := testdb.Actor(t, s.DB, "correction-chosen-style2", "adult")
	unpicked := testdb.Actor(t, s.DB, "correction-unpicked-interest", "adult")
	stable := testdb.Actor(t, s.DB, "correction-established-salt", "adult")
	holder := testdb.Actor(t, s.DB, "correction-forced-collision-holder", "adult")
	if _, err := setter.SetInterests(ctx, chosen, []string{"correction-interest-one"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PickStyle(ctx, chosen, 2); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.DB.QueryRow(ctx, `SELECT fingerprint FROM accounts_signatureavatar WHERE user_id=$1`, chosen.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := setter.SetInterests(ctx, chosen, []string{"correction-interest-one", "correction-interest-two"}); err != nil {
		t.Fatal(err)
	}
	var generation, salt int
	var after string
	if err := s.DB.QueryRow(ctx, `SELECT generation,salt,fingerprint FROM accounts_signatureavatar WHERE user_id=$1`, chosen.ID).Scan(&generation, &salt, &after); err != nil || generation != 2 || after == before || after != correctionInterestFingerprint(chosen.Username, 2, salt, 2) {
		t.Fatal("actual interest setter did not refresh chosen generation2 canonical fingerprint", err)
	}
	if _, err := setter.SetInterests(ctx, unpicked, []string{"correction-interest-one", "correction-interest-two"}); err != nil {
		t.Fatal(err)
	}
	var picks, interests int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts_signatureavatar WHERE user_id=$1),(SELECT count(*) FROM recommendations_userinterest WHERE user_id=$1)`, unpicked.ID).Scan(&picks, &interests); err != nil || picks != 0 || interests != 2 {
		t.Fatal("actual interest setter failed no-mint contract", err)
	}
	// Occupy only the old no-interest salt0 fingerprint. After the real interest
	// edit, salt0's new canonical visual is free, yet layout continuity keeps1.
	blocked := correctionInterestFingerprint(stable.Username, 1, 0, 0)
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_signatureavatar(user_id,generation,salt,fingerprint,created_at,updated_at) VALUES($1,1,0,$2,now(),now())`, holder.ID, blocked); err != nil {
		t.Fatal(err)
	}
	if err := s.PickStyle(ctx, stable, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT salt FROM accounts_signatureavatar WHERE user_id=$1`, stable.ID).Scan(&salt); err != nil || salt != 1 {
		t.Fatal("synthetic collision did not establish bumped salt", err)
	}
	if _, err := setter.SetInterests(ctx, stable, []string{"correction-interest-one", "correction-interest-two"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT salt,fingerprint FROM accounts_signatureavatar WHERE user_id=$1`, stable.ID).Scan(&salt, &after); err != nil || salt != 1 || after != correctionInterestFingerprint(stable.Username, 1, 1, 2) {
		t.Fatal("actual interest setter reset an established layout salt", err)
	}
	var free bool
	if err := s.DB.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM accounts_signatureavatar WHERE fingerprint=$1)`, correctionInterestFingerprint(stable.Username, 1, 0, 2)).Scan(&free); err != nil || !free {
		t.Fatal("salt continuity fixture never freed lower salt", err)
	}
}
