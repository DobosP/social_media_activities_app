package app

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
)

type groupNativeAppFixture struct {
	app         *App
	owner, peer platform.Actor
	credentials familyAppCredential
	typeID      int64
}

func newGroupNativeAppFixture(t *testing.T) groupNativeAppFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.New(t, *appDSN, nil)
	cfg := integrationConfig(t)
	cfg.AllowUserGroups = true
	cfg.StaticDir = cfg.SiteRoot + "/static"
	a, err := New(ctx, db, cfg, true)
	if err != nil {
		t.Fatal("assembled group application unavailable", err)
	}
	owner := testdb.Actor(t, db, "generated-native-group-owner", "adult")
	peer := testdb.Actor(t, db, "generated-native-group-peer", "adult")
	password := "synthetic-group-http-password-only"
	hash, err := authcore.HashPassword(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE accounts_user SET password=$2 WHERE id=$1`, owner.ID, hash); err != nil {
		t.Fatal(err)
	}
	var typeID int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball' AND is_active`).Scan(&typeID); err != nil {
		t.Fatal("canonical active group type unavailable")
	}
	return groupNativeAppFixture{app: a, owner: owner, peer: peer,
		credentials: familyAppLogin(t, a, owner.Username, password), typeID: typeID}
}

func (f groupNativeAppFixture) values(title string) url.Values {
	return url.Values{"city": {"Synthetic Native Group City"}, "activity_type": {strconv.FormatInt(f.typeID, 10)},
		"title": {title}, "description": {"Standing group description"}}
}

// Admission reservations are allowed to persist independently. These snapshots
// cover the actual group transaction and its area/audit side effects only.
func (f groupNativeAppFixture) state(t *testing.T) string {
	t.Helper()
	var state string
	if err := f.app.DB.QueryRow(context.Background(), `SELECT json_build_array((SELECT count(*) FROM social_group),(SELECT count(*) FROM social_groupmembership),(SELECT count(*) FROM social_thread),(SELECT count(*) FROM communities_area),(SELECT count(*) FROM safety_auditlog WHERE event='group.created'))::text`).Scan(&state); err != nil {
		t.Fatal("synthetic group state unavailable")
	}
	return state
}

func groupNativeAppHTML(t *testing.T, w *httptest.ResponseRecorder, alert bool) string {
	t.Helper()
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatal("native group form did not return HTML", w.Code)
	}
	if w.Header().Get("Cache-Control") != "private, no-cache" || !strings.Contains(w.Header().Get("Vary"), "Cookie") || w.Header().Get("X-Social-Runtime") != "go" {
		t.Fatal("native group form lost common private response headers")
	}
	body := w.Body.String()
	for _, part := range []string{`action="/groups/new/"`, `name="csrfmiddlewaretoken"`, `name="title"`, `name="city"`, `name="activity_type"`, `name="description"`} {
		if !strings.Contains(body, part) {
			t.Fatal("native group form lost a finite form contract", part)
		}
	}
	if strings.Contains(body, "<script") || strings.Contains(body, `name="owner"`) || strings.Contains(body, `name="owner_id"`) || strings.Contains(body, `name="cohort"`) {
		t.Fatal("ordinary adult group form exposed scripts or owner/cohort authority")
	}
	if alert && !strings.Contains(body, `role="alert"`) {
		t.Fatal("native group error was not an accessible form summary")
	}
	return body
}

