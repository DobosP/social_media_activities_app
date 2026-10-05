// Package social implements the social domain over the existing PostgreSQL
// schema. Every mutation goes through a domain transaction, never generic CRUD.
package social

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Actor = platform.Actor
type AuditFunc func(context.Context, pgx.Tx, Actor, string, string, any) error
type NotifyFunc func(context.Context, pgx.Tx, int64, string, string, string, string) (bool, error)

type Service struct {
	DB     *pgxpool.Pool
	Audit  AuditFunc
	Notify NotifyFunc
	// Safety-dependent adapters are required before their surfaces can run.
	// The avatar adapter must preserve the reviewed signature-avatar renderer.
	Avatar            func(context.Context, platform.Querier, int64) (string, error)
	ActivityVisual    func(context.Context, platform.Querier, Actor, int64) (any, error)
	ActivityVisuals   func(context.Context, platform.Querier, Actor, []int64) (map[int64]any, error)
	AfterActivitySave func(context.Context, pgx.Tx, int64) error
	// A deployment can supply its native moderation/encryption policy here.
	// Never dispatch a Python policy from the serving process.
	MessagePolicy          func(context.Context, platform.Querier, Actor, int64, string) (string, error)
	BodyMarkup             func(string, map[string]bool, bool) string
	AllowUserGroups        bool
	MinorOnboardingEnabled bool
	Cursor                 platform.CursorCodec
	ConnectionCohorts      map[string]bool
	Sentiment              SentimentConfig
	CommunityPolicy        CommunityConfig
	Budgets                *budgets.Store
	RatePolicies           map[string]budgets.Policy
	Policy                 PolicyConfig
	Now                    func() time.Time
}

func New(db *pgxpool.Pool, audit AuditFunc) *Service {
	return &Service{DB: db, Audit: audit, Notify: platform.Notify, Budgets: budgets.New(db), Now: time.Now, ConnectionCohorts: map[string]bool{"adult": true, "teen": true, "child": true}, Sentiment: DefaultSentimentConfig(), CommunityPolicy: DefaultCommunityConfig(), Policy: DefaultPolicyConfig()}
}

func (s *Service) admission(ctx context.Context, actor int64, action string, limit int, window time.Duration) (budgets.Decision, error) {
	policy, err := budgets.Resolve(s.RatePolicies, action, budgets.Policy{Limit: limit, Window: window})
	if err != nil {
		return budgets.Decision{}, err
	}
	return s.Budgets.Actor(ctx, actor, "social."+action, policy)
}
func (s *Service) allow(ctx context.Context, actor int64, action string, limit int, window time.Duration) bool {
	result, err := s.admission(ctx, actor, action, limit, window)
	return err == nil && result.Allowed
}
func (s *Service) rateTransaction(ctx context.Context, a Actor, action string, limit int, window time.Duration, f func(pgx.Tx, func() error) error) error {
	err := budgets.Reserve(func(reserve func() error) error {
		return s.transaction(ctx, a, func(tx pgx.Tx) error { return f(tx, reserve) })
	}, func() (budgets.Decision, error) { return s.admission(ctx, a.ID, action, limit, window) })
	if errors.Is(err, budgets.ErrDenied) {
		return platform.ErrForbidden
	}
	return err
}

func (s *Service) transaction(ctx context.Context, a Actor, f func(pgx.Tx) error) error {
	if s.DB == nil || s.Audit == nil {
		return errors.New("social service is not configured")
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if err := platform.Participate(ctx, tx, a); err != nil {
			return err
		}
		return f(tx)
	})
}

