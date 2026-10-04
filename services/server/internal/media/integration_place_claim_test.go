package media_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestNativeBusinessCoverRequiresCurrentExactPartnerVenue(t *testing.T) {
	m, _, db, blobs := integratedMedia(t)
	ctx := context.Background()
	owner := testdb.Actor(t, db, "generated-business-claimant", "adult")
	place := testdb.Place(t, db, "generated-business-venue", "osm")
	other := testdb.Place(t, db, "generated-other-venue", "osm")
	var partner int64
	if err := db.QueryRow(ctx, `INSERT INTO places_partner(name,kind,blurb,website,is_verified,is_active,created_at,updated_at,place_id) VALUES('Generated official venue','business','','https://example.invalid',true,true,now(),now(),$1) RETURNING id`, place).Scan(&partner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO places_placeclaim(org_name,kind,official_website,contact_email,cui,evidence,status,decided_at,created_at,claimant_id,decided_by_id,partner_id,place_id) VALUES('Generated venue','business','https://example.invalid','','','','approved',now(),now(),$1,NULL,$2,$3)`, owner.ID, partner, place); err != nil {
		t.Fatal(err)
	}
	path := sourceImage(t)
	if id, err := m.UploadPlaceCover(ctx, owner, place, path, "Official venue"); err != nil || id == 0 {
		t.Fatal("current official cover", id, err)
	}
	keys := len(blobs.keys)
	if _, err := db.Exec(ctx, `UPDATE places_partner SET place_id=$2 WHERE id=$1`, partner, other); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UploadPlaceCover(ctx, owner, place, path, "Stale claim"); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("reassigned partner granted old venue", err)
	}
	if err := m.DeletePlaceCover(ctx, owner, place); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("reassigned partner could delete old venue cover", err)
	}
	if len(blobs.keys) != keys {
		t.Fatal("stale claim processed bytes")
	}
}
