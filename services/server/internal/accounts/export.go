package accounts

import (
	"context"
	"encoding/json"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"net/http"
)

func jsonDecode(raw []byte, target any) error { return json.Unmarshal(raw, target) }
func jsonObject(ctx context.Context, q platform.Querier, sql string, args ...any) (json.RawMessage, error) {
	var raw []byte
	err := q.QueryRow(ctx, sql, args...).Scan(&raw)
	return json.RawMessage(raw), err
}
func jsonObjects(ctx context.Context, q platform.Querier, sql string, args ...any) ([]json.RawMessage, error) {
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

func (s *Service) MeExport(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	payload, err := s.Export(r.Context(), a, true)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}
func (s *Service) WardExport(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	ward, err := s.ward(r.Context(), s.DB, a, r.PathValue("public_id"))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	payload, err := s.Export(r.Context(), ward, false)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}

func (s *Service) Export(ctx context.Context, a platform.Actor, self bool) (map[string]any, error) {
	out := map[string]any{"schema_version": 5, "generated_at": s.Config.Now()}
	single := []struct{ Key, SQL string }{
		{"profile", `SELECT jsonb_build_object('public_id',public_id,'username',username,'display_name',display_name,'age_band',age_band,'cohort',cohort,'role',role,'is_identity_verified',is_identity_verified,'identity_verified_at',identity_verified_at,'is_active',is_active,'date_joined',date_joined) FROM accounts_user WHERE id=$1`},
		{"api_access", `SELECT jsonb_build_object('api_token_issued',EXISTS(SELECT 1 FROM authtoken_token WHERE user_id=$1),'issued_at',(SELECT created FROM authtoken_token WHERE user_id=$1))`},
		{"privacy_settings", `SELECT jsonb_build_object('muted_notification_kinds',COALESCE((SELECT to_jsonb(muted_kinds) FROM notifications_notificationpreference WHERE user_id=$1),'[]'::jsonb),'access_preferences',(SELECT jsonb_build_object('needs_step_free',needs_step_free,'needs_accessible_toilet',needs_accessible_toilet,'needs_hearing_loop',needs_hearing_loop,'prefers_quiet',prefers_quiet) FROM places_accesspreference WHERE user_id=$1),'avatar_style',jsonb_build_object('generation',COALESCE(s.generation,1),'name',CASE WHEN s.generation=2 THEN 'Orbits' ELSE 'Constellation' END)) FROM accounts_user u LEFT JOIN accounts_signatureavatar s ON s.user_id=u.id WHERE u.id=$1`},
		{"donations", `SELECT jsonb_build_object('count',count(*),'completed_count',count(*) FILTER(WHERE d.status='completed'),'completed_total_cents',COALESCE(sum(d.amount_cents) FILTER(WHERE d.status='completed'),0),'items',COALESCE(jsonb_agg(jsonb_build_object('amount_cents',d.amount_cents,'currency',d.currency,'recurring',d.recurring,'campaign',c.title,'provider',d.provider,'status',d.status,'external_ref',d.external_ref,'created_at',d.created_at,'completed_at',d.completed_at) ORDER BY d.created_at),'[]'::jsonb)) FROM donations_donation d LEFT JOIN donations_campaign c ON c.id=d.campaign_id WHERE d.donor_id=$1`},
	}
	for _, item := range single {
		value, err := jsonObject(ctx, s.DB, item.SQL, a.ID)
		if err != nil {
			return nil, err
		}
		out[item.Key] = value
	}
	lists := []struct{ Key, SQL string }{
		{"age_assurance", `SELECT jsonb_build_object('provider',provider,'method',method,'age_band',age_band,'verified_at',verified_at,'expires_at',expires_at,'evidence',raw) FROM accounts_ageassurance WHERE user_id=$1 ORDER BY verified_at,id`},
		{"memberships", `SELECT jsonb_build_object('activity_id',m.activity_id,'activity_title',a.title,'role',m.role,'state',m.state,'created_at',m.created_at,'decided_at',m.decided_at) FROM social_membership m JOIN social_activity a ON a.id=m.activity_id WHERE m.user_id=$1 ORDER BY m.created_at,m.id`},
		{"owned_activities", `SELECT jsonb_build_object('id',id,'title',title,'status',status,'cohort',cohort,'starts_at',starts_at,'created_at',created_at) FROM social_activity WHERE owner_id=$1 ORDER BY created_at,id`},
		{"owned_groups", `SELECT jsonb_build_object('id',g.id,'title',g.title,'status',g.status,'cohort',g.cohort,'area',a.name,'is_staff_curated',g.is_staff_curated,'created_at',g.created_at) FROM social_group g JOIN communities_area a ON a.id=g.area_id WHERE g.owner_id=$1 ORDER BY g.created_at,g.id`},
		{"group_memberships", `SELECT jsonb_build_object('group_id',m.group_id,'group_title',g.title,'role',m.role,'state',m.state,'joined_at',m.joined_at) FROM social_groupmembership m JOIN social_group g ON g.id=m.group_id WHERE m.user_id=$1 ORDER BY m.joined_at,m.id`},
		{"blocks", `SELECT jsonb_build_object('blocked',COALESCE(NULLIF(u.display_name,''),u.username),'blocked_public_id',u.public_id,'created_at',b.created_at) FROM safety_block b JOIN accounts_user u ON u.id=b.blocked_id WHERE b.blocker_id=$1 ORDER BY b.created_at,b.id`},
	}
	for _, item := range lists {
		value, err := jsonObjects(ctx, s.DB, item.SQL, a.ID)
		if err != nil {
			return nil, err
		}
		out[item.Key] = value
	}
	consents, err := jsonObjects(ctx, s.DB, `SELECT jsonb_build_object('status',status,'scope',scope,'guardian_identifier',guardian_identifier,'granted_at',granted_at,'expires_at',expires_at,'revoked_at',revoked_at,'created_at',created_at) FROM accounts_parentalconsent WHERE minor_id=$1 ORDER BY created_at,id`, a.ID)
	if err != nil {
		return nil, err
	}
	out["consents"] = map[string]any{"as_minor": consents}
	wards, err := jsonObjects(ctx, s.DB, `SELECT jsonb_build_object('ward_public_id',w.public_id,'relationship',g.relationship,'status',g.status,'created_at',g.created_at) FROM accounts_guardianrelationship g JOIN accounts_user w ON w.id=g.ward_id WHERE g.guardian_id=$1 ORDER BY g.created_at,g.id`, a.ID)
	if err != nil {
		return nil, err
	}
	guardians, err := jsonObjects(ctx, s.DB, `SELECT jsonb_build_object('guardian_public_id',u.public_id,'relationship',g.relationship,'status',g.status,'created_at',g.created_at) FROM accounts_guardianrelationship g JOIN accounts_user u ON u.id=g.guardian_id WHERE g.ward_id=$1 ORDER BY g.created_at,g.id`, a.ID)
	if err != nil {
		return nil, err
	}
	out["guardianships"] = map[string]any{"as_guardian_of": wards, "guarded_by": guardians}
	posts, err := s.exportPosts(ctx, a.ID, self)
	if err != nil {
		return nil, err
	}
	out["thread_posts"] = posts
	safety, err := s.SafetyRecord(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	out["safety_record"] = safety
	sentiment := map[string]any{}
	for _, item := range []struct{ Key, Table, Extra string }{{"reactions", "social_postreaction", ",'facet',emoji"}, {"dissents", "social_postdissent", ""}, {"concerns", "social_postconcern", ""}} {
		value, err := jsonObjects(ctx, s.DB, `SELECT jsonb_build_object('post_id',post_id,'created_at',created_at`+item.Extra+`) FROM `+item.Table+` WHERE user_id=$1 ORDER BY created_at,id`, a.ID)
		if err != nil {
			return nil, err
		}
		sentiment[item.Key] = value
	}
	out["own_sentiment_actions"] = sentiment
	return out, nil
}

func (s *Service) exportPosts(ctx context.Context, user int64, self bool) (map[string]any, error) {
	var total int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM social_post WHERE author_id=$1`, user).Scan(&total)
	if err != nil {
		return nil, err
	}
	rows, err := jsonObjects(ctx, s.DB, `WITH newest AS (SELECT p.*,EXISTS(SELECT 1 FROM safety_moderationaction m JOIN django_content_type c ON c.id=m.target_type_id WHERE c.app_label='social' AND c.model='post' AND m.target_id=p.id AND m.action='remove' AND m.lifted_at IS NULL) AS standing_remove FROM social_post p WHERE p.author_id=$1 ORDER BY p.created_at DESC,p.id DESC LIMIT $3) SELECT jsonb_build_object('thread_kind',CASE WHEN t.group_id IS NULL THEN 'activity' ELSE 'group' END,'thread_id',COALESCE(t.group_id,t.activity_id),'thread_title',COALESCE(g.title,a.title),'body',CASE WHEN NOT p.is_hidden THEN p.body WHEN p.is_author_deleted AND NOT p.standing_remove AND $2 THEN p.body ELSE '[removed]' END,'status',CASE WHEN NOT p.is_hidden THEN 'visible' WHEN p.is_author_deleted AND NOT p.standing_remove THEN 'deleted_by_you' ELSE 'removed' END,'is_announcement',p.is_announcement,'edited',p.updated_at>p.created_at,'had_attachment',EXISTS(SELECT 1 FROM media_attachment WHERE post_id=p.id),'created_at',p.created_at) FROM newest p JOIN social_thread t ON t.id=p.thread_id LEFT JOIN social_activity a ON a.id=t.activity_id LEFT JOIN social_group g ON g.id=t.group_id ORDER BY p.created_at,p.id`, user, self, s.Config.ExportPostCap)
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": rows, "total": total, "truncated": total > len(rows)}, nil
}