// Privacy withdrawal must still work after assurance or consent has lapsed.
// Domain callbacks retain self/owner gates and cannot use this for admission.
func (s *Service) privacyTransaction(ctx context.Context, a Actor, f func(pgx.Tx) error) error {
	if s.DB == nil || s.Audit == nil {
		return errors.New("social service is not configured")
	}
	if a.ID < 1 || !a.IsActive {
		return platform.ErrForbidden
	}
	return platform.Transaction(ctx, s.DB, f)
}
func (s *Service) audit(ctx context.Context, tx pgx.Tx, a Actor, event, model string, id int64, data any) error {
	if model == "activity" && s.AfterActivitySave != nil {
		if err := s.AfterActivitySave(ctx, tx, id); err != nil {
			return err
		}
	}
	return s.Audit(ctx, tx, a, event, "social."+model+":"+strconv.FormatInt(id, 10), data)
}
func (s *Service) notify(ctx context.Context, tx pgx.Tx, recipient int64, kind, title, body, url string) error {
	if s.Notify == nil {
		return errors.New("notification safety adapter unavailable")
	}
	_, err := s.Notify(ctx, tx, recipient, kind, title, body, url)
	return err
}

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
	out := make([]json.RawMessage, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, rows.Err()
}
func scalar(ctx context.Context, q platform.Querier, sql string, args ...any) (bool, error) {
	var yes bool
	err := q.QueryRow(ctx, sql, args...).Scan(&yes)
	return yes, err
}
func idFrom(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, platform.ErrNotFound
	}
	return id, nil
}
func intID(raw json.RawMessage) (int64, error) {
	var id int64
	if json.Unmarshal(raw, &id) != nil || id <= 0 {
		return 0, platform.ErrInvalid
	}
	return id, nil
}
func text(raw json.RawMessage, maxLen int, required bool) (string, error) {
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return "", platform.ErrInvalid
	}
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > maxLen || (required && v == "") {
		return "", platform.ErrInvalid
	}
	return v, nil
}
func boolValue(raw json.RawMessage) (bool, error) {
	var v bool
	if string(raw) != "true" && string(raw) != "false" {
		return false, platform.ErrInvalid
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}
func nullableInt(raw json.RawMessage) (*int, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	var n int
	if json.Unmarshal(raw, &n) != nil || n < 1 {
		return nil, platform.ErrInvalid
	}
	return &n, nil
}
func dateValue(raw json.RawMessage) (time.Time, error) {
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return time.Time{}, platform.ErrInvalid
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return t, platform.ErrInvalid
	}
	return t, nil
}
func optionalDate(raw json.RawMessage) (*time.Time, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	t, err := dateValue(raw)
	return &t, err
}
func errorIfFalse(ok bool, err error) error {
	if err != nil {
		return err
	}
	if !ok {
		return platform.ErrForbidden
	}
	return nil
}
func assigned(a Actor) bool { return a.IsActive && a.Cohort != "" && a.Cohort != "unassigned" }

type ActivityInput struct {
	OnBehalfOf          string     `json:"on_behalf_of"`
	Place               int64      `json:"place"`
	ActivityType        int64      `json:"activity_type"`
	Title               string     `json:"title"`
	Description         string     `json:"description"`
	StartsAt            time.Time  `json:"starts_at"`
	EndsAt              *time.Time `json:"ends_at"`
	JoinThreshold       *float64   `json:"join_threshold"`
	Capacity            *int       `json:"capacity"`
	MinToGo             *int       `json:"min_to_go"`
	GuardianAccompanied bool       `json:"guardian_accompanied"`
	Supervised          bool       `json:"supervised"`
	MeetingPoint        string     `json:"meeting_point"`
	WhatToBring         string     `json:"what_to_bring"`
	OrganizerNote       string     `json:"organizer_note"`
	FirstTimeNote       string     `json:"first_time_note"`
	CostBand            string     `json:"cost_band"`
	CostAmount          *Decimal   `json:"cost_amount"`
	CostNote            string     `json:"cost_note"`
	SecondaryTypes      []int64    `json:"secondary_types"`
	Difficulty          string     `json:"difficulty"`
	AccessibilityNotes  string     `json:"accessibility_notes"`
	BeginnersWelcome    bool       `json:"beginners_welcome"`
}

