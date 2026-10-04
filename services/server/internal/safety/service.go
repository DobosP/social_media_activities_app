package safety

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strconv"
	"time"
)

type Visibility func(context.Context, platform.Querier, platform.Actor, int64) (bool, error)
type Config struct {
	Accounts       *accounts.Service
	CanSeeActivity Visibility
	CanReadThread  Visibility
	Now            func() time.Time
}
type Service struct {
	DB           *pgxpool.Pool
	Config       Config
	RatePolicies map[string]budgets.Policy
}

func New(db *pgxpool.Pool, config Config) *Service {
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{DB: db, Config: config}
}

type Target struct {
	App, Model                string
	ID, ContentType, Affected int64
	Label, Body, Cohort       string
	ThreadID                  int64
}

var ErrInvalid = platform.ErrInvalid
var ErrConflict = errors.New("already decided or contested")
var ErrRate = errors.New("too many safety actions")
var reasons = map[string]string{"grooming": "Grooming / predatory contact", "harassment": "Harassment / bullying", "csam": "Child sexual abuse material", "spam": "Spam", "off_platform": "Unsafe off-platform / meetup risk", "other": "Other"}
var actions = map[string]string{"warn": "Warn", "remove": "Remove content", "suspend": "Suspend account", "timed_ban": "Timed ban", "ban": "Ban account"}
var authorities = map[string]string{"inhope": "INHOPE / national hotline", "igpr": "Romanian Police (IGPR)", "police": "Other law enforcement", "other": "Other authority"}
var severity = map[string]int{"csam": 5, "grooming": 5, "off_platform": 3, "harassment": 2, "spam": 1, "other": 0}

func object(ctx context.Context, q platform.Querier, sql string, args ...any) (json.RawMessage, error) {
	var raw []byte
	err := q.QueryRow(ctx, sql, args...).Scan(&raw)
	return json.RawMessage(raw), err
}
func objects(ctx context.Context, q platform.Querier, sql string, args ...any) ([]json.RawMessage, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, rows.Err()
}

