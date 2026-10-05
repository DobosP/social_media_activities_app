package safety

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	nativeschema "github.com/DobosP/social_media_activities_app/services/server/internal/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

var safetyTestDSN = flag.String("safety-test-dsn", "", "disposable native safety fixture database")

func safetyFixture(t *testing.T) *Service {
	t.Helper()
	if *safetyTestDSN == "" {
		t.Skip("explicit disposable safety-test-dsn not supplied")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, *safetyTestDSN)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("safety_native_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	rows, err := admin.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	tables := []string{}
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	rows.Close()
	for _, table := range tables {
		if _, err = admin.Exec(ctx, "CREATE TABLE "+quoted+"."+pgx.Identifier{table}.Sanitize()+" (LIKE public."+pgx.Identifier{table}.Sanitize()+" INCLUDING ALL)"); err != nil {
			t.Fatal(table, err)
		}
	}
	if _, err = admin.Exec(ctx, "INSERT INTO "+quoted+`.django_content_type SELECT * FROM public.django_content_type`); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(*safetyTestDSN)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = name + ",public"
	config.MaxConns = 4
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
		admin.Close()
	})
	// LIKE fixtures also need the native functions, capacity seed and triggers.
	if err = nativeschema.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := accounts.NewStore(db)
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	auth, err := authcore.New(authcore.Config{PublicURL: "https://app.example"}, store)
	if err != nil {
		t.Fatal(err)
	}
	acc := accounts.New(db, auth, "test-binding-secret-that-is-public-32", accounts.Config{})
	if err = acc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s := New(db, Config{Accounts: acc, Messaging: messaging.New(db, platform.CursorCodec{})})
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}
func user(t *testing.T, s *Service, name string, mod bool) platform.Actor {
	t.Helper()
	var a platform.Actor
	a.Username = name
	a.DisplayName = name
	a.Cohort = "adult"
	a.AgeBand = "adult"
	a.IsActive = true
	a.IdentityVerified = true
	a.Role = "user"
	if mod {
		a.Role = "moderator"
	}
	err := s.DB.QueryRow(context.Background(), `INSERT INTO accounts_user(password,last_login,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES('!',NULL,false,gen_random_uuid(),$1,$1,'adult','adult',true,now(),$2,true,false,now()) RETURNING id,public_id::text`, name, a.Role).Scan(&a.ID, &a.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func request(s *Service, a platform.Actor, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if a.ID > 0 {
		r = platform.WithActor(r, a)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, r)
	return out
}

func TestTriageRomanianBoundariesAndPhoneEmail(t *testing.T) {
	for raw, want := range map[string]string{"Scrie-mi pe WhatsApp 0712-345-678": "phone-number,scrie-mi,whatsapp", "snapshot instant add message": "", "numărul tău? foo@example.com": "email-address,numarul tau"} {
		if got := strings.Join(ContactHintTerms(raw), ","); got != want {
			t.Fatalf("triage %q got%s want%s", raw, got, want)
		}
	}
}
func TestSafetyHTTPPermissionWalls(t *testing.T) {
	s := New(nil, Config{})
	for _, path := range []string{"/api/safety/moderation/reports/", "/api/safety/moderation/appeals/", "/api/safety/moderation/concerns/"} {
		out := request(s, platform.Actor{}, "GET", path, "")
		if out.Code != 401 {
			t.Fatal(path, out.Code)
		}
		out = request(s, platform.Actor{ID: 1, IsActive: true, Role: "user"}, "GET", path, "")
		if out.Code != 403 {
			t.Fatal(path, out.Code)
		}
	}
}

func TestNativeReportSanctionAppealAndOverlap(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	moderator := user(t, s, "reviewer", true)
	offender := user(t, s, "subject", false)
	reporter := user(t, s, "reporter", false)
	target, err := s.ResolveTarget(ctx, s.DB, "accounts", "user", offender.ID)
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.FileReport(ctx, reporter, target, "harassment", "own submitted detail")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.Report(ctx, report, false)
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)
	if len(dto) != 5 || dto["detail"] != "own submitted detail" {
		t.Fatal("report projection mismatch", dto)
	}
	first, err := s.TakeAction(ctx, moderator, target, ActionInput{Decision: "suspend", Reason: "harassment", SuspendDays: 1}, report)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.TakeAction(ctx, moderator, target, ActionInput{Decision: "timed_ban", Reason: "spam", SuspendDays: 2}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var active bool
	_ = s.DB.QueryRow(ctx, `SELECT is_active FROM accounts_user WHERE id=$1`, offender.ID).Scan(&active)
	if active {
		t.Fatal("sanction did not deactivate")
	}
	appeal, err := s.FileAppeal(ctx, offender, first, "Please review this decision.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.FileAppeal(ctx, reporter, first, "Other person contest"); err == nil {
		t.Fatal("proxy appeal accepted")
	}
	result, err := s.ResolveAppeal(ctx, moderator, appeal, true, "private review notes")
	if err != nil || result.Reactivated {
		t.Fatal("overturn bypassed another sanction", result, err)
	}
	secondAppeal, err := s.FileAppeal(ctx, offender, second, "Please review the other decision.")
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.ResolveAppeal(ctx, moderator, secondAppeal, true, "")
	if err != nil || !result.Reactivated {
		t.Fatal("last sanction overturn failed", result, err)
	}
	rows, err := s.Appeals(ctx, offender.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(rows)
	if strings.Contains(string(encoded), "private review notes") || strings.Contains(string(encoded), "decided_by") {
		t.Fatal("staff appeal details leaked")
	}
	valid, _, err := s.VerifyAuditChain(ctx, nil)
	if err != nil || !valid {
		t.Fatal("native chain invalid", err)
	}
}

func TestNativeExpiredSanctionsConcurrentLiftExactlyOnce(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	mod := user(t, s, "mod-lift", true)
	subject := user(t, s, "lift-subject", false)
	target, err := s.ResolveTarget(ctx, s.DB, "accounts", "user", subject.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = s.TakeAction(ctx, mod, target, ActionInput{Decision: "suspend", Reason: "other", SuspendDays: 1}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.DB.Exec(ctx, `UPDATE safety_moderationaction SET expires_at=now()-interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	count := 0
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			n, err := s.LiftSuspensions(ctx)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			count += n
			mu.Unlock()
		}()
	}
	wait.Wait()
	if count != 1 {
		t.Fatal("duplicate account reactivation", count)
	}
	var notices int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND title='Your suspension has ended'`, subject.ID).Scan(&notices)
	if notices != 1 {
		t.Fatal("duplicate dignity notice", notices)
	}
}

func TestLegacyPythonChainAndNativeFloatAppend(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	raw, err := os.ReadFile("testdata/python-audit-chain.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Event, Target, Created, Previous, Hash string
		Data                                   json.RawMessage
	}
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		created, err := time.Parse(time.RFC3339Nano, row.Created)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.DB.Exec(ctx, `INSERT INTO safety_auditlog(actor_id,actor_ref,event,target_ref,data,created_at,prev_hash,hash) VALUES(NULL,NULL,$1,$2,$3,$4,$5,$6)`, row.Event, row.Target, row.Data, created, row.Previous, row.Hash); err != nil {
			t.Fatal(err)
		}
	}
	valid, checkpoint, err := s.VerifyAuditChain(ctx, nil)
	if err != nil || !valid {
		t.Fatal("untouched Django float chain rejected", err)
	}
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		return platform.RecordAudit(ctx, tx, platform.Actor{}, "native.float", "", map[string]any{"float": 1000000.0, "large_float": 1e16, "small": 1e-7, "unicode": "Cât 🧪"})
	})
	if err != nil {
		t.Fatal(err)
	}
	valid, _, err = s.VerifyAuditChain(ctx, checkpoint)
	if err != nil || !valid {
		t.Fatal("native float append broke incremental check", err)
	}
	if _, err = s.DB.Exec(ctx, `UPDATE safety_auditlog SET data='{"tampered":true}' WHERE id=$1`, checkpoint.LastID); err != nil {
		t.Fatal(err)
	}
	valid, _, err = s.VerifyAuditChain(ctx, nil)
	if err != nil || valid {
		t.Fatal("tampering not detected")
	}
}

func TestNativeAuthorityReferralNoSubjectNoticeAndProofScope(t *testing.T) {
	s := safetyFixture(t)
	mod := user(t, s, "authority-mod", true)
	subject := user(t, s, "authority-subject", false)
	out := request(s, mod, "POST", "/api/safety/moderation/referrals/", fmt.Sprintf(`{"subject":"%s","reason":"grooming","authority":"igpr","reference":"fixture-reference","notes":"staff confidential"}`, subject.PublicID))
	if out.Code != 201 {
		t.Fatal(out.Code, out.Body)
	}
	var body map[string]any
	_ = json.Unmarshal(out.Body.Bytes(), &body)
	if len(body) != 7 || body["chain_valid"] != true || body["authority"] != "Romanian Police (IGPR)" {
		t.Fatal("proof DTO mismatch", body)
	}
	if strings.Contains(out.Body.String(), "staff confidential") || strings.Contains(out.Body.String(), "referred_by") {
		t.Fatal("referral notes/moderator leaked")
	}
	var notices int
	_ = s.DB.QueryRow(context.Background(), `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1`, subject.ID).Scan(&notices)
	if notices != 0 {
		t.Fatal("subject tipped off to authority referral")
	}
}

func safetyPost(t *testing.T, s *Service, a platform.Actor, deleted bool) int64 {
	t.Helper()
	var thread, post int64
	err := s.DB.QueryRow(context.Background(), `INSERT INTO social_thread(created_at,activity_id,group_id) VALUES(now(),NULL,1) RETURNING id`).Scan(&thread)
	if err != nil {
		t.Fatal(err)
	}
	err = s.DB.QueryRow(context.Background(), `INSERT INTO social_post(body,created_at,updated_at,author_id,thread_id,is_hidden,is_announcement,reply_to_id,shared_activity_id,shared_event_id,shared_place_id,is_author_deleted) VALUES('generated private post',now(),now(),$1,$2,$3,false,NULL,NULL,NULL,NULL,$3) RETURNING id`, a.ID, thread, deleted).Scan(&post)
	if err != nil {
		t.Fatal(err)
	}
	return post
}

func TestNativeRemovalOverlapAuthorWithdrawalAndHonestNotice(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	mod := user(t, s, "remove-mod", true)
	author := user(t, s, "withdrawn-author", false)
	post := safetyPost(t, s, author, true)
	target, err := s.ResolveTarget(ctx, s.DB, "social", "post", post)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.TakeAction(ctx, mod, target, ActionInput{Decision: "remove", Reason: "spam"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.TakeAction(ctx, mod, target, ActionInput{Decision: "remove", Reason: "other"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	appeal, err := s.FileAppeal(ctx, author, first, "Please review")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ResolveAppeal(ctx, mod, appeal, true, "")
	if err != nil || result.LeftHiddenAuthorDeleted {
		t.Fatal("first overlapping reversal policy", result, err)
	}
	appeal, err = s.FileAppeal(ctx, author, second, "Please review second")
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.ResolveAppeal(ctx, mod, appeal, true, "")
	if err != nil || !result.LeftHiddenAuthorDeleted {
		t.Fatal("author withdrawal precedence", result, err)
	}
	var hidden bool
	_ = s.DB.QueryRow(ctx, `SELECT is_hidden FROM social_post WHERE id=$1`, post).Scan(&hidden)
	if !hidden {
		t.Fatal("overturn republished author's withdrawal")
	}
	var truthful int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND title='Your appeal succeeded' AND body LIKE '%message stays deleted%'`, author.ID).Scan(&truthful)
	if truthful != 1 {
		t.Fatal("misleading appeal outcome notice")
	}
}

func TestNativeConcernSensorsCannotAllegeVictimAndRelayMute(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	mod := user(t, s, "concern-mod", true)
	subject := user(t, s, "protected-subject", false)
	post := safetyPost(t, s, subject, false)
	var sensor, teen int64
	err := s.DB.QueryRow(ctx, `INSERT INTO safety_concernreview(kind,payload,status,handled_at,created_at,handled_by_id,post_id,subject_user_id) VALUES('sensor_pileon','{}','open',NULL,now(),NULL,$1,$2) RETURNING id`, post, subject.ID).Scan(&sensor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveConcern(ctx, mod, sensor, "escalate", ""); err == nil {
		t.Fatal("protected sensor victim targeted by report")
	}
	var count int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_report`).Scan(&count)
	if count != 0 {
		t.Fatal("sensor manufactured allegation")
	}
	if _, err = s.DB.Exec(ctx, `INSERT INTO notifications_notificationpreference(user_id,muted_kinds) VALUES($1,ARRAY['formative_note'])`, subject.ID); err != nil {
		t.Fatal(err)
	}
	err = s.DB.QueryRow(ctx, `INSERT INTO safety_concernreview(kind,payload,status,handled_at,created_at,handled_by_id,post_id,subject_user_id) VALUES('teen_concern','{}','open',NULL,now(),NULL,$1,$2) RETURNING id`, post, subject.ID).Scan(&teen)
	if err != nil {
		t.Fatal(err)
	}
	delivered, err := s.ResolveConcern(ctx, mod, teen, "send_note", "")
	if err != nil || delivered {
		t.Fatal("muted formative note delivered", err)
	}
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event='concern.note_muted'`).Scan(&count)
	if count != 1 {
		t.Fatal("muted relay dishonestly audited")
	}
	if _, err = s.ResolveConcern(ctx, mod, teen, "send_note", ""); err == nil {
		t.Fatal("handled concern replayed")
	}
}

func TestNativeBanAppealReleasesWalletAndExpiredDoesNotOverrideIndefinite(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	mod := user(t, s, "ban-mod", true)
	subject := user(t, s, "wallet-subject", false)
	holder := strings.Repeat("c", 64)
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_identitybinding(holder_hash,user_id,created_at,released_at) VALUES($1,$2,now(),NULL)`, holder, subject.ID); err != nil {
		t.Fatal(err)
	}
	target, err := s.ResolveTarget(ctx, s.DB, "accounts", "user", subject.ID)
	if err != nil {
		t.Fatal(err)
	}
	action, err := s.TakeAction(ctx, mod, target, ActionInput{Decision: "ban", Reason: "grooming"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var banned bool
	_ = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_bannedidentity WHERE holder_hash=$1)`, holder).Scan(&banned)
	if !banned {
		t.Fatal("lifetime ban did not bind wallet")
	}
	appeal, err := s.FileAppeal(ctx, subject, action, "This was mistaken.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveAppeal(ctx, mod, appeal, true, ""); err != nil {
		t.Fatal(err)
	}
	_ = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_bannedidentity WHERE holder_hash=$1)`, holder).Scan(&banned)
	if banned {
		t.Fatal("overturned lifetime ban retained wallet sanction")
	}
	if _, err = s.TakeAction(ctx, mod, target, ActionInput{Decision: "suspend", Reason: "other"}, 0); err != nil {
		t.Fatal(err)
	}
	timed, err := s.TakeAction(ctx, mod, target, ActionInput{Decision: "timed_ban", Reason: "spam", SuspendDays: 1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, `UPDATE safety_moderationaction SET expires_at=now()-interval '1 minute' WHERE id=$1`, timed); err != nil {
		t.Fatal(err)
	}
	n, err := s.LiftSuspensions(ctx)
	if err != nil || n != 0 {
		t.Fatal("expiry bypassed indefinite suspension", n, err)
	}
}

func TestRestrictedCredentialProofNoSessionAndSingleDecisionCapability(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	mod := user(t, s, "restricted-mod", true)
	subject := user(t, s, "restricted-subject", false)
	password := "correct horse battery"
	hash, err := authcore.HashPassword(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, `UPDATE accounts_user SET password=$2 WHERE id=$1`, subject.ID, hash); err != nil {
		t.Fatal(err)
	}
	target, err := s.ResolveTarget(ctx, s.DB, "accounts", "user", subject.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TakeAction(ctx, mod, target, ActionInput{Decision: "suspend", Reason: "spam", SuspendDays: 1}, 0); err != nil {
		t.Fatal(err)
	}
	statement, token, err := s.RestrictionAccess(ctx, subject.Username, password, "generated-client")
	if err != nil || token == "" || statement["reason_label"] != "Spam" {
		t.Fatal("restricted statement proof failed", err)
	}
	if _, _, err = s.RestrictionAccess(ctx, subject.Username, "incorrect password", "generated-client"); err == nil {
		t.Fatal("wrong credentials disclosed restriction")
	}
	var sessions int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_go_session WHERE user_id=$1`, subject.ID).Scan(&sessions)
	if sessions != 0 {
		t.Fatal("restriction proof granted participation session")
	}
	var userID, actionID int64
	err = s.DB.QueryRow(ctx, `DELETE FROM safety_go_restrictioncapability WHERE token_hash=$1 RETURNING user_id,action_id`, capHash(token)).Scan(&userID, &actionID)
	if err != nil || userID != subject.ID {
		t.Fatal("capability bound wrong account", err)
	}
	if _, err = s.FileAppeal(ctx, platform.Actor{ID: userID}, actionID, "Please reconsider."); err != nil {
		t.Fatal(err)
	}
}

func TestNativeUnsafeIdempotencyAndBlockedGuardianTruth(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	child := user(t, s, "generated-child", false)
	child.Cohort = "child"
	child.AgeBand = "under_16"
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET cohort='child',age_band='under_16' WHERE id=$1`, child.ID); err != nil {
		t.Fatal(err)
	}
	guardian := user(t, s, "helpful-guardian", false)
	blocked := user(t, s, "blocked-guardian", false)
	// Another child organises; the reporter holds an ordinary member seat.
	organiser := user(t, s, "generated-child-organiser", false)
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET cohort='child',age_band='under_16' WHERE id=$1`, organiser.ID); err != nil {
		t.Fatal(err)
	}
	var activity int64
	err := s.DB.QueryRow(ctx, `INSERT INTO social_activity(title,description,starts_at,ends_at,cohort,join_threshold,owner_can_override,capacity,status,created_at,updated_at,activity_type_id,owner_id,place_id,guardian_accompanied,is_hidden,meeting_point,organizer_note,what_to_bring,accessibility_notes,beginners_welcome,cost_band,difficulty,go_confirmed_at,min_to_go,series_id,supervised,first_time_note,is_publicly_listed,cost_amount,cost_note) VALUES('Generated child meetup','',now()+interval '1 day',NULL,'child',0.666666,false,NULL,'open',now(),now(),1,$1,1,false,false,'','','','',false,'unspecified','unspecified',NULL,NULL,NULL,false,'',false,NULL,'') RETURNING id`, organiser.ID).Scan(&activity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,'member','member','unknown','none',false,now(),now(),now())`, activity, child.ID); err != nil {
		t.Fatal(err)
	}
	for _, g := range []platform.Actor{guardian, blocked} {
		if _, err = s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, g.ID, child.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, child.ID, blocked.ID); err != nil {
		t.Fatal(err)
	}
	// An organiser who blocks the child first cannot pre-empt the safe exit.
	if _, err = s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, organiser.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	result, err := s.UnsafeReport(ctx, child, activity)
	if err != nil || result.GuardiansAlerted != 1 || result.Repeat {
		t.Fatal("unsafe guardian truth mismatch", result, err)
	}
	again, err := s.UnsafeReport(ctx, child, activity)
	if err != nil || !again.Repeat || again.GuardiansAlerted != 0 || again.ReportID != result.ReportID {
		t.Fatal("unsafe repeat stormed alerts", again, err)
	}
	var notices int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1`, blocked.ID).Scan(&notices)
	if notices != 0 {
		t.Fatal("blocked guardian notified")
	}
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND kind='system'`, guardian.ID).Scan(&notices); err != nil || notices != 1 {
		t.Fatal("active guardian system alert", notices, err)
	}
	var detail, reason, app, model string
	var target int64
	_ = s.DB.QueryRow(ctx, `SELECT r.detail,r.reason,c.app_label,c.model,r.target_id FROM safety_report r JOIN django_content_type c ON c.id=r.target_type_id WHERE r.id=$1`, result.ReportID).Scan(&detail, &reason, &app, &model, &target)
	if detail != UnsafeSentinel {
		t.Fatal("panic path introduced child free text")
	}
	if reason != "off_platform" || app != "social" || model != "activity" || target != activity {
		t.Fatal("unsafe report target/reason", reason, app, model, target)
	}
}

func TestNativeActivityLogAllowlistNeverRawPayload(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	a := user(t, s, "history-subject", false)
	other := user(t, s, "history-peer", false)
	for _, event := range []string{"group.joined", "private.new_unmapped"} {
		err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
			return platform.RecordAudit(ctx, tx, a, event, "private.target:99", map[string]string{"private": "must not be shown"})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error { return platform.RecordAudit(ctx, tx, other, "group.left", "", nil) })
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ActivityLog(ctx, a.ID)
	if err != nil || len(rows) != 1 {
		t.Fatal("activity log scope", rows, err)
	}
	if len(rows[0]) != 2 || rows[0]["label"] != "You joined a group" || rows[0]["when"] == nil {
		t.Fatal("activity log DTO widened", rows)
	}
}
