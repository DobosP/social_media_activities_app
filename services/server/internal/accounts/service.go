package accounts

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/avatars"
	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	DemoEnabled                bool
	EUDIClientID               string
	TrustedIssuers             map[string]string
	IdentityUniquenessEnforced bool
	AllowMinorOnboarding       bool
	GuardianInviteTTL          time.Duration
	ConsentValidity            time.Duration
	APITokenTTL                time.Duration
	ExportPostCap              int
	LoginFailureLimit          int
	LoginFailureWindow         time.Duration
	Now                        func() time.Time
}
type Service struct {
	DB           *pgxpool.Pool
	Auth         *authcore.Service
	Store        *Store
	Secret       []byte
	Config       Config
	RatePolicies map[string]budgets.Policy
}

func New(db *pgxpool.Pool, auth *authcore.Service, identityBindingSecret string, config Config) *Service {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.GuardianInviteTTL == 0 {
		config.GuardianInviteTTL = 7 * 24 * time.Hour
	}
	if config.ConsentValidity == 0 {
		config.ConsentValidity = 365 * 24 * time.Hour
	}
	if config.APITokenTTL == 0 {
		config.APITokenTTL = 90 * 24 * time.Hour
	}
	if config.ExportPostCap <= 0 || config.ExportPostCap > 5000 {
		config.ExportPostCap = 5000
	}
	if config.LoginFailureLimit == 0 {
		config.LoginFailureLimit = 10
	}
	if config.LoginFailureWindow == 0 {
		config.LoginFailureWindow = 15 * time.Minute
	}
	s := &Service{DB: db, Auth: auth, Store: NewStore(db), Secret: []byte(identityBindingSecret), Config: config}
	s.Store.PeerSecret = s.Secret
	return s
}

func (s *Service) Migrate(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `CREATE TABLE IF NOT EXISTS accounts_go_age_state(state_hash char(64) PRIMARY KEY,user_id bigint NOT NULL REFERENCES accounts_user(id) ON DELETE CASCADE,nonce text NOT NULL,expires_at timestamptz NOT NULL);CREATE INDEX IF NOT EXISTS accounts_go_age_expiry ON accounts_go_age_state(expires_at);CREATE TABLE IF NOT EXISTS accounts_go_action_budget(user_id bigint NOT NULL REFERENCES accounts_user(id) ON DELETE CASCADE,action text NOT NULL,count integer NOT NULL,until timestamptz NOT NULL,PRIMARY KEY(user_id,action));CREATE INDEX IF NOT EXISTS accounts_go_action_expiry ON accounts_go_action_budget(until)`)
	if err != nil {
		return err
	}
	return s.MigrateLoginFailures(ctx)
}

// pruneExpiredActionBudgets releases all expiry row locks before admission
// acquires its account FK lock. A locked victim is skipped rather than waiting
// behind account erasure; no user lock is acquired while holding unrelated rows.
func (s *Service) pruneExpiredActionBudgets(ctx context.Context, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `WITH expired AS MATERIALIZED (
   SELECT ctid FROM accounts_go_action_budget WHERE until<=$1
   ORDER BY until,user_id,action LIMIT 256 FOR UPDATE SKIP LOCKED
  ) DELETE FROM accounts_go_action_budget b USING expired e WHERE b.ctid=e.ctid`, now)
		return err
	})
}