func (s *Service) Migrate(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `CREATE TABLE IF NOT EXISTS safety_go_actionbudget(user_id bigint NOT NULL REFERENCES accounts_user(id) ON DELETE CASCADE,action text NOT NULL,count integer NOT NULL,until timestamptz NOT NULL,PRIMARY KEY(user_id,action));CREATE INDEX IF NOT EXISTS safety_go_actionexpiry ON safety_go_actionbudget(until)`)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, restrictedSchema)
	return err
}
func (s *Service) allow(ctx context.Context, a platform.Actor, action string, limit int, window time.Duration) (bool, error) {
	policy, err := budgets.Resolve(s.RatePolicies, action, budgets.Policy{Limit: limit, Window: window})
	if err != nil {
		return false, err
	}
	now := s.Config.Now()
	var allowed bool
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM safety_go_actionbudget WHERE until<=$1`, now); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `INSERT INTO safety_go_actionbudget(user_id,action,count,until) VALUES($1,$2,1,$3) ON CONFLICT(user_id,action) DO UPDATE SET count=safety_go_actionbudget.count+1 WHERE safety_go_actionbudget.count<$4 RETURNING true`, a.ID, action, now.Add(policy.Window), policy.Limit).Scan(&allowed)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return allowed && err == nil, err
}

func (s *Service) ResolveTarget(ctx context.Context, q platform.Querier, app, model string, id int64) (Target, error) {
	t := Target{App: app, Model: model, ID: id}
	if id < 1 {
		return t, platform.ErrInvalid
	}
	err := q.QueryRow(ctx, `SELECT id FROM django_content_type WHERE app_label=$1 AND model=$2`, app, model).Scan(&t.ContentType)
	if err != nil {
		return t, err
	}
	var sql string
	switch app + "." + model {
	case "accounts.user":
		sql = `SELECT id,COALESCE(NULLIF(display_name,''),username),'',cohort,0 FROM accounts_user WHERE id=$1`
	case "social.activity":
		sql = `SELECT a.owner_id,a.title,'',u.cohort,0 FROM social_activity a JOIN accounts_user u ON u.id=a.owner_id WHERE a.id=$1`
	case "social.group":
		sql = `SELECT g.owner_id,g.title,'',u.cohort,0 FROM social_group g JOIN accounts_user u ON u.id=g.owner_id WHERE g.id=$1`
	case "social.post":
		sql = `SELECT p.author_id,'post('||COALESCE(NULLIF(u.display_name,''),u.username)||' @ '||p.thread_id::text||')',p.body,u.cohort,p.thread_id FROM social_post p JOIN accounts_user u ON u.id=p.author_id WHERE p.id=$1`
	case "messaging.message":
		sql = `SELECT COALESCE(m.sender_id,0),'Message<'||m.id::text||'>','',COALESCE(u.cohort,''),0 FROM messaging_message m LEFT JOIN accounts_user u ON u.id=m.sender_id WHERE m.id=$1`
	case "booking.booking":
		sql = `SELECT b.user_id,'Booking<'||b.id::text||'>','',u.cohort,0 FROM booking_booking b JOIN accounts_user u ON u.id=b.user_id WHERE b.id=$1`
	case "social.membership":
		sql = `SELECT m.user_id,'Membership<'||m.id::text||'>','',u.cohort,0 FROM social_membership m JOIN accounts_user u ON u.id=m.user_id WHERE m.id=$1`
	default:
		return t, platform.ErrNotFound
	}
	err = q.QueryRow(ctx, sql, id).Scan(&t.Affected, &t.Label, &t.Body, &t.Cohort, &t.ThreadID)
	return t, err
}

func (s *Service) reportTarget(ctx context.Context, a platform.Actor, model string, id int64) (Target, error) {
	app := "social"
	if model == "user" {
		app = "accounts"
	}
	if model != "user" && model != "activity" && model != "post" {
		return Target{}, platform.ErrInvalid
	}
	t, err := s.ResolveTarget(ctx, s.DB, app, model, id)
	if err != nil {
		return t, err
	}
	if a.IsStaff {
		return t, nil
	}
	switch model {
	case "activity":
		if s.Config.CanSeeActivity == nil {
			return t, platform.ErrNotFound
		}
		yes, err := s.Config.CanSeeActivity(ctx, s.DB, a, id)
		if err != nil {
			return t, err
		}
		if !yes {
			return t, platform.ErrNotFound
		}
	case "post":
		if s.Config.CanReadThread == nil {
			return t, platform.ErrNotFound
		}
		yes, err := s.Config.CanReadThread(ctx, s.DB, a, t.ThreadID)
		if err != nil {
			return t, err
		}
		if !yes {
			return t, platform.ErrNotFound
		}
	}
	return t, nil
}

// Delivery is best-effort and savepoint-isolated. A notification failure must
// never roll back the underlying safety decision or expose its private content.
func notify(ctx context.Context, tx pgx.Tx, user int64, kind, title, body, url string) bool {
	if user < 1 {
		return false
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT safety_notify`); err != nil {
		return false
	}
	yes, err := platform.Notify(ctx, tx, user, kind, title, body, url)
	if err != nil {
		_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT safety_notify`)
		_, _ = tx.Exec(ctx, `RELEASE SAVEPOINT safety_notify`)
		return false
	}
	_, _ = tx.Exec(ctx, `RELEASE SAVEPOINT safety_notify`)
	return yes
}
func guardians(ctx context.Context, tx pgx.Tx, minors ...int64) error {
	if len(minors) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT g.guardian_id FROM accounts_guardianrelationship g JOIN accounts_user w ON w.id=g.ward_id WHERE g.ward_id=ANY($1) AND g.status='active' AND w.cohort='child' AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=g.guardian_id AND b.blocked_id=g.ward_id) OR (b.blocker_id=g.ward_id AND b.blocked_id=g.guardian_id)) ORDER BY g.guardian_id`, minors)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		notify(ctx, tx, id, "system", "A moderation decision concerning a child you look after", "A moderation decision was made about something concerning a child you look after. You can see their upcoming meetups on your guardian page.", "/wards/")
	}
	return nil
}

