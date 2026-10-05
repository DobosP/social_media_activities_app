package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func permissionFixture(t *testing.T) (*Service, platform.Actor) {
	t.Helper()
	s, a := fixture(t)
	if err := accounts.NewStore(s.DB).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	permissionAssurance(t, s, a.ID)
	if _, err := s.DB.Exec(context.Background(), `UPDATE accounts_user SET role='admin',is_superuser=true WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	return s, permissionActor(t, s, a.ID)
}

func permissionAssurance(t *testing.T, s *Service, id int64) {
	t.Helper()
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) SELECT id,'synthetic','fixture',age_band,now(),now()+interval '1 year','{}','' FROM accounts_user WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func permissionUser(t *testing.T, s *Service, name, cohort string) platform.Actor {
	t.Helper()
	a := testdb.Actor(t, s.DB, name, cohort)
	permissionAssurance(t, s, a.ID)
	return a
}

func permissionActor(t *testing.T, s *Service, id int64) platform.Actor {
	t.Helper()
	var a platform.Actor
	err := s.DB.QueryRow(context.Background(), `SELECT id,public_id::text,username,display_name,role,is_staff,is_superuser,is_active,age_band,cohort,is_identity_verified FROM accounts_user WHERE id=$1`, id).Scan(&a.ID, &a.PublicID, &a.Username, &a.DisplayName, &a.Role, &a.IsStaff, &a.IsSuperuser, &a.IsActive, &a.AgeBand, &a.Cohort, &a.IdentityVerified)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func permissionAssertState(t *testing.T, s *Service, id int64, want PermissionState) {
	t.Helper()
	a := permissionActor(t, s, id)
	if got := (PermissionState{Role: a.Role, IsStaff: a.IsStaff, IsSuperuser: a.IsSuperuser}); got != want {
		t.Fatalf("permission state = %+v, want %+v", got, want)
	}
}

func permissionAuditCount(t *testing.T, s *Service) int {
	t.Helper()
	var count int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM safety_auditlog WHERE event='admin.permissions_changed'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

type permissionAuth struct {
	accounts       *accounts.Service
	auth           *authcore.Service
	token, session *http.Request
}

func permissionCredentials(t *testing.T, s *Service, id int64) permissionAuth {
	t.Helper()
	ctx := context.Background()
	store := accounts.NewStore(s.DB)
	auth, err := authcore.New(authcore.Config{PublicURL: "https://app.example"}, store)
	if err != nil {
		t.Fatal(err)
	}
	// These generated credentials belong only to this disposable synthetic schema.
	token := fmt.Sprintf("%040x", id)
	if _, err = s.DB.Exec(ctx, `INSERT INTO authtoken_token(key,user_id,created) VALUES($1,$2,now())`, token, id); err != nil {
		t.Fatal(err)
	}
	cookie := fmt.Sprintf("%052d", id)
	sum := sha256.Sum256([]byte(cookie))
	if err = store.CreateSession(ctx, authcore.Session{TokenHash: hex.EncodeToString(sum[:]), UserID: strconv.FormatInt(id, 10), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	tokenRequest := httptest.NewRequest(http.MethodGet, "https://app.example/api/accounts/me/", nil)
	tokenRequest.Header.Set("Authorization", "Token "+token)
	sessionRequest := httptest.NewRequest(http.MethodGet, "https://app.example/admin/", nil)
	sessionRequest.AddCookie(&http.Cookie{Name: "sessionid", Value: cookie})
	result := permissionAuth{accounts: accounts.New(s.DB, auth, "synthetic", accounts.Config{}), auth: auth, token: tokenRequest, session: sessionRequest}
	permissionAssertAuthentication(t, result, id, true)
	return result
}

func permissionAssertAuthentication(t *testing.T, a permissionAuth, id int64, valid bool) {
	t.Helper()
	actor, tokenErr := a.accounts.AuthenticateToken(a.token)
	user, sessionErr := a.auth.Authenticate(a.session)
	if valid {
		if tokenErr != nil || sessionErr != nil || actor.ID != id || user.ID != strconv.FormatInt(id, 10) {
			t.Fatal("synthetic native token/session could not authenticate")
		}
	} else if !errors.Is(tokenErr, authcore.ErrNotFound) || !errors.Is(sessionErr, authcore.ErrNotFound) {
		t.Fatal("changed account retained token/session authentication")
	}
}

func TestNativePermissionsLevelsRevokeAuthenticationAndAudit(t *testing.T) {
	s, manager := permissionFixture(t)
	ctx := context.Background()
	target := permissionUser(t, s, "permission-target", "adult")
	bystander := permissionUser(t, s, "permission-bystander", "adult")
	untouched := permissionCredentials(t, s, bystander.ID)
	before := PermissionState{Role: "user"}
	for i, change := range []struct {
		level PermissionLevel
		state PermissionState
	}{{PermissionModerator, PermissionState{Role: "moderator"}}, {PermissionOperator, PermissionState{Role: "user", IsStaff: true}}, {PermissionAdministrator, PermissionState{Role: "admin", IsStaff: true, IsSuperuser: true}}, {PermissionUser, PermissionState{Role: "user"}}} {
		t.Run(string(change.level), func(t *testing.T) {
			stale := permissionActor(t, s, target.ID)
			credentials := permissionCredentials(t, s, target.ID)
			if err := s.ChangePermissions(ctx, manager, target.ID, change.level, "  Synthetic reviewed change  "); err != nil {
				t.Fatal(err)
			}
			permissionAssertState(t, s, target.ID, change.state)
			permissionAssertAuthentication(t, credentials, target.ID, false)
			permissionAssertAuthentication(t, untouched, bystander.ID, true)
			if got := permissionAuditCount(t, s); got != i+1 {
				t.Fatalf("effective change audit count = %d", got)
			}
			var raw []byte
			if err := s.DB.QueryRow(ctx, `SELECT data FROM safety_auditlog WHERE event='admin.permissions_changed' AND actor_id=$1 AND target_ref=$2 ORDER BY id DESC LIMIT 1`, manager.ID, "accounts.user:"+strconv.FormatInt(target.ID, 10)).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var audit struct {
				Before  PermissionState `json:"before"`
				After   PermissionState `json:"after"`
				Reason  string          `json:"reason"`
				Revoked bool            `json:"authentication_revoked"`
			}
			if err := json.Unmarshal(raw, &audit); err != nil || audit.Before != before || audit.After != change.state || audit.Reason != "Synthetic reviewed change" || !audit.Revoked {
				t.Fatal("permission audit omitted reviewed transition")
			}
			for _, field := range []string{"age_band", "cohort", "password", "token_hash", "guardian_identifier"} {
				if strings.Contains(string(raw), `"`+field+`"`) {
					t.Fatal("permission audit exposed private account fields")
				}
			}
			fresh := permissionActor(t, s, target.ID)
			if change.level == PermissionModerator && (!fresh.Moderator() || s.Gate(ctx, fresh) == nil) {
				t.Fatal("moderator did not retain least-privilege console boundary")
			}
			if change.level == PermissionUser {
				if _, err := s.Models(ctx, stale); !errors.Is(err, platform.ErrForbidden) {
					t.Fatal("revoked administrator retained model read access", err)
				}
				if _, err := s.Save(ctx, stale, "taxonomy.activitycategory", 0, rawFields(map[string]any{"name": "Denied", "slug": "denied"})); !errors.Is(err, platform.ErrForbidden) {
					t.Fatal("revoked administrator retained curated write access", err)
				}
				if err := s.ReviewEvent(ctx, stale, 1, true, "Synthetic denied review"); !errors.Is(err, platform.ErrForbidden) {
					t.Fatal("revoked administrator retained reviewed write access", err)
				}
			}
			before = change.state
		})
	}
}

func TestNativePermissionsExternalIdentityReloadsCurrentCapabilities(t *testing.T) {
	s, manager := permissionFixture(t)
	target := permissionUser(t, s, "permission-external", "adult")
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_go_identity(provider,subject,user_id) VALUES('google','synthetic-subject',$1)`, target.ID); err != nil {
		t.Fatal(err)
	}
	store := accounts.NewStore(s.DB)
	for _, level := range []PermissionLevel{PermissionAdministrator, PermissionUser} {
		old := permissionCredentials(t, s, target.ID)
		if err := s.ChangePermissions(ctx, manager, target.ID, level, "Synthetic external identity transition"); err != nil {
			t.Fatal(err)
		}
		permissionAssertAuthentication(t, old, target.ID, false)
		// A new provider login can retain its stable subject link, but privileges
		// come from a fresh database actor, never the external identity row.
		user, err := store.FindOrCreateExternal(ctx, "google", "synthetic-subject", authcore.User{})
		if err != nil || user.ID != strconv.FormatInt(target.ID, 10) {
			t.Fatal("permission change altered stable external identity", err)
		}
		fresh, err := store.Actor(ctx, user.ID)
		want, _ := permissionState(level)
		if err != nil || permissions(fresh) != want {
			t.Fatal("external login retained stale platform authority", err)
		}
	}
}