func (v *ActivityInput) validate() error {
	v.Title = strings.TrimSpace(v.Title)
	v.Description = strings.TrimSpace(v.Description)
	if v.Title == "" || utf8.RuneCountInString(v.Title) > 200 || utf8.RuneCountInString(v.Description) > 2000 || v.Place < 1 || v.ActivityType < 1 || v.StartsAt.IsZero() {
		return platform.ErrInvalid
	}
	for _, t := range []string{v.MeetingPoint, v.WhatToBring, v.OrganizerNote, v.FirstTimeNote, v.AccessibilityNotes} {
		if utf8.RuneCountInString(t) > 500 {
			return platform.ErrInvalid
		}
	}
	if v.CostBand == "" {
		v.CostBand = "unspecified"
	}
	if v.CostAmount != nil && (v.CostBand != "low" && v.CostBand != "paid" || !validDecimal(string(*v.CostAmount))) {
		return platform.ErrInvalid
	}
	if utf8.RuneCountInString(v.CostNote) > 120 {
		return platform.ErrInvalid
	}
	if v.Difficulty == "" {
		v.Difficulty = "unspecified"
	}
	if !strings.Contains("|unspecified|free|low|paid|", "|"+v.CostBand+"|") || !strings.Contains("|unspecified|easy|moderate|challenging|", "|"+v.Difficulty+"|") {
		return platform.ErrInvalid
	}
	if v.Capacity != nil && *v.Capacity < 1 || v.MinToGo != nil && *v.MinToGo < 1 || v.Capacity != nil && v.MinToGo != nil && *v.MinToGo > *v.Capacity {
		return platform.ErrInvalid
	}
	if v.EndsAt != nil && v.EndsAt.Before(v.StartsAt) {
		return platform.ErrInvalid
	}
	if v.JoinThreshold != nil && (math.IsNaN(*v.JoinThreshold) || math.IsInf(*v.JoinThreshold, 0) || *v.JoinThreshold < 0.01 || *v.JoinThreshold > 1) {
		return platform.ErrInvalid
	}
	return nil
}

func response(w http.ResponseWriter, value any, err error, status int) {
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, status, value)
}

const blockOwner = `NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=a.owner_id) OR (b.blocker_id=a.owner_id AND b.blocked_id=$1))`
const activityColumns = `jsonb_build_object('id',a.id,'title',a.title,'description',a.description,'meeting_point',a.meeting_point,'what_to_bring',a.what_to_bring,'organizer_note',a.organizer_note,'cost_band',a.cost_band,'difficulty',a.difficulty,'accessibility_notes',a.accessibility_notes,'beginners_welcome',a.beginners_welcome,'owner',u.display_name,'place',a.place_id,'activity_type',t.slug,'cohort',a.cohort,'starts_at',a.starts_at,'ends_at',a.ends_at,'join_threshold',a.join_threshold,'owner_can_override',a.owner_can_override,'capacity',a.capacity,'min_to_go',a.min_to_go,'status',a.status,'guardian_accompanied',a.guardian_accompanied,'supervised',a.supervised,'is_publicly_listed',a.is_publicly_listed,'open_positions',CASE WHEN a.capacity IS NULL THEN NULL ELSE GREATEST(0,a.capacity-(SELECT COUNT(*)::int FROM social_membership m WHERE m.activity_id=a.id AND m.state='member' AND m.role<>'guardian')) END,'created_at',a.created_at)`
const activityJoin = ` FROM social_activity a JOIN accounts_user u ON u.id=a.owner_id JOIN taxonomy_activitytype t ON t.id=a.activity_type_id `

func ActivityProjectionSQL() string { return activityColumns }
func ActivityVisibilitySQL() string { return `a.cohort=$2 AND NOT a.is_hidden AND ` + blockOwner }

const membershipColumns = `jsonb_build_object('id',m.id,'activity',m.activity_id,'user',u.display_name,'role',m.role,'state',m.state,'attendance_intent',m.attendance_intent,'arrived_at',m.arrived_at,'transit_status',m.transit_status,'departing_at',m.departing_at,'created_at',m.created_at,'decided_at',m.decided_at)`

// Membership rows (with live presence) are co-member scoped, not cohort-wide:
// the row's own user, the activity owner, or a current non-guardian member
// (which includes co-organizers). Owner decision 2026-10-05.
const membershipAudience = `(m.user_id=$1 OR a.owner_id=$1 OR EXISTS(SELECT 1 FROM social_membership cm WHERE cm.activity_id=m.activity_id AND cm.user_id=$1 AND cm.state='member' AND cm.role<>'guardian'))`

