package web

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func accountWebFixture(t *testing.T) (*Server, platform.Actor) {
	t.Helper()
	if *webDomainDSN == "" {
		t.Skip("explicit isolated web fixture DSN required")
	}
	ctx := context.Background()
	db := testdb.New(t, *webDomainDSN, func(ctx context.Context, db *pgxpool.Pool) error {
		if err := catalog.New(db).Migrate(ctx); err != nil {
			return err
		}
		_, err := db.Exec(ctx, `INSERT INTO django_content_type SELECT * FROM public.django_content_type ON CONFLICT DO NOTHING`)
		return err
	})
	store := accounts.NewStore(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	auth, err := authcore.New(authcore.Config{PublicURL: "https://fixture.local"}, store)
	if err != nil {
		t.Fatal(err)
	}
	acc := accounts.New(db, auth, "synthetic-binding-secret-for-native-test", accounts.Config{})
	if err = acc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	soc := social.New(db, platform.RecordAudit)
	soc.Avatar = accounts.Avatar
	cat := catalog.New(db)
	safe := safety.New(db, safety.Config{Accounts: acc, Messaging: messaging.New(db, platform.CursorCodec{})})
	if err = safe.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../../../..")
	s := &Server{DB: db, Auth: auth, Accounts: acc, Social: soc, Safety: safe, Catalog: cat, Recommendations: recommendations.New(db, cat, soc), Messaging: messaging.New(db, platform.CursorCodec{Key: []byte("synthetic-cursor-key-32bytes-native")}), Renderer: NewRenderer(root), Config: Config{Root: root, PublicURL: "https://fixture.local"}}
	mux := http.NewServeMux()
	acc.Register(mux)
	soc.Register(mux)
	safe.Register(mux)
	s.API = mux
	return s, testdb.Actor(t, db, "generated-account-owner", "adult")
}
func TestNativeAccountPagesUseCompletePrivateContexts(t *testing.T) {
	s, a := accountWebFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'Generated EU issuer','openid4vp','adult',now(),now()+interval '5 days','{"private_marker":"raw-proof-must-never-render"}','')`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO authtoken_token(key,user_id,created) VALUES('synthetic-not-a-real-token',$1,now())`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_moderationaction(target_id,action,reason,notes,expires_at,created_at,moderator_id,target_type_id,report_id,lifted_at) VALUES($1,'warn','other','Private moderator notes',NULL,now(),NULL,(SELECT id FROM django_content_type WHERE app_label='accounts' AND model='user'),NULL,NULL)`, a.ID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"profile", "you", "settings", "my_privacy", "activity_log", "safety_record", "verify_age", "notification_preferences", "access_preferences", "account_delete", "wards", "my_guardians"} {
		t.Run(name, func(t *testing.T) {
			r := platform.WithActor(httptest.NewRequest("GET", "/"+name+"/?user_id=999999", nil), a)
			data, tpl, handled, err := s.AccountView(r, a, name)
			if err != nil || !handled {
				t.Fatal("native account view unavailable", err)
			}
			data["csrf"] = "synthetic-csrf"
			w := httptest.NewRecorder()
			if err = s.Renderer.Render(w, r, tpl, data); err != nil {
				t.Fatal("legacy template contract", name, err)
			}
			body := w.Body.String()
			for _, private := range []string{"raw-proof-must-never-render", "synthetic-not-a-real-token", "Private moderator notes"} {
				if strings.Contains(body, private) {
					t.Fatal("private operational field in account HTML", name, private)
				}
			}
			if name == "profile" && (!strings.Contains(body, "Generated EU issuer") || !strings.Contains(body, "Your avatar style") || !strings.Contains(body, "Level 0 of 5")) {
				t.Fatal("incomplete private profile")
			}
			if name == "settings" && !strings.Contains(body, "Revoke API access") {
				t.Fatal("token metadata control missing")
			}
			if name == "my_privacy" && data["decisions_count"] != 1 {
				t.Fatal("privacy count diverged from scoped record", data["decisions_count"])
			}
			if name == "safety_record" && !strings.Contains(body, "Reason: Other") {
				t.Fatal("scoped decisions absent")
			}
		})
	}
	w := httptest.NewRecorder()
	r := platform.WithActor(httptest.NewRequest("GET", "/account/export/?user_id=999999", nil), a)
	if !s.AccountDownload(w, r, a, "account_export") || w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), a.PublicID) || strings.Contains(w.Body.String(), "synthetic-not-a-real-token") {
		t.Fatal("private export download boundary")
	}
}

// The source delete page posts only its CSRF token after the GET preview;
// erasure through the registered form route must not require a confirm field.
func TestNativeAccountDeleteFormErasesWithoutConfirmField(t *testing.T) {
	s, actor, _, _, mux := webCasePortFixture(t)
	page := webCasePortHTML(t, mux, actor, "/account/delete/")
	webCasePortContains(t, page, "Permanently delete my account")
	webCasePortAbsent(t, page, `name="confirm"`)
	w := webCasePort2Post(t, mux, actor, "/account/delete/", "/account/delete/", url.Values{})
	if w.Code != 302 || w.Header().Get("Location") != "/" {
		t.Fatalf("account erasure redirect: status %d location=%q body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	cleared := map[string]bool{}
	for _, cookie := range w.Result().Cookies() {
		if cookie.MaxAge < 0 {
			cleared[cookie.Name] = true
		}
	}
	if !cleared["sessionid"] || !cleared["csrftoken"] {
		t.Fatal("erasure kept session cookies", w.Result().Cookies())
	}
	var exists bool
	if err := s.DB.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM accounts_user WHERE id=$1)`, actor.ID).Scan(&exists); err != nil || exists {
		t.Fatal("account not erased", exists, err)
	}
}
func TestNativeDisplayPreferencesAnonymousAndFakeAgeCannotGrant(t *testing.T) {
	s := &Server{Config: Config{PublicURL: "https://fixture.local"}}
	r := httptest.NewRequest("POST", "/display/", strings.NewReader("display_theme=contrast&display_text=larger&display_motion=evil"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	if !s.AccountAction(w, r, platform.Actor{}, "display_preferences") || w.Code != 302 || w.Header().Get("Location") != "/display/" {
		t.Fatal("anonymous functional preference path")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatal("invalid functional cookie accepted")
	}
	for _, cookie := range cookies {
		if !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
			t.Fatal("cookie safety regression")
		}
	}
	s, a := accountWebFixture(t)
	if _, err := s.DB.Exec(context.Background(), `UPDATE accounts_user SET age_band='unknown',cohort='unassigned',is_identity_verified=false WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	a.AgeBand, a.Cohort, a.IdentityVerified = "unknown", "unassigned", false
	r = platform.WithActor(httptest.NewRequest("POST", "/verify-age/", strings.NewReader("age=adult")), a)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	s.AccountAction(w, r, a, "verify_age")
	var verified bool
	if err := s.DB.QueryRow(context.Background(), `SELECT is_identity_verified FROM accounts_user WHERE id=$1`, a.ID).Scan(&verified); err != nil || verified {
		t.Fatal("unsigned source demo selected an age band", err)
	}
}
func TestNativeGuardianControlsDoNotEnumerateAndPruneExpiredSupervision(t *testing.T) {
	s, g := accountWebFixture(t)
	ctx := context.Background()
	ward := testdb.Actor(t, s.DB, "generated-child-ward", "child")
	co := testdb.Actor(t, s.DB, "generated-co-guardian", "adult")
	if _, err := s.DB.Exec(ctx, `DELETE FROM accounts_parentalconsent WHERE minor_id=$1`, ward.ID); err != nil {
		t.Fatal(err)
	}
	for _, guardian := range []platform.Actor{g, co} {
		if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,created_at,updated_at) VALUES($1,$2,'parent','active',now(),now())`, guardian.ID, ward.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_parentalconsent(minor_id,guardian_identifier,status,scope,granted_at,expires_at,renewal_notice,created_at,updated_at) VALUES($1,$2,'active','',now(),now()+interval '1 year','',now(),now())`, ward.ID, guardian.PublicID); err != nil {
			t.Fatal(err)
		}
	}
	place := testdb.Place(t, s.DB, "Generated child library", "osm")
	var typ int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='reading'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	activity, err := s.Social.CreateActivity(ctx, ward, social.ActivityInput{Place: place, ActivityType: typ, Title: "Private child meeting", StartsAt: time.Now().Add(24 * time.Hour), Supervised: true, MeetingPoint: "Authorized child meeting point"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at) VALUES($1,$2,'guardian','member','unknown','none',false,now(),now())`, activity, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'generated','openid4vp','adult',now(),now()-interval '1 second','{}','')`, g.ID); err != nil {
		t.Fatal(err)
	}
	earliest := 7
	if err = s.Accounts.SetGuardianGuardrail(ctx, g, ward.ID, accounts.GuardrailInput{Earliest: &earliest, Weekdays: []string{"1", "7"}, Categories: []string{"reading"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Accounts.SetGuardianGuardrail(ctx, g, ward.ID, accounts.GuardrailInput{Weekdays: []string{"17"}}); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("junk guardrail silently widened access", err)
	}
	outsider := testdb.Actor(t, s.DB, "generated-outside-child", "child")
	first := s.Accounts.SetGuardianGuardrail(ctx, g, outsider.ID, accounts.GuardrailInput{})
	second := s.Accounts.SetGuardianGuardrail(ctx, g, 9999999, accounts.GuardrailInput{})
	if !errors.Is(first, platform.ErrForbidden) || !errors.Is(second, platform.ErrForbidden) {
		t.Fatal("guardian controls enumerate child accounts", first, second)
	}
	r := platform.WithActor(httptest.NewRequest("GET", "/wards/", nil), g)
	data, tpl, _, err := s.AccountView(r, g, "wards")
	if err != nil {
		t.Fatal(err)
	}
	wards := data["wards"].([]map[string]any)
	if len(wards) != 1 {
		t.Fatal("active ward scope")
	}
	meetups := wards[0]["meetups"].([]map[string]any)
	if len(meetups) != 1 || meetups[0]["supervision_live"] != false {
		t.Fatal("expired guardian proof falsely reassured parent", meetups)
	}
	data["csrf"] = "synthetic-csrf"
	w := httptest.NewRecorder()
	if err = s.Renderer.Render(w, r, tpl, data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), "Authorized child meeting point") || !strings.Contains(w.Body.String(), "no adult seated yet") || strings.Contains(w.Body.String(), outsider.Username) {
		t.Fatal("guardian manifest scope or live supervision label")
	}
	var conversation int64
	if err = s.DB.QueryRow(ctx, `INSERT INTO messaging_conversation(kind,title,cohort,disappearing_seconds,creator_id,created_at,updated_at) VALUES('group','Generated private room','child',0,$1,now(),now()) RETURNING id`, ward.ID).Scan(&conversation); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []platform.Actor{ward, g, co} {
		role := "guardian"
		if actor.ID == ward.ID {
			role = "member"
		}
		if _, err = s.DB.Exec(ctx, `INSERT INTO messaging_participant(conversation_id,user_id,state,role,created_at) VALUES($1,$2,'active',$3,now())`, conversation, actor.ID, role); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Accounts.RevokeGuardian(ctx, g, ward.ID, s.Messaging); err != nil {
		t.Fatal(err)
	}
	var state, other, child string
	if err = s.DB.QueryRow(ctx, `SELECT (SELECT state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2),(SELECT state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$3),(SELECT state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$4)`, conversation, g.ID, co.ID, ward.ID).Scan(&state, &other, &child); err != nil || state != "removed" || other != "active" || child != "active" {
		t.Fatal("guardian prune broke co-guardian consent independence", state, other, child, err)
	}
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "ciphertext") || strings.Contains(string(raw), "raw-proof") {
		t.Fatal("private cryptographic content entered manifest")
	}
}