func TestNativePermissionsAuditFailureRollsBackFlagsAndCredentials(t *testing.T) {
	s, manager := permissionFixture(t)
	target := permissionUser(t, s, "permission-rollback", "adult")
	credentials := permissionCredentials(t, s, target.ID)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `ALTER TABLE safety_auditlog ADD CONSTRAINT fixture_refuse_permission_audit CHECK(event<>'admin.permissions_changed')`); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePermissions(ctx, manager, target.ID, PermissionAdministrator, "Synthetic rollback test"); err == nil {
		t.Fatal("unaudited permission change committed")
	}
	permissionAssertState(t, s, target.ID, PermissionState{Role: "user"})
	permissionAssertAuthentication(t, credentials, target.ID, true)
	if permissionAuditCount(t, s) != 0 {
		t.Fatal("failed permission transaction left an audit")
	}
}

func TestNativePermissionsGrantRequiresCurrentAdultProof(t *testing.T) {
	s, manager := permissionFixture(t)
	ctx := context.Background()
	for _, invalid := range []struct {
		name, cohort, query string
	}{
		{"child", "child", ""},
		{"teen", "teen", ""},
		{"inactive", "adult", `UPDATE accounts_user SET is_active=false WHERE id=$1`},
		{"unverified", "adult", `UPDATE accounts_user SET is_identity_verified=false WHERE id=$1`},
		{"mismatched-band", "adult", `UPDATE accounts_user SET age_band='under_16' WHERE id=$1`},
		{"mismatched-cohort", "adult", `UPDATE accounts_user SET cohort='teen' WHERE id=$1`},
		{"unknown", "adult", `UPDATE accounts_user SET age_band='unknown',cohort='unassigned',is_identity_verified=false WHERE id=$1`},
		{"missing-assurance", "adult", `DELETE FROM accounts_ageassurance WHERE user_id=$1`},
		{"expired-assurance", "adult", `UPDATE accounts_ageassurance SET expires_at=now()-interval '1 hour' WHERE user_id=$1`},
		{"mismatched-assurance", "adult", `UPDATE accounts_ageassurance SET age_band='16_17' WHERE user_id=$1`},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			target := permissionUser(t, s, "permission-ineligible-"+invalid.name, invalid.cohort)
			if invalid.query != "" {
				if _, err := s.DB.Exec(ctx, invalid.query, target.ID); err != nil {
					t.Fatal(err)
				}
			}
			for _, level := range []PermissionLevel{PermissionModerator, PermissionOperator, PermissionAdministrator} {
				if err := s.ChangePermissions(ctx, manager, target.ID, level, "Synthetic eligibility test"); !errors.Is(err, platform.ErrForbidden) {
					t.Fatalf("ineligible %s grant = %v", level, err)
				}
			}
			permissionAssertState(t, s, target.ID, PermissionState{Role: "user"})
		})
	}
	if permissionAuditCount(t, s) != 0 {
		t.Fatal("rejected grants produced change audits")
	}
}

