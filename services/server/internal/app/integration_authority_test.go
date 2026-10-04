package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestNativeDocumentationRoutesAreReachableThroughApplication(t *testing.T) {
	db := testdb.New(t, *appDSN, nil)
	a, err := New(context.Background(), db, integrationConfig(t), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/schema/", "/api/docs/"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "https://app.example"+path, nil))
		if w.Code != 200 || w.Header().Get("X-Social-Runtime") != "go" || w.Header().Get("Set-Cookie") != "" {
			t.Fatal("anonymous native documentation unavailable", path, w.Code)
		}
		if path == "/api/schema/" {
			var schema struct {
				OpenAPI    string                     `json:"openapi"`
				Operations int                        `json:"x-native-api-operation-count"`
				Paths      map[string]json.RawMessage `json:"paths"`
			}
			if json.Unmarshal(w.Body.Bytes(), &schema) != nil || schema.OpenAPI != "3.0.3" || schema.Operations != 380 || len(schema.Paths) != 318 {
				t.Fatal("assembled documentation inventory differs")
			}
		} else if !strings.Contains(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), "/api/schema/") {
			t.Fatal("native guide is not reachable HTML")
		}
	}
}

// The actor may have been loaded before a separately committed rate reservation.
// Every identity or capability withdrawal must invalidate that captured actor.
func TestParticipationRejectsAuthorityChangedBetweenTransactions(t *testing.T) {
	db := testdb.New(t, *appDSN, nil)
	ctx := context.Background()
	actor := testdb.Actor(t, db, "authority-snapshot", "adult")
	if err := platform.Participate(ctx, db, actor); err != nil {
		t.Fatal("unchanged authority", err)
	}
	for _, check := range []struct {
		name, sql string
	}{
		{"inactive", `UPDATE accounts_user SET is_active=false WHERE id=$1`},
		{"identity-withdrawn", `UPDATE accounts_user SET is_identity_verified=false WHERE id=$1`},
		{"age-changed", `UPDATE accounts_user SET age_band='16_17' WHERE id=$1`},
		{"cohort-changed", `UPDATE accounts_user SET cohort='teen' WHERE id=$1`},
		{"role-changed", `UPDATE accounts_user SET role='moderator' WHERE id=$1`},
		{"staff-changed", `UPDATE accounts_user SET is_staff=true WHERE id=$1`},
		{"superuser-changed", `UPDATE accounts_user SET is_superuser=true WHERE id=$1`},
	} {
		t.Run(check.name, func(t *testing.T) {
			if _, err := db.Exec(ctx, `UPDATE accounts_user SET is_active=true,is_identity_verified=true,age_band='adult',cohort='adult',role='user',is_staff=false,is_superuser=false WHERE id=$1`, actor.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, check.sql, actor.ID); err != nil {
				t.Fatal(err)
			}
			if err := platform.Participate(ctx, db, actor); !errors.Is(err, platform.ErrForbidden) {
				t.Fatal("stale actor was admitted", err)
			}
		})
	}
}

func TestFreshParticipationRetainsAssuranceAndConsentGates(t *testing.T) {
	db := testdb.New(t, *appDSN, nil)
	ctx := context.Background()
	adult := testdb.Actor(t, db, "current-adult-assurance", "adult")
	child := testdb.Actor(t, db, "current-child-consent", "child")
	if err := platform.Participate(ctx, db, child); err != nil {
		t.Fatal("current consent", err)
	}
	if _, err := db.Exec(ctx, `UPDATE accounts_parentalconsent SET status='revoked',revoked_at=now() WHERE minor_id=$1`, child.ID); err != nil {
		t.Fatal(err)
	}
	if err := platform.Participate(ctx, db, child); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("revoked consent was admitted", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'synthetic','fixture','adult',now(),now()-interval '1 second','{}','')`, adult.ID); err != nil {
		t.Fatal(err)
	}
	if err := platform.Participate(ctx, db, adult); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("expired assurance was admitted", err)
	}
}
