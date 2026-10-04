package social

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"
)

type groupState struct {
	ID, OwnerID, ThreadID, AreaID, CategoryID int64
	TypeID                                    *int64
	Cohort, Status, Title, Tier, City         string
	Hidden                                    bool
}

const groupColumns = `jsonb_build_object('id',g.id,'title',g.title,'description',g.description,'owner',u.display_name,'area',ar.name,'category',cat.name,'activity_type',t.name,'tier',g.tier,'cohort',g.cohort,'status',g.status,'is_staff_curated',g.is_staff_curated,'is_publicly_listed',g.is_publicly_listed,'created_at',g.created_at)`
const groupJoin = ` FROM social_group g JOIN accounts_user u ON u.id=g.owner_id JOIN communities_area ar ON ar.id=g.area_id JOIN taxonomy_activitycategory cat ON cat.id=g.category_id LEFT JOIN taxonomy_activitytype t ON t.id=g.activity_type_id `
const groupVisible = `g.cohort=$2 AND g.status='active' AND NOT g.is_hidden AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=g.owner_id) OR (b.blocker_id=g.owner_id AND b.blocked_id=$1))`

func group(ctx context.Context, q platform.Querier, a Actor, id int64, lock, staffBypass bool) (groupState, error) {
	var v groupState
	if !assigned(a) {
		return v, platform.ErrNotFound
	}
	sql := `SELECT g.id,g.owner_id,COALESCE(th.id,0),g.area_id,g.category_id,g.activity_type_id,g.cohort,g.status,g.title,g.tier,ar.city,g.is_hidden FROM social_group g JOIN communities_area ar ON ar.id=g.area_id LEFT JOIN social_thread th ON th.group_id=g.id WHERE g.id=$3 AND (($4 AND $5) OR (` + groupVisible + `))`
	if lock {
		sql += ` FOR UPDATE OF g`
	}
	err := q.QueryRow(ctx, sql, a.ID, a.Cohort, id, staffBypass, a.IsStaff).Scan(&v.ID, &v.OwnerID, &v.ThreadID, &v.AreaID, &v.CategoryID, &v.TypeID, &v.Cohort, &v.Status, &v.Title, &v.Tier, &v.City, &v.Hidden)
	return v, err
}
func (s *Service) Group(ctx context.Context, a Actor, id int64) (json.RawMessage, error) {
	if _, err := group(ctx, s.DB, a, id, false, true); err != nil {
		return nil, err
	}
	return object(ctx, s.DB, `SELECT `+groupColumns+groupJoin+` WHERE g.id=$1`, id)
}
func (s *Service) listGroups(ctx context.Context, a Actor, limit, offset int) ([]json.RawMessage, error) {
	if !assigned(a) {
		return []json.RawMessage{}, nil
	}
	return objects(ctx, s.DB, `SELECT `+groupColumns+groupJoin+` WHERE `+groupVisible+` ORDER BY g.title,g.id LIMIT $3 OFFSET $4`, a.ID, a.Cohort, limit, offset)
}