func TestNativePermissionsLatestAssuranceControlsGrants(t *testing.T) {
	s, manager := permissionFixture(t)
	target := permissionUser(t, s, "permission-newest-proof", "adult")
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'synthetic','fixture','adult',now()+interval '1 second',now()-interval '1 hour','{}','')`, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePermissions(ctx, manager, target.ID, PermissionModerator, "Synthetic latest assurance test"); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("older valid assurance replaced newest expired assurance", err)
	}
}

func TestNativePermissionsFreshActorAndLegacyDjangoFlags(t *testing.T) {
	for _, invalid := range []struct {
		name, query string
	}{
		{"operator", `UPDATE accounts_user SET role='user',is_superuser=false WHERE id=$1`},
		{"role-only-admin", `UPDATE accounts_user SET is_superuser=false WHERE id=$1`},
		{"revoked-staff", `UPDATE accounts_user SET is_staff=false WHERE id=$1`},
		{"inactive", `UPDATE accounts_user SET is_active=false WHERE id=$1`},
		{"expired-proof", `UPDATE accounts_ageassurance SET expires_at=now()-interval '1 hour' WHERE user_id=$1`},
		{"missing-proof", `DELETE FROM accounts_ageassurance WHERE user_id=$1`},
		{"unrecorded-bootstrap", `UPDATE accounts_user SET age_band='unknown',cohort='unassigned',is_identity_verified=false WHERE id=$1`},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			s, captured := permissionFixture(t)
			target := permissionUser(t, s, "permission-fresh-target", "adult")
			if _, err := s.DB.Exec(context.Background(), invalid.query, captured.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.PermissionGate(context.Background(), captured); !errors.Is(err, platform.ErrForbidden) {
				t.Fatal("presentation accepted stale administrator", err)
			}
			if err := s.ChangePermissions(context.Background(), captured, target.ID, PermissionAdministrator, "Synthetic fresh authorization test"); !errors.Is(err, platform.ErrForbidden) {
				t.Fatal("captured actor bypassed current authorization", err)
			}
			permissionAssertState(t, s, target.ID, PermissionState{Role: "user"})
		})
	}
	t.Run("forged-actor-with-Django-grants", func(t *testing.T) {
		s, manager := permissionFixture(t)
		ordinary := permissionUser(t, s, "permission-django-member", "adult")
		target := permissionUser(t, s, "permission-django-target", "adult")
		ctx := context.Background()
		var contentType, permission, group int64
		if err := s.DB.QueryRow(ctx, `INSERT INTO django_content_type(app_label,model) VALUES('permission_fixture','user') RETURNING id`).Scan(&contentType); err != nil {
			t.Fatal(err)
		}
		if err := s.DB.QueryRow(ctx, `INSERT INTO auth_permission(name,content_type_id,codename) VALUES('Synthetic change account',$1,'change_user') RETURNING id`, contentType).Scan(&permission); err != nil {
			t.Fatal(err)
		}
		if err := s.DB.QueryRow(ctx, `INSERT INTO auth_group(name) VALUES('Synthetic administrators') RETURNING id`).Scan(&group); err != nil {
			t.Fatal(err)
		}
		for _, statement := range []struct {
			query string
			args  []any
		}{{`INSERT INTO auth_group_permissions(group_id,permission_id) VALUES($1,$2)`, []any{group, permission}}, {`INSERT INTO accounts_user_groups(user_id,group_id) VALUES($1,$2)`, []any{ordinary.ID, group}}, {`INSERT INTO accounts_user_user_permissions(user_id,permission_id) VALUES($1,$2)`, []any{ordinary.ID, permission}}} {
			if _, err := s.DB.Exec(ctx, statement.query, statement.args...); err != nil {
				t.Fatal(err)
			}
		}
		forged := manager
		forged.ID = ordinary.ID
		if err := s.ChangePermissions(ctx, forged, target.ID, PermissionAdministrator, "Synthetic forged actor test"); !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("supplied authority or Django grants bypassed native capability gate", err)
		}
		if err := s.PermissionGate(ctx, forged); !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("legacy grants exposed permission form", err)
		}
		permissionAssertState(t, s, target.ID, PermissionState{Role: "user"})
	})
}

func TestNativePermissionsBootstrapManagerHasNoParticipationAuthority(t *testing.T) {
	s, _ := permissionFixture(t)
	ctx := context.Background()
	accountService := accounts.New(s.DB, nil, "synthetic", accounts.Config{})
	bootstrap, err := accountService.BootstrapAdministrator(ctx, "permission_bootstrap", "Synthetic-bootstrap-password-9347")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PermissionGate(ctx, bootstrap); err != nil {
		t.Fatal("audited dedicated operator denied management", err)
	}
	target := permissionUser(t, s, "permission-bootstrap-target", "adult")
	if err := s.ChangePermissions(ctx, bootstrap, target.ID, PermissionModerator, "Synthetic bootstrap-managed grant"); err != nil {
		t.Fatal(err)
	}
	fresh := permissionActor(t, s, bootstrap.ID)
	if fresh.AgeBand != "unknown" || fresh.Cohort != "unassigned" || fresh.IdentityVerified || !errors.Is(platform.Participate(ctx, s.DB, fresh), platform.ErrForbidden) {
		t.Fatal("permission management granted bootstrap age/participation authority")
	}
	var assurances, consents int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts_ageassurance WHERE user_id=$1),(SELECT count(*) FROM accounts_parentalconsent WHERE minor_id=$1)`, bootstrap.ID).Scan(&assurances, &consents); err != nil || assurances != 0 || consents != 0 {
		t.Fatal("permission management fabricated bootstrap age or consent")
	}
}

