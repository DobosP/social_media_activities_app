package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB                 *pgxpool.Pool
	Cursor             platform.CursorCodec
	Now                func() time.Time
	MaxGroupMembers    int
	MaxCiphertextBytes int
	ConversationLimit  int
	MessagePageLimit   int
	RetentionDays      int
	RatePolicies       map[string]budgets.Policy
}

func New(db *pgxpool.Pool, cursor platform.CursorCodec) *Service {
	return &Service{DB: db, Cursor: cursor, Now: time.Now, MaxGroupMembers: 256, MaxCiphertextBytes: 65536, ConversationLimit: 100, MessagePageLimit: 50}
}
func (s *Service) maxMembers() int {
	if s.MaxGroupMembers < 2 || s.MaxGroupMembers > 256 {
		return 256
	}
	return s.MaxGroupMembers
}
func actor(ctx context.Context, q platform.Querier, id int64) (platform.Actor, error) {
	var a platform.Actor
	err := q.QueryRow(ctx, `SELECT id,public_id::text,username,display_name,age_band,cohort,role,is_identity_verified,is_active,is_staff,is_superuser FROM accounts_user WHERE id=$1`, id).Scan(&a.ID, &a.PublicID, &a.Username, &a.DisplayName, &a.AgeBand, &a.Cohort, &a.Role, &a.IdentityVerified, &a.IsActive, &a.IsStaff, &a.IsSuperuser)
	return a, err
}
func target(ctx context.Context, q platform.Querier, username string) (platform.Actor, error) {
	var id int64
	if err := q.QueryRow(ctx, `SELECT id FROM accounts_user WHERE username=$1`, username).Scan(&id); err != nil {
		return platform.Actor{}, err
	}
	return actor(ctx, q, id)
}
func pair(ctx context.Context, q platform.Querier, a, b platform.Actor) error {
	if a.ID == b.ID || !a.IsActive || !b.IsActive || a.Cohort == "unassigned" || a.Cohort == "" || a.Cohort != b.Cohort {
		return platform.ErrForbidden
	}
	if err := platform.Participate(ctx, q, a); err != nil {
		return err
	}
	if err := platform.Participate(ctx, q, b); err != nil {
		return err
	}
	blocked, err := platform.Blocked(ctx, q, a.ID, b.ID)
	if err != nil {
		return err
	}
	if blocked {
		return platform.ErrForbidden
	}
	return nil
}
func (s *Service) CanView(ctx context.Context, q platform.Querier, a platform.Actor, conversation int64) (bool, error) {
	fresh, err := actor(ctx, q, a.ID)
	if err != nil {
		return false, err
	}
	if !fresh.IsActive {
		return false, nil
	}
	a = fresh
	var state, role, cohort string
	err = q.QueryRow(ctx, `SELECT p.state,p.role,c.cohort FROM messaging_participant p JOIN messaging_conversation c ON c.id=p.conversation_id WHERE p.user_id=$1 AND c.id=$2`, a.ID, conversation).Scan(&state, &role, &cohort)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state != "active" {
		return false, nil
	}
	if role == "guardian" {
		return s.guardianEligible(ctx, q, a, conversation)
	}
	if a.Cohort != cohort {
		return false, nil
	}
	err = platform.Participate(ctx, q, a)
	if errors.Is(err, platform.ErrForbidden) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var blocked bool
	err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messaging_participant p JOIN safety_block b ON (b.blocker_id=$2 AND b.blocked_id=p.user_id) OR (b.blocker_id=p.user_id AND b.blocked_id=$2) WHERE p.conversation_id=$1 AND p.state='active' AND p.role!='guardian')`, conversation, a.ID).Scan(&blocked)
	return !blocked, err
}
func (s *Service) canWrite(ctx context.Context, q platform.Querier, a platform.Actor, id int64) error {
	ok, err := s.CanView(ctx, q, a, id)
	if err != nil {
		return err
	}
	if !ok {
		return platform.ErrForbidden
	}
	var role string
	if err = q.QueryRow(ctx, `SELECT role FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, id, a.ID).Scan(&role); err != nil {
		return err
	}
	if role == "guardian" {
		return platform.ErrForbidden
	}
	return nil
}
func userRef(ctx context.Context, q platform.Querier, id int64) (any, error) {
	if id == 0 {
		return nil, nil
	}
	a, err := actor(ctx, q, id)
	if err != nil {
		return nil, err
	}
	avatar, err := accounts.Avatar(ctx, q, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"public_id": a.PublicID, "username": a.Username, "display_name": a.DisplayName, "avatar": avatar}, nil
}
func keyFingerprint(jwk any) (string, error) {
	raw, err := platform.CanonicalJSON(jwk)
	if err != nil {
		return "", err
	}
	var canonical any
	if err = json.Unmarshal(raw, &canonical); err != nil {
		return "", err
	}
	return platform.Hash(raw)[:32], nil
}
func reqID(r *http.Request) (int64, error) { return strconv.ParseInt(r.PathValue("id"), 10, 64) }
