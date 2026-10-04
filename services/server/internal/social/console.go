package social

import (
	"context"
	"net/http"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
)

func (s *Service) console(w http.ResponseWriter, r *http.Request, a Actor) {
	v, err := s.OrganizerConsole(r.Context(), a)
	response(w, v, err, 200)
}
func (s *Service) OrganizerConsole(ctx context.Context, a Actor) (map[string]any, error) {
	policy := catalog.PolicyFromContext(ctx)
	activities, err := objects(ctx, s.DB, `SELECT jsonb_build_object('id',a.id,'title',a.title,'starts_at',a.starts_at,'pending_joins',stats.pending,'support_companions',stats.support,'needs_supervisor',a.supervised AND NOT EXISTS(SELECT 1 FROM social_membership sup JOIN accounts_guardianrelationship rel ON rel.guardian_id=sup.user_id AND rel.ward_id=a.owner_id AND rel.status='active' JOIN accounts_user guardian ON guardian.id=sup.user_id WHERE sup.activity_id=a.id AND sup.state='member' AND sup.role='guardian' AND guardian.is_active AND guardian.is_identity_verified AND guardian.cohort='adult'),'missing_meeting_point',a.starts_at<=now()+interval '48 hours' AND btrim(a.meeting_point)='','readiness',jsonb_build_object('missing_what_to_bring',btrim(a.what_to_bring)='','near_capacity',a.capacity IS NOT NULL AND stats.total>=a.capacity),'quorum',jsonb_build_object('going',stats.going,'total',stats.total,'min_to_go',a.min_to_go,'met_minimum',CASE WHEN a.min_to_go IS NULL THEN NULL ELSE stats.going>=a.min_to_go END,'remaining_needed',CASE WHEN a.min_to_go IS NULL THEN NULL ELSE GREATEST(0,a.min_to_go-stats.going) END),'venue_flag',(SELECT COUNT(*) FROM places_opennowreport report WHERE report.place_id=a.place_id AND report.created_at>=now()-$2::double precision*interval '1 second')>=$3) FROM social_activity a CROSS JOIN LATERAL(SELECT COUNT(*) FILTER(WHERE m.state='requested') pending,COUNT(*) FILTER(WHERE m.state='member' AND m.role<>'guardian') total,COUNT(*) FILTER(WHERE m.state='member' AND m.role<>'guardian' AND m.attendance_intent='going') going,COUNT(*) FILTER(WHERE m.state='member' AND m.role<>'guardian' AND m.brings_support_person) support FROM social_membership m WHERE m.activity_id=a.id) stats WHERE a.status='open' AND a.starts_at>=now() AND (a.owner_id=$1 OR EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=a.id AND m.user_id=$1 AND m.role='co_organizer' AND m.state='member')) ORDER BY a.starts_at,a.id LIMIT 100`, a.ID, policy.OpenNowReportDecay.Seconds(), policy.OpenNowReportThreshold)
	if err != nil {
		return nil, err
	}
	series, err := objects(ctx, s.DB, `SELECT jsonb_build_object('id',id,'title',title,'cadence',cadence,'next_starts_at',next_starts_at) FROM social_activityseries WHERE owner_id=$1 AND status<>'ended' ORDER BY next_starts_at,id LIMIT 100`, a.ID)
	if err != nil {
		return nil, err
	}
	groups, err := objects(ctx, s.DB, `SELECT jsonb_build_object('id',g.id,'title',g.title) FROM social_group g WHERE g.owner_id=$1 AND `+groupVisible+` ORDER BY g.title,g.id LIMIT 100`, a.ID, a.Cohort)
	if err != nil {
		return nil, err
	}
	return map[string]any{"activities": activities, "series": series, "groups": groups}, nil
}