func (s *Service) Activity(ctx context.Context, a Actor, id int64) (json.RawMessage, error) {
	if !assigned(a) {
		return nil, platform.ErrNotFound
	}
	return object(ctx, s.DB, `SELECT `+activityColumns+activityJoin+` WHERE a.id=$3 AND a.cohort=$2 AND NOT a.is_hidden AND `+blockOwner, a.ID, a.Cohort, id)
}
func (s *Service) Membership(ctx context.Context, a Actor, id int64) (json.RawMessage, error) {
	if !assigned(a) {
		return nil, platform.ErrNotFound
	}
	return object(ctx, s.DB, `SELECT `+membershipColumns+` FROM social_membership m JOIN accounts_user u ON u.id=m.user_id JOIN social_activity a ON a.id=m.activity_id WHERE m.id=$3 AND a.cohort=$2 AND NOT a.is_hidden AND `+blockOwner+` AND `+membershipAudience, a.ID, a.Cohort, id)
}
func (s *Service) listActivities(ctx context.Context, a Actor, query string, limit, offset int) ([]json.RawMessage, error) {
	if !assigned(a) {
		return []json.RawMessage{}, nil
	}
	return objects(ctx, s.DB, matchingTypes+`SELECT `+activityColumns+activityJoin+` JOIN places_place p ON p.id=a.place_id WHERE a.cohort=$2 AND NOT a.is_hidden AND `+blockOwner+` AND `+activitySearch+` ORDER BY a.id LIMIT $4 OFFSET $5`, a.ID, a.Cohort, escapeLike(query), limit, offset)
}

type activityState struct {
	ID, OwnerID, PlaceID, TypeID, ThreadID    int64
	Cohort, Title, Status                     string
	StartsAt                                  time.Time
	EndsAt                                    *time.Time
	Capacity, MinToGo                         *int
	Threshold                                 float64
	Override, Supervised, GuardianAccompanied bool
}

func activity(ctx context.Context, q platform.Querier, a Actor, id int64, lock bool) (activityState, error) {
	var v activityState
	if !assigned(a) {
		return v, platform.ErrNotFound
	}
	sql := `SELECT a.id,a.owner_id,a.place_id,a.activity_type_id,COALESCE(th.id,0),a.cohort,a.title,a.status,a.starts_at,a.ends_at,a.capacity,a.min_to_go,a.join_threshold,a.owner_can_override,a.supervised,a.guardian_accompanied FROM social_activity a LEFT JOIN social_thread th ON th.activity_id=a.id WHERE a.id=$3 AND a.cohort=$2 AND NOT a.is_hidden AND ` + blockOwner
	if lock {
		sql += ` FOR UPDATE OF a`
	}
	err := q.QueryRow(ctx, sql, a.ID, a.Cohort, id).Scan(&v.ID, &v.OwnerID, &v.PlaceID, &v.TypeID, &v.ThreadID, &v.Cohort, &v.Title, &v.Status, &v.StartsAt, &v.EndsAt, &v.Capacity, &v.MinToGo, &v.Threshold, &v.Override, &v.Supervised, &v.GuardianAccompanied)
	return v, err
}
func organizer(ctx context.Context, q platform.Querier, a Actor, v activityState) (bool, error) {
	if a.ID == v.OwnerID {
		return true, nil
	}
	return scalar(ctx, q, `SELECT EXISTS(SELECT 1 FROM social_membership WHERE activity_id=$1 AND user_id=$2 AND state='member' AND role='co_organizer')`, v.ID, a.ID)
}
func participantCount(ctx context.Context, q platform.Querier, id int64) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM social_membership WHERE activity_id=$1 AND state='member' AND role<>'guardian'`, id).Scan(&n)
	return n, err
}
func (s *Service) fanout(ctx context.Context, tx pgx.Tx, a Actor, v activityState, kind, title, body string) error {
	rows, err := tx.Query(ctx, `SELECT user_id FROM social_membership m WHERE activity_id=$1 AND state='member' AND user_id<>$2 AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$2 AND b.blocked_id=m.user_id) OR (b.blocker_id=m.user_id AND b.blocked_id=$2)) ORDER BY user_id`, v.ID, a.ID)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
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
		if err := s.notify(ctx, tx, id, kind, title, body, fmt.Sprintf("/api/social/activities/%d/", v.ID)); err != nil {
			return err
		}
	}
	return nil
}