func (s *Service) allowAction(ctx context.Context, user int64, action string, limit int, window time.Duration) (bool, error) {
	policy, err := budgets.Resolve(s.RatePolicies, action, budgets.Policy{Limit: limit, Window: window})
	if err != nil {
		return false, err
	}
	if s.DB == nil || user < 1 {
		return false, errors.New("account budget actor unavailable")
	}
	now := s.Config.Now()
	if err = s.pruneExpiredActionBudgets(ctx, now); err != nil {
		return false, err
	}
	var allowed bool
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var locked int64
		if err := tx.QueryRow(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR KEY SHARE`, user).Scan(&locked); err != nil {
			return err
		}
		// A bounded sweep may leave this subject's expired bucket behind. Reset it
		// atomically under the user-before-budget lock order rather than pruning
		// other users while holding this actor's account lock.
		err := tx.QueryRow(ctx, `INSERT INTO accounts_go_action_budget(user_id,action,count,until)
   VALUES($1,$2,1,$3) ON CONFLICT(user_id,action) DO UPDATE SET
   count=CASE WHEN accounts_go_action_budget.until<=$5 THEN 1 ELSE accounts_go_action_budget.count+1 END,
   until=CASE WHEN accounts_go_action_budget.until<=$5 THEN EXCLUDED.until ELSE accounts_go_action_budget.until END
   WHERE accounts_go_action_budget.until<=$5 OR accounts_go_action_budget.count<$4 RETURNING true`,
			user, action, now.Add(policy.Window), policy.Limit, now).Scan(&allowed)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return allowed && err == nil, err
}

func (s *Service) actor(ctx context.Context, q platform.Querier, id int64) (platform.Actor, error) {
	var a platform.Actor
	err := q.QueryRow(ctx, `SELECT id,public_id::text,username,display_name,age_band,cohort,role,is_identity_verified,is_active,is_staff,is_superuser FROM accounts_user WHERE id=$1`, id).Scan(&a.ID, &a.PublicID, &a.Username, &a.DisplayName, &a.AgeBand, &a.Cohort, &a.Role, &a.IdentityVerified, &a.IsActive, &a.IsStaff, &a.IsSuperuser)
	return a, err
}
func (s *Service) byPublicID(ctx context.Context, q platform.Querier, public string) (platform.Actor, error) {
	var id int64
	err := q.QueryRow(ctx, `SELECT id FROM accounts_user WHERE public_id=$1::uuid`, public).Scan(&id)
	if err != nil {
		return platform.Actor{}, err
	}
	return s.actor(ctx, q, id)
}
func (s *Service) require(w http.ResponseWriter, r *http.Request) (platform.Actor, bool) {
	return platform.RequireActor(w, r)
}
func (s *Service) isGuardian(ctx context.Context, q platform.Querier, guardian, ward int64) (bool, error) {
	var yes bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='active')`, guardian, ward).Scan(&yes)
	return yes, err
}
func (s *Service) ward(ctx context.Context, q platform.Querier, a platform.Actor, public string) (platform.Actor, error) {
	ward, err := s.byPublicID(ctx, q, public)
	if err != nil {
		return ward, err
	}
	yes, err := s.isGuardian(ctx, q, a.ID, ward.ID)
	if err != nil {
		return ward, err
	}
	if !yes {
		return ward, platform.ErrForbidden
	}
	return ward, nil
}
func (s *Service) wardPayload(ctx context.Context, q platform.Querier, a platform.Actor) (map[string]any, error) {
	err := platform.Participate(ctx, q, a)
	if err != nil && !errors.Is(err, platform.ErrForbidden) {
		return nil, err
	}
	return map[string]any{"public_id": a.PublicID, "username": a.Username, "display_name": a.DisplayName, "age_band": a.AgeBand, "cohort": a.Cohort, "is_active": a.IsActive, "can_participate": err == nil}, nil
}
func (s *Service) Self(ctx context.Context, q platform.Querier, a platform.Actor) (map[string]any, error) {
	var guardian bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_guardianrelationship WHERE guardian_id=$1 AND status='active')`, a.ID).Scan(&guardian)
	if err != nil {
		return nil, err
	}
	can := platform.Participate(ctx, q, a)
	if can != nil && !errors.Is(can, platform.ErrForbidden) {
		return nil, can
	}
	progression, err := Progression(ctx, q, a.ID)
	if err != nil {
		return nil, err
	}
	avatar, err := renderAvatar(ctx, q, a.ID, 80, float64(progression["level"])/5)
	if err != nil {
		return nil, err
	}
	style, err := s.Style(ctx, q, a.ID, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"public_id": a.PublicID, "username": a.Username, "display_name": a.DisplayName, "age_band": a.AgeBand, "cohort": a.Cohort, "role": a.Role, "is_identity_verified": a.IdentityVerified, "requires_parental_consent": a.AgeBand == "under_16", "is_guardian": guardian, "can_participate": can == nil, "progression": progression, "avatar": avatar, "avatar_style": style}, nil
}

func (s *Service) Style(ctx context.Context, q platform.Querier, user int64, previews bool) (map[string]any, error) {
	in, err := avatarInput(ctx, q, user)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"generation": in.Generation, "generation_name": avatars.GenerationName(in.Generation), "available": []map[string]any{{"generation": 1, "name": "Constellation"}, {"generation": 2, "name": "Orbits"}}}
	if previews {
		list := []map[string]any{}
		for _, generation := range []int{1, 2} {
			salt := 0
			if generation == in.Generation {
				salt = in.Salt
			}
			svg := avatars.RenderGeneration(generation, avatars.SignatureSeed(in.Seed, generation, salt), in.Nodes, in.Edges, avatars.Options{PX: 96})
			list = append(list, map[string]any{"generation": generation, "name": avatars.GenerationName(generation), "uri": avatars.DataURI(svg), "current": generation == in.Generation})
		}
		result["previews"] = list
	}
	return result, nil
}

func (s *Service) PickStyle(ctx context.Context, a platform.Actor, generation int) error {
	if generation != 1 && generation != 2 {
		return platform.ErrInvalid
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR UPDATE`, a.ID); err != nil {
			return err
		}
		in, err := avatarInput(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		var oldGeneration, oldSalt int
		var oldFingerprint string
		err = tx.QueryRow(ctx, `SELECT generation,salt,fingerprint FROM accounts_signatureavatar WHERE user_id=$1 FOR UPDATE`, a.ID).Scan(&oldGeneration, &oldSalt, &oldFingerprint)
		has := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if has && oldGeneration == generation && oldFingerprint == canonicalFingerprint(in, generation, oldSalt) {
			return nil
		}
		order := []int{oldSalt}
		for salt := 0; salt < 16; salt++ {
			if salt != oldSalt {
				order = append(order, salt)
			}
		}
		for _, salt := range order {
			fingerprint := canonicalFingerprint(in, generation, salt)
			var clash bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_signatureavatar WHERE fingerprint=$1 AND user_id<>$2)`, fingerprint, a.ID).Scan(&clash); err != nil {
				return err
			}
			if clash {
				continue
			}
			if _, err = tx.Exec(ctx, `SAVEPOINT avatar_pick`); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO accounts_signatureavatar(user_id,generation,salt,fingerprint,created_at,updated_at) VALUES($1,$2,$3,$4,now(),now()) ON CONFLICT(user_id) DO UPDATE SET generation=EXCLUDED.generation,salt=EXCLUDED.salt,fingerprint=EXCLUDED.fingerprint,updated_at=now()`, a.ID, generation, salt, fingerprint)
			if err != nil {
				_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT avatar_pick`)
				if errors.Is(storeError(err), authcore.ErrConflict) {
					continue
				}
				return err
			}
			if _, err = tx.Exec(ctx, `RELEASE SAVEPOINT avatar_pick`); err != nil {
				return err
			}
			return platform.RecordAudit(ctx, tx, a, "avatar.style_changed", "accounts.user:"+strconv.FormatInt(a.ID, 10), map[string]int{"generation": generation})
		}
		return platform.ErrInvalid
	})
}