func (f groupNativeAppFixture) assertSaved(t *testing.T, w *httptest.ResponseRecorder, title string) {
	t.Helper()
	if w.Code != http.StatusFound {
		t.Fatal("real group creation did not redirect", w.Code)
	}
	var id, owner, typ int64
	var cohort, description, tier, status string
	var curated, listed, hidden bool
	if err := f.app.DB.QueryRow(context.Background(), `SELECT id,owner_id,activity_type_id,cohort,description,tier,status,is_staff_curated,is_publicly_listed,is_hidden FROM social_group WHERE title=$1`, title).Scan(&id, &owner, &typ, &cohort, &description, &tier, &status, &curated, &listed, &hidden); err != nil {
		t.Fatal("real group row unavailable")
	}
	if owner != f.owner.ID || typ != f.typeID || cohort != "adult" || description != "Standing group description" || tier != "type" || status != "active" || curated || listed || hidden {
		t.Fatal("posted fields changed group ownership, cohort or default visibility")
	}
	if w.Header().Get("Location") != fmt.Sprintf("/groups/%d/", id) {
		t.Fatal("group creation did not use its existing detail route")
	}
	var members, ownerMembers, threads, audits int
	if err := f.app.DB.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM social_groupmembership WHERE group_id=$1),(SELECT count(*) FROM social_groupmembership WHERE group_id=$1 AND user_id=$2 AND role='owner' AND state='member'),(SELECT count(*) FROM social_thread WHERE group_id=$1 AND activity_id IS NULL),(SELECT count(*) FROM safety_auditlog WHERE event='group.created' AND actor_id=$2 AND target_ref=$3 AND data->>'cohort'='adult' AND data->>'staff_curated'='false')`, id, f.owner.ID, fmt.Sprintf("social.group:%d", id)).Scan(&members, &ownerMembers, &threads, &audits); err != nil {
		t.Fatal("group transaction relations unavailable")
	}
	if members != 1 || ownerMembers != 1 || threads != 1 || audits != 1 {
		t.Fatal("group creation did not atomically persist its owner membership, thread and audit")
	}
}

func TestNativeGroupAssembledCreationCSRFAndCurrentAuthority(t *testing.T) {
	f := newGroupNativeAppFixture(t)
	t.Run("real-session-native-get-and-owned-create", func(t *testing.T) {
		w := familyAppRequest(t, f.app, f.credentials, "GET", "/groups/new/", "", nil, false)
		body := groupNativeAppHTML(t, w, false)
		style := familyAppRequest(t, f.app, familyAppCredential{}, "GET", "/static/css/group-create-native.css", "", nil, false)
		if style.Code != http.StatusOK || !strings.HasPrefix(style.Header().Get("Content-Type"), "text/css") || style.Header().Get("Cache-Control") != "public, max-age=3600" || style.Header().Get("X-Content-Type-Options") != "nosniff" || style.Header().Get("X-Social-Runtime") != "go" || style.Body.String() != ".group-create-page .btn { transition: transform .04s ease; }\n" || !strings.Contains(body, `<link rel="stylesheet" href="/static/css/group-create-native.css">`) {
			t.Fatal("native group stylesheet lost actual transport or scoped bytes")
		}
		guest := familyAppRequest(t, f.app, familyAppCredential{}, "GET", "/groups/new/?owner=999999", "", nil, false)
		if guest.Code != http.StatusFound || !strings.HasPrefix(guest.Header().Get("Location"), "/login/?next=") || strings.Contains(guest.Body.String(), `name="title"`) {
			t.Fatal("anonymous group form gained authority or private content")
		}
		values := f.values("Generated native HTTP group")
		values.Set("owner", strconv.FormatInt(f.peer.ID, 10))
		values.Set("owner_id", strconv.FormatInt(f.peer.ID, 10))
		values.Set("cohort", "child")
		values.Set("is_staff_curated", "true")
		w = familyAppRequest(t, f.app, f.credentials, "POST", "/groups/new/", "https://app.example", values, true)
		f.assertSaved(t, w, "Generated native HTTP group")
	})
	t.Run("csrf-and-origin-refuse-before-domain-writes", func(t *testing.T) {
		before := f.state(t)
		for _, probe := range []struct {
			proof  bool
			origin string
		}{{false, "https://app.example"}, {true, "https://outside.example.invalid"}} {
			w := familyAppRequest(t, f.app, f.credentials, "POST", "/groups/new/", probe.origin, f.values("Forbidden synthetic group"), probe.proof)
			if w.Code != http.StatusForbidden || f.state(t) != before {
				t.Fatal("group form CSRF/Origin refusal changed group, membership, thread, area or audit")
			}
		}
	})
	t.Run("validation-rerenders-retained-escaped-values-without-writes", func(t *testing.T) {
		before := f.state(t)
		values := f.values("")
		values.Set("description", "Kept <svg> & notes")
		w := familyAppRequest(t, f.app, f.credentials, "POST", "/groups/new/", "https://app.example", values, true)
		body := groupNativeAppHTML(t, w, true)
		if !strings.Contains(body, "Synthetic Native Group City") || !strings.Contains(body, html.EscapeString(values.Get("description"))) || strings.Contains(body, "Kept <svg>") || f.state(t) != before {
			t.Fatal("group validation lost own input, escaping or transaction boundary")
		}
	})
	t.Run("current-identity-reload-and-domain-policy-refuse", func(t *testing.T) {
		before := f.state(t)
		if _, err := f.app.DB.Exec(context.Background(), `UPDATE accounts_user SET is_identity_verified=false WHERE id=$1`, f.owner.ID); err != nil {
			t.Fatal(err)
		}
		w := familyAppRequest(t, f.app, f.credentials, "POST", "/groups/new/", "https://app.example", f.values("Identity refused group"), true)
		groupNativeAppHTML(t, w, true)
		if f.state(t) != before {
			t.Fatal("stale login identity admitted a group write")
		}
		if _, err := f.app.DB.Exec(context.Background(), `UPDATE accounts_user SET is_identity_verified=true WHERE id=$1`, f.owner.ID); err != nil {
			t.Fatal(err)
		}
		policy := social.DefaultPolicyConfig()
		policy.UserGroupCohorts = map[string]bool{}
		if err := f.app.Social.ConfigurePolicy(policy); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := f.app.Social.ConfigurePolicy(social.DefaultPolicyConfig()); err != nil {
				t.Error(err)
			}
		}()
		// Existing GET admission checks the environment flag, not the complete
		// domain creation policy. Preserve that distinction in the new view.
		groupNativeAppHTML(t, familyAppRequest(t, f.app, f.credentials, "GET", "/groups/new/", "", nil, false), false)
		w = familyAppRequest(t, f.app, f.credentials, "POST", "/groups/new/", "https://app.example", f.values("Policy refused group"), true)
		groupNativeAppHTML(t, w, true)
		if f.state(t) != before {
			t.Fatal("configured group cohort policy admitted a write")
		}
	})
	t.Run("disabled-feature-and-inactive-current-account-cannot-create", func(t *testing.T) {
		before := f.state(t)
		f.app.Social.AllowUserGroups = false
		w := familyAppRequest(t, f.app, f.credentials, "POST", "/groups/new/", "https://app.example", f.values("Disabled feature group"), true)
		f.app.Social.AllowUserGroups = true
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/communities/" || f.state(t) != before {
			t.Fatal("disabled group creation did not preserve its existing refusal")
		}
		if _, err := f.app.DB.Exec(context.Background(), `UPDATE accounts_user SET is_active=false WHERE id=$1`, f.owner.ID); err != nil {
			t.Fatal(err)
		}
		w = familyAppRequest(t, f.app, f.credentials, "POST", "/groups/new/", "https://app.example", f.values("Inactive actor group"), true)
		if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/login/") || f.state(t) != before {
			t.Fatal("current inactive account retained group mutation authority")
		}
	})
}

func TestNativeGroupAssembledAuditFailureRollsBack(t *testing.T) {
	f := newGroupNativeAppFixture(t)
	before := f.state(t)
	var calls atomic.Int64
	original := f.app.Social.Audit
	f.app.Social.Audit = func(ctx context.Context, tx pgx.Tx, actor platform.Actor, event, target string, data any) error {
		if event != "group.created" {
			return original(ctx, tx, actor, event, target, data)
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(target, "social.group:"), 10, 64)
		if err != nil || id <= 0 || actor.ID != f.owner.ID {
			return errors.New("synthetic group audit target invalid")
		}
		var staged bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_group g JOIN social_groupmembership m ON m.group_id=g.id JOIN social_thread th ON th.group_id=g.id WHERE g.id=$1 AND g.owner_id=$2 AND m.user_id=$2 AND m.role='owner' AND m.state='member' AND th.activity_id IS NULL)`, id, actor.ID).Scan(&staged); err != nil || !staged {
			return errors.New("synthetic group transaction did not reach staged audit")
		}
		if err := original(ctx, tx, actor, event, target, data); err != nil {
			return err
		}
		calls.Add(1)
		return errors.New("PRIVATE_SYNTHETIC_GROUP_AUDIT_REFUSAL")
	}
	t.Cleanup(func() { f.app.Social.Audit = original })
	w := familyAppRequest(t, f.app, f.credentials, "POST", "/groups/new/", "https://app.example", f.values("Audit refused native group"), true)
	body := groupNativeAppHTML(t, w, true)
	if calls.Load() != 1 || f.state(t) != before || !strings.Contains(body, "temporarily unavailable") || strings.Contains(body, "PRIVATE_SYNTHETIC_GROUP_AUDIT_REFUSAL") {
		t.Fatal("mandatory group audit failure leaked an error or committed group/member/thread/area/audit writes")
	}
	f.app.Social.Audit = original
	w = familyAppRequest(t, f.app, f.credentials, "POST", "/groups/new/", "https://app.example", f.values("Recovered native group"), true)
	f.assertSaved(t, w, "Recovered native group")
}
