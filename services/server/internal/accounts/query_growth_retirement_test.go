package accounts

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestRetirementPostgresProfileInterestNodeQueryGrowth(t *testing.T) {
	s := accountFixture(t)
	if err := catalog.New(s.DB).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, trace := testdb.TracedPool(t, s.DB)
	ctx := context.Background()
	ids := []int64{}
	var category int64
	if err := db.QueryRow(ctx, `SELECT category_id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&category); err != nil {
		t.Fatal(err)
	}
	seed := func(begin, end int) {
		for i := begin; i < end; i++ {
			a := testdb.Actor(t, db, fmt.Sprintf("growth-avatar-user-%02d", i), "adult")
			ids = append(ids, a.ID)
			var typ int64
			if err := db.QueryRow(ctx, `INSERT INTO taxonomy_activitytype(slug,name,aliases,is_active,created_at,updated_at,category_id,parent_id,family_friendly,wellness) VALUES($1,$2,'[]',true,now(),now(),$3,NULL,false,false) RETURNING id`, fmt.Sprintf("growth-avatar-interest-%02d", i), fmt.Sprintf("Private avatar interest %02d", i), category).Scan(&typ); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `INSERT INTO recommendations_userinterest(user_id,activity_type_id,created_at) VALUES($1,$2,now()),($3,$2,now()) ON CONFLICT DO NOTHING`, a.ID, typ, ids[0]); err != nil {
				t.Fatal(err)
			}
		}
	}
	measure := func() (int64, int64) {
		trace.Reset()
		rendered, err := Avatars(ctx, db, ids)
		if err != nil || len(rendered) != len(ids) {
			t.Fatal("batch avatar projection incomplete", err)
		}
		batch := trace.Count()
		for _, uri := range rendered {
			if !strings.HasPrefix(uri, "data:image/svg+xml;base64,") || strings.Contains(uri, "Private avatar interest") {
				t.Fatal("avatar leaked readable interest names or omitted image")
			}
		}
		trace.Reset()
		if _, err := Avatar(ctx, db, ids[0]); err != nil {
			t.Fatal(err)
		}
		return batch, trace.Count()
	}
	seed(0, 4)
	smallBatch, smallNodes := measure()
	seed(4, 28)
	largeBatch, largeNodes := measure()
	if smallBatch == 0 || smallNodes == 0 || largeBatch != smallBatch || largeNodes != smallNodes {
		t.Fatalf("avatar users/nodes introduced per-record queries: batch%d->%d nodes%d->%d", smallBatch, largeBatch, smallNodes, largeNodes)
	}
}
