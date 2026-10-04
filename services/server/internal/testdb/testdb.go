// Package testdb creates isolated database fixtures. Serving code must not import
// it. Tests supply an explicit disposable DSN; no environment is consulted.
package testdb

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	nativeschema "github.com/DobosP/social_media_activities_app/services/server/internal/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Seed func(context.Context, *pgxpool.Pool) error

func New(t testing.TB, dsn string, seed Seed) *pgxpool.Pool {
	t.Helper()
	if dsn == "" {
		t.Skip("explicit disposable test DSN not supplied")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("native_fixture_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	var db *pgxpool.Pool
	t.Cleanup(func() {
		if db != nil {
			db.Close()
		}
		if _, err := admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	rows, err := admin.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename<>'spatial_ref_sys' ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	tables := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range tables {
		quoted := pgx.Identifier{name}.Sanitize()
		if _, err := admin.Exec(ctx, `CREATE TABLE `+schema+`.`+quoted+`(LIKE public.`+quoted+` INCLUDING ALL)`); err != nil {
			t.Fatal(name, err)
		}
	}
	// Render FK definitions with an empty search_path so every target is
	// qualified, then rewrite only the source schema. This retains the actual
	// full-baseline FK behavior rather than testing a constraint-free clone.
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL search_path=''`); err != nil {
		t.Fatal(err)
	}
	rows, err = tx.Query(ctx, `SELECT tabledef.relname,c.conname,pg_catalog.pg_get_constraintdef(c.oid,false) FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_class tabledef ON tabledef.oid=c.conrelid JOIN pg_catalog.pg_namespace ns ON ns.oid=tabledef.relnamespace WHERE c.contype='f' AND ns.nspname='public' ORDER BY tabledef.relname,c.conname`)
	if err != nil {
		t.Fatal(err)
	}
	type constraint struct{ Table, Name, Definition string }
	constraints := []constraint{}
	for rows.Next() {
		var c constraint
		if err := rows.Scan(&c.Table, &c.Name, &c.Definition); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		constraints = append(constraints, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range constraints {
		definition := strings.ReplaceAll(c.Definition, "public.", schema+".")
		if _, err := tx.Exec(ctx, `ALTER TABLE `+schema+`.`+pgx.Identifier{c.Table}.Sanitize()+` ADD CONSTRAINT `+pgx.Identifier{c.Name}.Sanitize()+` `+definition); err != nil {
			t.Fatal(c.Table, c.Name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.MaxConns = 4
	db, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// LIKE copies tables/FKs, but not functions or statement-level triggers.
	// Apply native additive migrations inside each isolated fixture schema too.
	if err := nativeschema.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if seed != nil {
		if err := seed(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func Actor(t testing.TB, db *pgxpool.Pool, name, cohort string) platform.Actor {
	t.Helper()
	ctx := context.Background()
	a := platform.Actor{Username: name, DisplayName: name, Cohort: cohort, AgeBand: "adult", Role: "user", IsActive: true, IdentityVerified: true}
	if cohort == "child" {
		a.AgeBand = "under_16"
	}
	if cohort == "teen" {
		a.AgeBand = "16_17"
	}
	err := db.QueryRow(ctx, `INSERT INTO accounts_user(password,last_login,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES('!',NULL,false,gen_random_uuid(),$1,$1,$2,$3,true,now(),'user',true,false,now()) RETURNING id,public_id::text`, name, a.AgeBand, cohort).Scan(&a.ID, &a.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if cohort == "child" {
		if _, err := db.Exec(ctx, `INSERT INTO accounts_parentalconsent(minor_id,guardian_identifier,status,scope,granted_at,expires_at,revoked_at,renewal_notice,created_at,updated_at) VALUES($1,'synthetic-fixture','active','',now(),now()+interval '1 year',NULL,'',now(),now())`, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	return a
}
func Place(t testing.TB, db *pgxpool.Pool, name, source string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(context.Background(), `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES($1,$2,'',NULL,'',ST_SetSRID(ST_MakePoint(23.6,46.77),4326),'{"amenity":"library"}','Test Street','2','Cluj-Napoca','','RO','24/7','{"mo":[[0,1440]],"tu":[[0,1440]],"we":[[0,1440]],"th":[[0,1440]],"fr":[[0,1440]],"sa":[[0,1440]],"su":[[0,1440]]}','','',now(),now(),'','','') RETURNING id`, name, source).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