type GroupInput struct {
	City         string `json:"city"`
	ActivityType *int64 `json:"activity_type"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Cohort       string `json:"cohort"`
}

var slugSeparators = regexp.MustCompile(`[\s-]+`)

func citySlug(value string) string {
	var ascii strings.Builder
	for _, r := range norm.NFKD.String(value) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSpace(r) || r == '_' || r == '-') {
			ascii.WriteRune(unicode.ToLower(r))
		}
	}
	return strings.Trim(slugSeparators.ReplaceAllString(ascii.String(), "-"), "-_")
}
func ensureArea(ctx context.Context, tx pgx.Tx, city string) (int64, error) {
	var id int64
	city = strings.TrimSpace(city)
	if city == "" || utf8.RuneCountInString(city) > 128 {
		return 0, platform.ErrInvalid
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(lower($1),193341))`, city); err != nil {
		return 0, err
	}
	err := tx.QueryRow(ctx, `SELECT id FROM communities_area WHERE lower(city)=lower($1) AND derive_method='city' ORDER BY id LIMIT 1`, city).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return 0, err
	}
	slug := citySlug(city)
	if slug == "" {
		slug = "area"
	}
	if len(slug) > 96 {
		slug = slug[:96]
	}
	for n := 0; n < 100; n++ {
		candidate := slug
		if n > 0 {
			suffix := fmt.Sprintf("-%d", n+1)
			candidate = slug[:min(len(slug), 96-len(suffix))] + suffix
		}
		err = tx.QueryRow(ctx, `INSERT INTO communities_area(city,slug,name,derive_method,min_radius_m,is_active,created_at) VALUES($1,$2,$1,'city',1500,true,now()) ON CONFLICT(slug) DO NOTHING RETURNING id`, city, candidate).Scan(&id)
		if err == nil {
			return id, nil
		}
		if err != pgx.ErrNoRows {
			return 0, err
		}
	}
	return 0, platform.ErrInvalid
}
func (s *Service) CreateGroup(ctx context.Context, a Actor, in GroupInput) (int64, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || utf8.RuneCountInString(in.Title) > 200 || utf8.RuneCountInString(in.Description) > 2000 || in.ActivityType == nil || *in.ActivityType <= 0 {
		return 0, platform.ErrInvalid
	}
	if !a.IsStaff && !s.AllowUserGroups {
		return 0, platform.ErrForbidden
	}
	if !s.allow(ctx, a.ID, "group_create", 5, time.Hour) {
		return 0, platform.ErrForbidden
	}
	var id int64
	err := s.transaction(ctx, a, func(tx pgx.Tx) error {
		cohort := a.Cohort
		if in.Cohort != "" {
			cohort = in.Cohort
		}
		if !a.IsStaff && cohort != a.Cohort {
			return platform.ErrForbidden
		}
		if cohort == "child" || cohort == "teen" {
			if !a.IsStaff || !s.MinorOnboardingEnabled {
				return platform.ErrForbidden
			}
		} else if cohort != "adult" {
			return platform.ErrForbidden
		}
		areaID, err := ensureArea(ctx, tx, in.City)
		if err != nil {
			return err
		}
		var categoryID int64
		if err := tx.QueryRow(ctx, `SELECT category_id FROM taxonomy_activitytype WHERE id=$1 AND is_active`, *in.ActivityType).Scan(&categoryID); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO social_group(owner_id,area_id,category_id,activity_type_id,tier,cohort,title,description,status,is_hidden,is_publicly_listed,is_staff_curated,created_at,updated_at) VALUES($1,$2,$3,$4,'type',$5,$6,$7,'active',false,false,$8,now(),now()) RETURNING id`, a.ID, areaID, categoryID, *in.ActivityType, cohort, in.Title, in.Description, a.IsStaff).Scan(&id)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO social_groupmembership(group_id,user_id,role,state,joined_at) VALUES($1,$2,'owner','member',now())`, id, a.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO social_thread(group_id,activity_id,created_at) VALUES($1,NULL,now())`, id); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "group.created", "group", id, map[string]any{"cohort": cohort, "staff_curated": a.IsStaff})
	})
	return id, err
}
func (s *Service) JoinGroup(ctx context.Context, a Actor, id int64) error {
	return s.rateTransaction(ctx, a, "group_join", 20, time.Hour, func(tx pgx.Tx, reserve func() error) error {
		if _, err := group(ctx, tx, a, id, true, false); err != nil {
			return err
		}
		existing, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM social_groupmembership WHERE group_id=$1 AND user_id=$2 AND state='member')`, id, a.ID)
		if err != nil {
			return err
		}
		if existing {
			return nil
		}
		if err := reserve(); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO social_groupmembership(group_id,user_id,role,state,joined_at) VALUES($1,$2,'member','member',now()) ON CONFLICT(group_id,user_id) DO UPDATE SET state='member'`, id, a.ID); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "group.joined", "group", id, nil)
	})
}
func (s *Service) LeaveGroup(ctx context.Context, a Actor, id int64) error {
	return s.privacyTransaction(ctx, a, func(tx pgx.Tx) error {
		if _, err := group(ctx, tx, a, id, true, false); err != nil {
			return err
		}
		var role string
		if err := tx.QueryRow(ctx, `SELECT role FROM social_groupmembership WHERE group_id=$1 AND user_id=$2 AND state='member' FOR UPDATE`, id, a.ID).Scan(&role); err != nil {
			return err
		}
		if role == "owner" {
			return platform.ErrInvalid
		}
		if _, err := tx.Exec(ctx, `UPDATE social_groupmembership SET state='left' WHERE group_id=$1 AND user_id=$2`, id, a.ID); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "group.left", "group", id, nil)
	})
}
func (s *Service) SetGroupListing(ctx context.Context, a Actor, id int64, listed bool) error {
	return s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := group(ctx, tx, a, id, true, true)
		if err != nil {
			return err
		}
		if v.OwnerID != a.ID || v.Cohort != "adult" {
			return platform.ErrForbidden
		}
		if _, err := tx.Exec(ctx, `UPDATE social_group SET is_publicly_listed=$2 WHERE id=$1`, id, listed); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "group.public_listing", "group", id, map[string]bool{"listed": listed})
	})
}
func (s *Service) ArchiveGroup(ctx context.Context, a Actor, id int64) error {
	return s.privacyTransaction(ctx, a, func(tx pgx.Tx) error {
		v, err := group(ctx, tx, a, id, true, true)
		if err != nil {
			return err
		}
		if v.OwnerID != a.ID && !a.IsStaff {
			return platform.ErrForbidden
		}
		if _, err := tx.Exec(ctx, `UPDATE social_group SET status='archived',updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "group.archived", "group", id, nil)
	})
}
func groupRecipients(ctx context.Context, q platform.Querier, a Actor, id int64) ([]int64, error) {
	rows, err := q.Query(ctx, `SELECT user_id FROM social_groupmembership m WHERE group_id=$1 AND state='member' AND user_id<>$2 AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$2 AND b.blocked_id=m.user_id) OR (b.blocker_id=m.user_id AND b.blocked_id=$2)) ORDER BY user_id`, id, a.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *Service) GroupRoster(ctx context.Context, a Actor, id int64) ([]string, error) {
	v, err := group(ctx, s.DB, a, id, false, false)
	if err != nil {
		return nil, err
	}
	if a.Cohort != "adult" || v.Cohort != a.Cohort {
		return nil, platform.ErrForbidden
	}
	if err := platform.Participate(ctx, s.DB, a); err != nil {
		return nil, err
	}
	ok, err := scalar(ctx, s.DB, `SELECT EXISTS(SELECT 1 FROM social_groupmembership WHERE group_id=$1 AND user_id=$2 AND state='member')`, id, a.ID)
	if err := errorIfFalse(ok, err); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT u.id FROM social_groupmembership m JOIN accounts_user u ON u.id=m.user_id WHERE m.group_id=$1 AND m.state='member' AND u.cohort=$3 AND u.is_active AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$2 AND b.blocked_id=u.id) OR (b.blocker_id=u.id AND b.blocked_id=$2)) ORDER BY m.joined_at,m.id LIMIT 1000`, id, a.ID, a.Cohort)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, uid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, uid := range ids {
		target, err := actorByID(ctx, s.DB, uid)
		if err != nil {
			return nil, err
		}
		if err = platform.Participate(ctx, s.DB, target); err == platform.ErrForbidden {
			continue
		} else if err != nil {
			return nil, err
		}
		name := target.DisplayName
		if name == "" {
			name = target.Username
		}
		names = append(names, name)
	}
	return names, nil
}

// EnsureCityArea resolves an area-only city filter under its transaction lock.
func EnsureCityArea(ctx context.Context, tx pgx.Tx, city string) (int64, error) {
	return ensureArea(ctx, tx, city)
}
