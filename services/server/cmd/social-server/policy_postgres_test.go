package main

import (
	"context"
	"errors"
	"flag"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/app"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/DobosP/social_media_activities_app/services/server/internal/web"
)

var configurationDSN = flag.String("configuration-test-dsn", "", "explicit disposable configuration fixture DSN")

func TestPostgresCLIConfigChangesDomainAdmissionAndVisibility(t *testing.T) {
	db := testdb.New(t, *configurationDSN, nil)
	ctx := context.Background()
	c, err := configuration(fixtureEnv(map[string]string{"FACT_VOTE_RATE_LIMIT": "1", "FACT_VOTE_RATE_WINDOW_SECONDS": "300", "CLOSURE_REPORT_THRESHOLD": "1", "CLOSURE_REPORT_DECAY_SECONDS": "2592000", "DJANGO_REQUIRE_SHARED_STATE": "true"}), fixtureOptions())
	if err != nil {
		t.Fatal(err)
	}
	soc := social.New(db, nil)
	cat := catalog.New(db)
	msg := &messaging.Service{DB: db}
	a := &app.App{DB: db, Social: soc, Catalog: cat, Media: &media.Service{}, Messaging: msg, Accounts: &accounts.Service{DB: db}, Safety: safety.New(db, safety.Config{Messaging: msg}), Recommendations: recommendations.New(db, cat, soc), Web: &web.Server{}}
	if err = c.apply(a); err != nil {
		t.Fatal(err)
	}
	actor := platform.Actor{Role: "user", Cohort: "adult", AgeBand: "adult", IdentityVerified: true, IsActive: true}
	err = db.QueryRow(ctx, `INSERT INTO accounts_user(password,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES('!',false,gen_random_uuid(),'fixture_config_user','Fixture','adult','adult',true,now(),'user',true,false,now()) RETURNING id,public_id::text`).Scan(&actor.ID, &actor.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	var place int64
	err = db.QueryRow(ctx, `INSERT INTO places_place(name,source,osm_type,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES('Fixture place','manual','','',ST_SetSRID(ST_MakePoint(23.6,46.77),4326),'{}','','','Cluj-Napoca','','RO','','{}','','',now(),now(),'Fixture credit','CC0','https://fixture.invalid') RETURNING id`).Scan(&place)
	if err != nil {
		t.Fatal(err)
	}
	if err = cat.VoteFact(ctx, actor, place, "toilets", true); err != nil {
		t.Fatal("first configured fact vote", err)
	}
	if err = cat.VoteFact(ctx, actor, place, "shade", true); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("CLI rate limit did not alter actual domain admission", err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM places_placefactvote WHERE user_id=$1`, actor.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("denied vote changed state", err)
	}
	if _, err = cat.ReportVenue(ctx, actor, place, true); err != nil {
		t.Fatal("configured closure report", err)
	}
	policyCtx := catalog.WithPolicy(ctx, c.CatalogPolicy)
	if err = db.QueryRow(policyCtx, `SELECT count(*) FROM places_place p WHERE p.id=$1 AND `+catalog.PolicyFromContext(policyCtx).PlaceSQL(), place).Scan(&count); err != nil || count != 0 {
		t.Fatal("context reader did not withhold configured closure", err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM places_place p WHERE p.id=$1 AND `+catalog.DefaultPolicy().PlaceSQL(), place).Scan(&count); err != nil || count != 1 {
		t.Fatal("configured policy polluted default runtime", err)
	}
}