func TestNativePermissionsLastAdministratorAndSelfDemotion(t *testing.T) {
	s, manager := permissionFixture(t)
	ctx := context.Background()
	credentials := permissionCredentials(t, s, manager.ID)
	if err := s.ChangePermissions(ctx, manager, manager.ID, PermissionUser, "Synthetic last-admin test"); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("last administrator demoted itself", err)
	}
	permissionAssertAuthentication(t, credentials, manager.ID, true)
	peer := permissionUser(t, s, "permission-second-admin", "adult")
	if err := s.ChangePermissions(ctx, manager, peer.ID, PermissionAdministrator, "Synthetic administrator handover"); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePermissions(ctx, manager, manager.ID, PermissionUser, "Synthetic self-demotion after handover"); err != nil {
		t.Fatal(err)
	}
	permissionAssertState(t, s, manager.ID, PermissionState{Role: "user"})
	permissionAssertAuthentication(t, credentials, manager.ID, false)
	if err := s.ChangePermissions(ctx, manager, manager.ID, PermissionAdministrator, "Synthetic attempted self-escalation"); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("stale self-demoted actor regained administrator", err)
	}
}

func TestNativePermissionsExpiredPeerDoesNotReplaceLastAdministrator(t *testing.T) {
	s, manager := permissionFixture(t)
	peer := permissionUser(t, s, "permission-expired-peer", "adult")
	ctx := context.Background()
	if err := s.ChangePermissions(ctx, manager, peer.ID, PermissionAdministrator, "Synthetic peer setup"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_ageassurance SET expires_at=now()-interval '1 hour' WHERE user_id=$1`, peer.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePermissions(ctx, manager, manager.ID, PermissionUser, "Synthetic invalid successor test"); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("expired peer counted as remaining administrator", err)
	}
	permissionAssertState(t, s, manager.ID, PermissionState{Role: "admin", IsStaff: true, IsSuperuser: true})
}

func TestNativePermissionsConcurrentMutualDemotionRetainsManager(t *testing.T) {
	s, first := permissionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	second := permissionUser(t, s, "permission-concurrent-admin", "adult")
	if err := s.ChangePermissions(ctx, first, second.ID, PermissionAdministrator, "Synthetic second administrator"); err != nil {
		t.Fatal(err)
	}
	second = permissionActor(t, s, second.ID)
	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, change := range []struct {
		actor  platform.Actor
		target int64
	}{{first, second.ID}, {second, first.ID}} {
		go func(actor platform.Actor, target int64) {
			ready.Done()
			<-start
			results <- s.ChangePermissions(ctx, actor, target, PermissionUser, "Synthetic concurrent mutual demotion")
		}(change.actor, change.target)
	}
	ready.Wait()
	close(start)
	var applied, refused int
	for range 2 {
		err := <-results
		if err == nil {
			applied++
		} else if errors.Is(err, platform.ErrForbidden) {
			refused++
		} else {
			t.Fatal("concurrent permission change failed unexpectedly", err)
		}
	}
	var managers int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_user WHERE role='admin' AND is_staff AND is_superuser AND is_active`).Scan(&managers); err != nil || managers != 1 || applied != 1 || refused != 1 || permissionAuditCount(t, s) != 2 {
		t.Fatalf("concurrent handover: managers=%d applied=%d refused=%d err=%v", managers, applied, refused, err)
	}
}