func (s *Service) FileReport(ctx context.Context, a platform.Actor, target Target, reason, detail string) (int64, error) {
	if reasons[reason] == "" {
		return 0, platform.ErrInvalid
	}
	var id int64
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var err error
		id, err = s.fileReport(ctx, tx, a, target, reason, detail)
		return err
	})
	return id, err
}
func (s *Service) fileReport(ctx context.Context, tx pgx.Tx, a platform.Actor, target Target, reason, detail string) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO safety_report(target_type_id,target_id,reason,detail,status,handled_at,resolution,created_at,handled_by_id,reporter_id) VALUES($1,$2,$3,$4,'open',NULL,'',now(),NULL,$5) RETURNING id`, target.ContentType, target.ID, reason, detail, a.ID).Scan(&id)
	if err != nil {
		return 0, err
	}
	if err = platform.RecordAudit(ctx, tx, a, "report.filed", target.App+"."+target.Model+":"+strconv.FormatInt(target.ID, 10), map[string]string{"reason": reason}); err != nil {
		return 0, err
	}
	notify(ctx, tx, a.ID, "system", "We received your report", "Thanks - your report was sent to the moderation team. We'll let you know once it's been reviewed.", "")
	return id, nil
}

type ActionInput struct {
	Decision    string `json:"decision"`
	Reason      string `json:"reason"`
	Notes       string `json:"notes"`
	SuspendDays int    `json:"suspend_days"`
}

func (s *Service) TakeAction(ctx context.Context, a platform.Actor, target Target, input ActionInput, reportID int64) (int64, error) {
	if !a.Moderator() {
		return 0, platform.ErrForbidden
	}
	if actions[input.Decision] == "" || reasons[input.Reason] == "" || input.SuspendDays < 0 || input.SuspendDays > 36500 || input.Decision == "timed_ban" && input.SuspendDays < 1 {
		return 0, platform.ErrInvalid
	}
	var id int64
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var reportUser int64
		if reportID > 0 {
			if err := tx.QueryRow(ctx, `SELECT COALESCE(reporter_id,0) FROM safety_report WHERE id=$1 FOR UPDATE`, reportID).Scan(&reportUser); err != nil {
				return err
			}
		}
		var expires *time.Time
		if (input.Decision == "suspend" || input.Decision == "timed_ban") && input.SuspendDays > 0 {
			value := s.Config.Now().Add(time.Duration(input.SuspendDays) * 24 * time.Hour)
			expires = &value
		}
		var report any
		if reportID > 0 {
			report = reportID
		}
		if err := tx.QueryRow(ctx, `INSERT INTO safety_moderationaction(target_id,action,reason,notes,expires_at,created_at,moderator_id,target_type_id,report_id,lifted_at) VALUES($1,$2,$3,$4,$5,now(),$6,$7,$8,NULL) RETURNING id`, target.ID, input.Decision, input.Reason, input.Notes, expires, a.ID, target.ContentType, report).Scan(&id); err != nil {
			return err
		}
		if target.App == "accounts" && target.Model == "user" && (input.Decision == "suspend" || input.Decision == "timed_ban" || input.Decision == "ban") {
			if _, err := tx.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, target.ID); err != nil {
				return err
			}
			if input.Decision == "ban" {
				var bannedID int64
				err := tx.QueryRow(ctx, `INSERT INTO accounts_bannedidentity(holder_hash,created_at) SELECT holder_hash,now() FROM accounts_identitybinding WHERE user_id=$1 ON CONFLICT(holder_hash) DO NOTHING RETURNING id`, target.ID).Scan(&bannedID)
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				if err == nil {
					if err = platform.RecordAudit(ctx, tx, a, "identity.banned", "accounts.bannedidentity:"+strconv.FormatInt(bannedID, 10), nil); err != nil {
						return err
					}
				}
			}
		}
		if input.Decision == "remove" && (target.App == "social" && (target.Model == "activity" || target.Model == "post" || target.Model == "group")) {
			if _, err := tx.Exec(ctx, "UPDATE "+pgx.Identifier{"social_" + target.Model}.Sanitize()+" SET is_hidden=true WHERE id=$1", target.ID); err != nil {
				return err
			}
		}
		if reportID > 0 {
			if _, err := tx.Exec(ctx, `UPDATE safety_report SET status='actioned',handled_by_id=$2,handled_at=now() WHERE id=$1`, reportID, a.ID); err != nil {
				return err
			}
			notify(ctx, tx, reportUser, "system", "Your report was reviewed", "Thanks for your report. Our moderation team reviewed it and took action.", "")
		}
		if err := platform.RecordAudit(ctx, tx, a, "moderation.action", target.App+"."+target.Model+":"+strconv.FormatInt(target.ID, 10), map[string]string{"action": input.Decision, "reason": input.Reason}); err != nil {
			return err
		}
		notify(ctx, tx, target.Affected, "moderation", "A moderation decision affected your content/account", fmt.Sprintf("Action taken: %s. Reason: %s. If you believe this decision is wrong, you may contest it.", actions[input.Decision], reasons[input.Reason]), "")
		return guardians(ctx, tx, target.Affected, reportUser)
	})
	return id, err
}
