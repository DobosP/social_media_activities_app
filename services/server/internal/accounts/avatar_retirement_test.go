package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func retirementOccupyAvatar(t *testing.T, s *Service, user int64, generation int, salts []int) {
	t.Helper()
	ctx := context.Background()
	in, err := avatarInput(ctx, s.DB, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, salt := range salts {
		holder := accountUser(t, s, fmt.Sprintf("retirement-holder-%d-%d", user, salt), "adult", "adult")
		fingerprint := canonicalFingerprint(in, generation, salt)
		if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_signatureavatar(user_id,generation,salt,fingerprint,created_at,updated_at) VALUES($1,$2,0,$3,now(),now())`, holder.ID, generation, fingerprint); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRetirementAvatarCollisionRetryAndStrictNoop(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	u := accountUser(t, s, "retirement-avatar-collision", "adult", "adult")
	retirementOccupyAvatar(t, s, u.ID, 1, []int{0})
	if err := s.PickStyle(ctx, u, 1); err != nil {
		t.Fatal(err)
	}
	var salt int
	var fingerprint string
	var updated time.Time
	if err := s.DB.QueryRow(ctx, `SELECT salt,fingerprint,updated_at FROM accounts_signatureavatar WHERE user_id=$1`, u.ID).Scan(&salt, &fingerprint, &updated); err != nil {
		t.Fatal(err)
	}
	in, err := avatarInput(ctx, s.DB, u.ID)
	if err != nil || salt != 1 || fingerprint != canonicalFingerprint(in, 1, 1) {
		t.Fatal("real unique collision did not choose next canonical salt")
	}
	if err := s.PickStyle(ctx, u, 1); err != nil {
		t.Fatal(err)
	}
	var after time.Time
	var audits int
	if err := s.DB.QueryRow(ctx, `SELECT updated_at FROM accounts_signatureavatar WHERE user_id=$1`, u.ID).Scan(&after); err != nil || !after.Equal(updated) {
		t.Fatal("same-generation pick rewrote timestamp")
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event='avatar.style_changed' AND actor_id=$1`, u.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("same-generation pick repeated change audit")
	}
	style, err := s.Style(ctx, s.DB, u.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(style)
	if strings.Contains(string(raw), fingerprint) || strings.Contains(string(raw), `"salt"`) || strings.Contains(string(raw), `"fingerprint"`) {
		t.Fatal("style API leaked uniqueness internals")
	}
}

func TestRetirementAvatarSaltExhaustionIsAtomic(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	u := accountUser(t, s, "retirement-avatar-exhausted", "adult", "adult")
	salts := make([]int, 16)
	for i := range salts {
		salts[i] = i
	}
	retirementOccupyAvatar(t, s, u.ID, 2, salts)
	if err := s.PickStyle(ctx, u, 2); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("exhausted uniqueness candidates admitted a pick", err)
	}
	var rows, audits int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts_signatureavatar WHERE user_id=$1),(SELECT count(*) FROM safety_auditlog WHERE actor_id=$1 AND event='avatar.style_changed')`, u.ID).Scan(&rows, &audits); err != nil || rows != 0 || audits != 0 {
		t.Fatal("exhausted pick retained partial state")
	}
}

func TestRetirementConcurrentFirstAvatarPicksConverge(t *testing.T) {
	s := accountFixture(t)
	u := accountUser(t, s, "retirement-avatar-race", "adult", "adult")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, generation := range []int{1, 2} {
		go func(generation int) { ready.Done(); <-start; results <- s.PickStyle(ctx, u, generation) }(generation)
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var rows, generation int
	if err := s.DB.QueryRow(ctx, `SELECT count(*),min(generation) FROM accounts_signatureavatar WHERE user_id=$1`, u.ID).Scan(&rows, &generation); err != nil || rows != 1 || (generation != 1 && generation != 2) {
		t.Fatal("concurrent initial choices did not converge to one valid pick")
	}
}

func TestRetirementInterestRefreshPreservesChosenSaltAndDoesNotMint(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `WITH category AS (INSERT INTO taxonomy_activitycategory(name,slug,description,parent_id,created_at,updated_at) VALUES('Retirement category','retirement-category','',NULL,now(),now()) RETURNING id) INSERT INTO taxonomy_activitytype(name,slug,aliases,is_active,wellness,family_friendly,category_id,parent_id,created_at,updated_at) SELECT 'Retirement basketball','basketball','[]',true,false,false,id,NULL,now(),now() FROM category`); err != nil {
		t.Fatal(err)
	}
	chosen := accountUser(t, s, "retirement-avatar-refresh", "adult", "adult")
	unpicked := accountUser(t, s, "retirement-avatar-unpicked", "adult", "adult")
	retirementOccupyAvatar(t, s, chosen.ID, 1, []int{0})
	if err := s.PickStyle(ctx, chosen, 1); err != nil {
		t.Fatal(err)
	}
	var previous string
	if err := s.DB.QueryRow(ctx, `SELECT fingerprint FROM accounts_signatureavatar WHERE user_id=$1`, chosen.ID).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	for _, user := range []platform.Actor{chosen, unpicked} {
		if err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO recommendations_userinterest(user_id,activity_type_id,created_at) SELECT $1,id,now() FROM taxonomy_activitytype WHERE slug='basketball'`, user.ID); err != nil {
				return err
			}
			return RefreshAvatarFingerprint(ctx, tx, user)
		}); err != nil {
			t.Fatal(err)
		}
	}
	var salt, unpickedRows int
	var fingerprint string
	if err := s.DB.QueryRow(ctx, `SELECT salt,fingerprint FROM accounts_signatureavatar WHERE user_id=$1`, chosen.ID).Scan(&salt, &fingerprint); err != nil || salt != 1 || fingerprint == previous {
		t.Fatal("interest edit reset established layout or retained stale fingerprint")
	}
	in, err := avatarInput(ctx, s.DB, chosen.ID)
	if err != nil || fingerprint != canonicalFingerprint(in, 1, 1) {
		t.Fatal("refreshed fingerprint differs from canonical chosen visual")
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_signatureavatar WHERE user_id=$1`, unpicked.ID).Scan(&unpickedRows); err != nil || unpickedRows != 0 {
		t.Fatal("interest edit silently minted unpicked user's signature")
	}
}