func TestNativePermissionsNoopAndInvalidInputDoNotChangeState(t *testing.T) {
	s, manager := permissionFixture(t)
	target := permissionUser(t, s, "permission-noop", "adult")
	credentials := permissionCredentials(t, s, target.ID)
	ctx := context.Background()
	if err := s.ChangePermissions(ctx, manager, target.ID, PermissionUser, "Synthetic same permission level"); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []struct {
		id     int64
		level  PermissionLevel
		reason string
	}{{0, PermissionModerator, "review"}, {target.ID, "superuser", "review"}, {target.ID, PermissionModerator, "   "}, {target.ID, PermissionModerator, strings.Repeat("x", 2001)}, {target.ID, PermissionModerator, "review\x00reason"}} {
		if err := s.ChangePermissions(ctx, manager, invalid.id, invalid.level, invalid.reason); !errors.Is(err, platform.ErrInvalid) {
			t.Fatal("invalid permission input accepted", err)
		}
	}
	permissionAssertState(t, s, target.ID, PermissionState{Role: "user"})
	permissionAssertAuthentication(t, credentials, target.ID, true)
	if permissionAuditCount(t, s) != 0 {
		t.Fatal("no-op or invalid input manufactured change audit")
	}
}

func TestNativePermissionsReductionPreservesConsentAndPrivateBoundaries(t *testing.T) {
	s, manager := permissionFixture(t)
	ctx := context.Background()
	child := permissionUser(t, s, "permission-legacy-child", "child")
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET role='moderator',is_staff=true WHERE id=$1`, child.ID); err != nil {
		t.Fatal(err)
	}
	childAuth := permissionCredentials(t, s, child.ID)
	var consentBefore, consentAfter []byte
	if err := s.DB.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM accounts_parentalconsent c WHERE minor_id=$1`, child.ID).Scan(&consentBefore); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePermissions(ctx, manager, child.ID, PermissionUser, "Synthetic repair of legacy child privileges"); err != nil {
		t.Fatal(err)
	}
	permissionAssertAuthentication(t, childAuth, child.ID, false)
	freshChild := permissionActor(t, s, child.ID)
	if freshChild.AgeBand != "under_16" || freshChild.Cohort != "child" || !freshChild.IdentityVerified || !freshChild.IsActive {
		t.Fatal("permission reduction changed child identity/activity gates")
	}
	if err := s.DB.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM accounts_parentalconsent c WHERE minor_id=$1`, child.ID).Scan(&consentAfter); err != nil || string(consentBefore) != string(consentAfter) {
		t.Fatal("permission reduction changed parental consent")
	}
	adult := permissionUser(t, s, "permission-private-adult", "adult")
	owner := permissionUser(t, s, "permission-private-owner", "adult")
	var category, area, group, thread int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,description,created_at,updated_at,parent_id) VALUES('permission-category','Synthetic category','',now(),now(),NULL) RETURNING id`).Scan(&category); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `INSERT INTO communities_area(city,slug,name,derive_method,min_radius_m,is_active,created_at) VALUES('Cluj-Napoca','permission-area','Synthetic area','city',500,true,now()) RETURNING id`).Scan(&area); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `INSERT INTO social_group(tier,cohort,title,description,status,is_hidden,is_staff_curated,created_at,updated_at,activity_type_id,area_id,category_id,owner_id,is_publicly_listed) VALUES('category','adult','Synthetic private group','','active',false,true,now(),now(),NULL,$1,$2,$3,false) RETURNING id`, area, category, owner.ID).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `INSERT INTO social_thread(created_at,activity_id,group_id) VALUES(now(),NULL,$1) RETURNING id`, group).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePermissions(ctx, manager, adult.ID, PermissionAdministrator, "Synthetic elevated private boundary test"); err != nil {
		t.Fatal(err)
	}
	adult = permissionActor(t, s, adult.ID)
	if allowed, err := s.Social.CanReadThread(ctx, s.DB, adult, thread); err != nil || allowed {
		t.Fatal("administrator received private thread membership", err)
	}
	if allowed, err := s.Social.CanWriteThread(ctx, s.DB, adult, thread); err != nil || allowed {
		t.Fatal("administrator received private thread write access", err)
	}
	messageService := messaging.New(s.DB, platform.CursorCodec{})
	if _, err := messageService.Start(ctx, adult, "direct", []string{child.Username}, ""); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("administrator could initiate cross-cohort private contact", err)
	}
	var memberships, conversations int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM social_groupmembership WHERE user_id=$1),(SELECT count(*) FROM messaging_participant WHERE user_id=$1)`, adult.ID).Scan(&memberships, &conversations); err != nil || memberships != 0 || conversations != 0 {
		t.Fatal("permission elevation created private participation rows")
	}
}

func TestNativePermissionsHTMLManagerVisibilityCSRFAndStrictForm(t *testing.T) {
	s, manager := permissionFixture(t)
	ctx := context.Background()
	operator := permissionUser(t, s, "permission-html-operator", "adult")
	if err := s.ChangePermissions(ctx, manager, operator.ID, PermissionOperator, "Synthetic operator setup"); err != nil {
		t.Fatal(err)
	}
	operator = permissionActor(t, s, operator.ID)
	target := permissionUser(t, s, "permission-html-target", "adult")
	auth, err := authcore.New(authcore.Config{PublicURL: "https://app.example"}, accounts.NewStore(s.DB))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(HTTP{Service: s, Auth: auth}).Register(mux)
	csrf := strings.Repeat("c", 52)
	request := func(actor platform.Actor, method, path string, fields url.Values, csrfCookie bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://app.example"+path, strings.NewReader(fields.Encode()))
		if actor.ID > 0 {
			r = platform.WithActor(r, actor)
		}
		if method == http.MethodPost {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if csrfCookie {
			r.AddCookie(&http.Cookie{Name: "csrftoken", Value: csrf})
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		return out
	}
	if out := request(manager, http.MethodGet, "/admin/accounts/user/", nil, true, ""); out.Code != http.StatusOK || !strings.Contains(out.Body.String(), `name="operation" value="permissions"`) || out.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("permission form did not respect fresh manager visibility/no-store", out.Code)
	}
	if out := request(platform.Actor{}, http.MethodGet, "/admin/accounts/user/", nil, false, ""); out.Code != http.StatusNotFound {
		t.Fatal("anonymous visitor enumerated operator permission form", out.Code)
	}
	// The console is administrator-only (ADR-0035, 2026-10-05): the operator
	// preset receives the anonymous 404 on every console route and action.
	for _, path := range []string{"/admin/", "/admin/accounts/user/", "/admin/accounts.bannedidentity/", "/admin/accounts/bannedidentity/", "/admin/safety/auditlog/"} {
		if out := request(operator, http.MethodGet, path, nil, true, ""); out.Code != http.StatusNotFound || strings.Contains(out.Body.String(), "Model index") {
			t.Fatal("operator reached the administrator console", path, out.Code)
		}
	}
	var ban int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO accounts_bannedidentity(holder_hash,created_at) VALUES('synthetic-html-ban',now()) RETURNING id`).Scan(&ban); err != nil {
		t.Fatal(err)
	}
	lift := url.Values{"csrfmiddlewaretoken": {csrf}, "action": {"lift_bans"}, "ids": {strconv.FormatInt(ban, 10)}, "reason": {"Synthetic reviewed lift"}}
	save := url.Values{"csrfmiddlewaretoken": {csrf}, "operation": {"save"}, "id": {"0"}, "fields": {`{"name":"Operator HTML category","slug":"operator-html-category"}`}}
	if out := request(operator, http.MethodPost, "/admin/accounts/bannedidentity/", lift, true, "https://app.example"); out.Code != http.StatusNotFound {
		t.Fatal("operator applied a console action", out.Code)
	}
	if out := request(operator, http.MethodPost, "/admin/taxonomy/activitycategory/", save, true, "https://app.example"); out.Code != http.StatusNotFound {
		t.Fatal("operator saved curated data", out.Code)
	}
	var banned bool
	var categories int
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_bannedidentity WHERE id=$1),(SELECT count(*) FROM taxonomy_activitycategory WHERE slug='operator-html-category')`, ban).Scan(&banned, &categories); err != nil || !banned || categories != 0 {
		t.Fatal("refused operator request changed console state", banned, categories, err)
	}
	if out := request(manager, http.MethodPost, "/admin/accounts/bannedidentity/", lift, true, "https://app.example"); out.Code != http.StatusOK {
		t.Fatal("administrator could not apply the same console action", out.Code)
	}
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_bannedidentity WHERE id=$1)`, ban).Scan(&banned); err != nil || banned {
		t.Fatal("administrator console action was not applied", err)
	}
	form := func() url.Values {
		return url.Values{"csrfmiddlewaretoken": {csrf}, "operation": {"permissions"}, "id": {strconv.FormatInt(target.ID, 10)}, "level": {"moderator"}, "reason": {"Synthetic reviewed HTML grant"}}
	}
	for _, invalid := range []struct {
		name   string
		actor  platform.Actor
		cookie bool
		origin string
		edit   func(url.Values)
		status int
	}{
		{"no-CSRF-cookie", manager, false, "https://app.example", nil, http.StatusForbidden},
		{"bad-origin", manager, true, "https://other.example", nil, http.StatusForbidden},
		{"bad-CSRF-token", manager, true, "https://app.example", func(v url.Values) { v.Set("csrfmiddlewaretoken", "invalid") }, http.StatusForbidden},
		{"operator", operator, true, "https://app.example", nil, http.StatusNotFound},
		{"raw-cohort-field", manager, true, "https://app.example", func(v url.Values) { v.Set("cohort", "child") }, http.StatusBadRequest},
		{"raw-superuser-field", manager, true, "https://app.example", func(v url.Values) { v.Set("is_superuser", "true") }, http.StatusBadRequest},
		{"duplicate-level", manager, true, "https://app.example", func(v url.Values) { v.Add("level", "administrator") }, http.StatusBadRequest},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			fields := form()
			if invalid.edit != nil {
				invalid.edit(fields)
			}
			if out := request(invalid.actor, http.MethodPost, "/admin/accounts/user/", fields, invalid.cookie, invalid.origin); out.Code != invalid.status {
				t.Fatalf("rejected permission form status = %d, want %d", out.Code, invalid.status)
			}
			permissionAssertState(t, s, target.ID, PermissionState{Role: "user"})
		})
	}
	credentials := permissionCredentials(t, s, target.ID)
	out := request(manager, http.MethodPost, "/admin/accounts/user/", form(), true, "https://app.example")
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/admin/" {
		t.Fatal("reviewed HTML permission change did not redirect", out.Code)
	}
	permissionAssertState(t, s, target.ID, PermissionState{Role: "moderator"})
	permissionAssertAuthentication(t, credentials, target.ID, false)
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_superuser=false WHERE id=$1`, manager.ID); err != nil {
		t.Fatal(err)
	}
	if out := request(manager, http.MethodGet, "/admin/accounts/user/", nil, true, ""); out.Code != http.StatusNotFound || strings.Contains(out.Body.String(), `name="operation" value="permissions"`) {
		t.Fatal("stale manager retained console or permission form visibility", out.Code)
	}
}
