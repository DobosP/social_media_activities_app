package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
	"github.com/jackc/pgx/v5"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type AccountRetentionConfig struct{ MessagingDays, ArrivalHours, AdultPhotoMinimumSeconds, MinorPhotoMinimumSeconds int }

func accountUserMap(a platform.Actor) map[string]any {
	return map[string]any{"id": a.ID, "pk": a.ID, "public_id": a.PublicID, "username": a.Username, "display_name": a.DisplayName, "age_band": a.AgeBand, "cohort": a.Cohort, "role": a.Role, "is_active": a.IsActive, "is_authenticated": a.ID > 0 && a.IsActive, "is_identity_verified": a.IdentityVerified, "is_moderator": a.Moderator(), "is_admin": a.Admin(), "requires_parental_consent": a.AgeBand == "under_16", "get_age_band_display": accountBandLabel(a.AgeBand), "get_cohort_display": map[string]string{"unassigned": "Unassigned", "child": "Child (under 16)", "teen": "Teen (16-17)", "adult": "Adult (18+)"}[a.Cohort]}
}
func accountBandLabel(band string) string {
	return map[string]string{"unknown": "Unknown", "under_16": "Under 16", "16_17": "16-17", "adult": "Adult (18+)"}[band]
}
func (s *Server) accountObjects(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	rows, err := s.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var value map[string]any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return nil, platform.ErrInvalid
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
func accountDecodeRows(rows []json.RawMessage) []map[string]any {
	out := []map[string]any{}
	for _, raw := range rows {
		var row map[string]any
		if json.Unmarshal(raw, &row) == nil {
			out = append(out, row)
		}
	}
	return out
}
func accountRows(value any) []any {
	if rows, ok := value.([]json.RawMessage); ok {
		out := []any{}
		for _, row := range accountDecodeRows(rows) {
			out = append(out, row)
		}
		return out
	}
	return results(value)
}
func (s *Server) accountProvenance(ctx context.Context, a platform.Actor) (map[string]any, error) {
	var band, provider, method string
	var verified time.Time
	var expiry *time.Time
	err := s.DB.QueryRow(ctx, `SELECT age_band,provider,method,verified_at,expires_at FROM accounts_ageassurance WHERE user_id=$1 ORDER BY verified_at DESC,id DESC LIMIT 1`, a.ID).Scan(&band, &provider, &method, &verified, &expiry)
	if errors.Is(err, pgx.ErrNoRows) {
		if !a.IdentityVerified {
			return nil, nil
		}
		return map[string]any{"has_row": false, "is_current": true, "band_display": accountBandLabel(a.AgeBand), "status": "no_expiry", "expires_soon": false}, nil
	}
	if err != nil {
		return nil, err
	}
	now := s.Accounts.Config.Now()
	current := a.IdentityVerified && (expiry == nil || expiry.After(now))
	status := "no_expiry"
	var days any
	soon := false
	if expiry != nil {
		days = max(0, int(math.Ceil(expiry.Sub(now).Hours()/24)))
		status = "current"
		if !current {
			status = "expired"
		} else if days.(int) <= 14 {
			status = "expiring"
			soon = true
		}
	}
	if method == "openid4vp" {
		method = "the EU Digital Identity wallet"
	}
	var expires any
	if expiry != nil {
		expires = *expiry
	}
	return map[string]any{"has_row": true, "provider": provider, "method": method, "band_display": accountBandLabel(band), "verified_at": verified, "expires_at": expires, "is_current": current, "expires_soon": soon, "days_left": days, "status": status}, nil
}
func (s *Server) AccountView(r *http.Request, a platform.Actor, name string) (pongo2.Context, string, bool, error) {
	known := map[string]bool{"profile": true, "you": true, "settings": true, "my_privacy": true, "activity_log": true, "safety_record": true, "verify_age": true, "wards": true, "my_guardians": true, "guardianship": true, "notification_preferences": true, "access_preferences": true, "display_preferences": true, "account_delete": true, "privacy": true, "terms": true}
	if !known[name] {
		return nil, "", false, nil
	}
	data := pongo2.Context{}
	template := "web/" + name + ".html"
	if name == "guardianship" {
		template = "web/guardianship.html"
	}
	if name == "my_guardians" {
		template = "web/guardianship.html"
	}
	if name == "display_preferences" || name == "privacy" || name == "terms" {
		return data, template, true, nil
	}
	if a.ID < 1 || !a.IsActive {
		return nil, "", true, platform.ErrForbidden
	}
	if s.Accounts == nil || s.DB == nil {
		return nil, "", true, errors.New("account service unavailable")
	}
	ctx := r.Context()
	user := accountUserMap(a)
	var isGuardian, hasGuardians bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_guardianrelationship WHERE guardian_id=$1 AND status='active'),EXISTS(SELECT 1 FROM accounts_guardianrelationship WHERE ward_id=$1 AND status='active')`, a.ID).Scan(&isGuardian, &hasGuardians)
	if err != nil {
		return nil, "", true, err
	}
	user["is_guardian"] = isGuardian
	data["user"] = user
	data["is_guardian"] = isGuardian
	data["connections_enabled"] = s.Social != nil && s.Social.ConnectionCohorts[a.Cohort]
	data["has_guardians"] = hasGuardians
	switch name {
	case "you":
	case "profile":
		self, err := s.Accounts.Self(ctx, s.DB, a)
		if err != nil {
			return nil, "", true, err
		}
		provenance, err := s.accountProvenance(ctx, a)
		if err != nil {
			return nil, "", true, err
		}
		data["provenance"] = provenance
		baseAvatar, avatarErr := accounts.Avatar(ctx, s.DB, a.ID)
		if avatarErr != nil {
			return nil, "", true, avatarErr
		}
		user["avatar_uri"] = baseAvatar
		data["avatar_uri"] = baseAvatar
		data["profile_user"] = user
		data["can_participate"] = self["can_participate"]
		data["progression"] = self["progression"]
		data["journey_avatar"] = self["avatar"]
		style, err := s.Accounts.Style(ctx, s.DB, a.ID, true)
		if err != nil {
			return nil, "", true, err
		}
		data["avatar_styles"] = style["previews"]
		rows, err := s.DB.Query(ctx, `SELECT t.name FROM recommendations_userinterest i JOIN taxonomy_activitytype t ON t.id=i.activity_type_id WHERE i.user_id=$1 ORDER BY i.id`, a.ID)
		if err != nil {
			return nil, "", true, err
		}
		chosen := []string{}
		for rows.Next() {
			var value string
			if err = rows.Scan(&value); err != nil {
				rows.Close()
				return nil, "", true, err
			}
			chosen = append(chosen, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, "", true, err
		}
		data["interests"] = chosen
		blocked, err := s.accountObjects(ctx, `SELECT jsonb_build_object('pk',u.id,'username',u.username,'display_name',u.display_name) FROM safety_block b JOIN accounts_user u ON u.id=b.blocked_id WHERE b.blocker_id=$1 ORDER BY b.id`, a.ID)
		if err != nil {
			return nil, "", true, err
		}
		data["blocked"] = blocked
		if s.Social != nil {
			connections, err := s.Social.Connections(ctx, a)
			if err != nil {
				return nil, "", true, err
			}
			data["connections"] = accountDecodeRows(connections)
			pending, err := s.Social.PendingConnections(ctx, a)
			if err != nil {
				return nil, "", true, err
			}
			data["pending_in"] = accountDecodeRows(pending["incoming"])
		}
		var photoID int64
		err = s.DB.QueryRow(ctx, `SELECT id FROM media_photo WHERE uploader_id=$1 AND kind='profile' AND scan_status='clean' ORDER BY created_at DESC,id DESC LIMIT 1`, a.ID).Scan(&photoID)
		if err == nil && s.API != nil {
			photo, getErr := s.get(r, "/api/media/photos/"+strconv.FormatInt(photoID, 10)+"/")
			if getErr != nil {
				return nil, "", true, getErr
			}
			data["avatar_url"] = object(photo)["url"]
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, "", true, err
		}
	case "settings":
		var created time.Time
		err = s.DB.QueryRow(ctx, `SELECT created FROM authtoken_token WHERE user_id=$1`, a.ID).Scan(&created)
		if err == nil {
			data["api_token_created"] = created
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, "", true, err
		}
		data["languages"] = []any{[]string{"en", "English"}, []string{"ro", "Română"}}
		data["current_language"] = language(r)
	case "activity_log":
		items, err := s.Safety.ActivityLog(ctx, a.ID)
		if err != nil {
			return nil, "", true, err
		}
		data["entries"] = items
	case "safety_record":
		record, err := s.Accounts.SafetyRecord(ctx, a.ID)
		if err != nil {
			return nil, "", true, err
		}
		for key, value := range record {
			if key == "decisions" || key == "reports" {
				data[key] = accountRows(value)
			} else {
				data[key] = value
			}
		}
	case "my_privacy":
		provenance, err := s.accountProvenance(ctx, a)
		if err != nil {
			return nil, "", true, err
		}
		data["provenance"] = provenance
		value, err := s.get(r, "/api/accounts/me/settings/")
		if err != nil {
			return nil, "", true, err
		}
		preferences := object(value)
		access := object(preferences["access"])
		data["access_set"] = access["needs_step_free"] == true || access["needs_accessible_toilet"] == true || access["prefers_quiet"] == true
		labels := []string{}
		for _, raw := range results(preferences["muted_kinds"]) {
			if label := accountNotificationLabels[fmt.Sprint(raw)]; label != "" {
				labels = append(labels, label)
			}
		}
		sort.Strings(labels)
		data["muted_labels"] = labels
		var interests, donations int64
		var token bool
		if err = s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM recommendations_userinterest WHERE user_id=$1),(SELECT count(*) FROM donations_donation WHERE donor_id=$1),EXISTS(SELECT 1 FROM authtoken_token WHERE user_id=$1)`, a.ID).Scan(&interests, &donations, &token); err != nil {
			return nil, "", true, err
		}
		data["interests_count"] = interests
		data["donations_count"] = donations
		data["api_token_exists"] = token
		record, err := s.Accounts.SafetyRecord(ctx, a.ID)
		if err != nil {
			return nil, "", true, err
		}
		data["decisions_count"] = len(accountRows(record["decisions"]))
		data["reports_count"] = len(accountRows(record["reports"]))
		data["retention"] = s.accountRetention(a, provenance)
	case "verify_age":
		data["sandbox"] = false
		can := platform.Participate(ctx, s.DB, a)
		if can != nil && !errors.Is(can, platform.ErrForbidden) {
			return nil, "", true, can
		}
		data["verified"] = can == nil
		data["native_available"] = s.Accounts.Config.EUDIClientID != "" && len(s.Accounts.Config.TrustedIssuers) > 0
	case "notification_preferences":
		preferences, err := s.get(r, "/api/accounts/me/settings/")
		if err != nil {
			return nil, "", true, err
		}
		muted := map[string]bool{}
		for _, raw := range results(object(preferences)["muted_kinds"]) {
			muted[fmt.Sprint(raw)] = true
		}
		rows := []map[string]any{}
		for _, kind := range accountNotificationOrder {
			rows = append(rows, map[string]any{"value": kind, "label": accountNotificationLabels[kind], "reason": accountNotificationReasons[kind], "muted": muted[kind]})
		}
		data["rows"] = rows
	case "access_preferences":
		preferences, err := s.get(r, "/api/accounts/me/settings/")
		if err != nil {
			return nil, "", true, err
		}
		pref := object(object(preferences)["access"])
		data["pref"] = pref
	case "account_delete":
		preview, err := s.accountErasurePreview(ctx, a.ID)
		if err != nil {
			return nil, "", true, err
		}
		data["preview"] = preview
	case "wards":
		if s.Social == nil {
			return nil, "", true, errors.New("guardian safety projection unavailable")
		}
		wards, err := s.accountObjects(ctx, `SELECT jsonb_build_object('id',u.id,'pk',u.id,'public_id',u.public_id,'username',u.username,'display_name',u.display_name,'age_band',u.age_band,'cohort',u.cohort) FROM accounts_guardianrelationship g JOIN accounts_user u ON u.id=g.ward_id WHERE g.guardian_id=$1 AND g.status='active' ORDER BY u.id`, a.ID)
		if err != nil {
			return nil, "", true, err
		}
		for _, ward := range wards {
			wardID := accountRowID(ward["id"])
			ward["get_cohort_display"] = map[string]string{"child": "Child (under 16)", "teen": "Teen (16-17)", "adult": "Adult (18+)"}[fmt.Sprint(ward["cohort"])]
			caps, err := s.accountCapabilities(ctx, a.ID, wardID)
			if err != nil {
				return nil, "", true, err
			}
			ward["caps"] = caps
			meetups, err := s.accountObjects(ctx, `SELECT jsonb_build_object('id',x.id,'starts_at',x.starts_at,'ends_at',CASE WHEN u.cohort='child' THEN x.ends_at ELSE NULL END,'meeting_point',CASE WHEN u.cohort='child' THEN x.meeting_point ELSE '' END,'supervised',x.supervised,'place',jsonb_build_object('id',p.id,'name',p.name,'address_city',p.address_city),'activity_type',jsonb_build_object('name',t.name),'supervision_live',EXISTS(SELECT 1 FROM social_membership gm JOIN accounts_guardianrelationship gl ON gl.guardian_id=gm.user_id AND gl.ward_id=x.owner_id AND gl.status='active' JOIN accounts_user gu ON gu.id=gm.user_id WHERE gm.activity_id=x.id AND gm.role='guardian' AND gm.state='member' AND gu.cohort='adult' AND gu.is_active AND gu.is_identity_verified)) FROM social_activity x JOIN social_membership m ON m.activity_id=x.id JOIN accounts_user u ON u.id=m.user_id JOIN places_place p ON p.id=x.place_id JOIN taxonomy_activitytype t ON t.id=x.activity_type_id WHERE m.user_id=$1 AND m.state='member' AND x.cohort=u.cohort AND NOT x.is_hidden AND x.status='open' AND x.starts_at>=$2 ORDER BY x.starts_at,x.id`, wardID, time.Now())
			if err != nil {
				return nil, "", true, err
			}
			if s.Social != nil && ward["cohort"] == "child" {
				for _, meetup := range meetups {
					live, liveErr := s.Social.SupervisionSatisfied(ctx, s.DB, accountRowID(meetup["id"]))
					if liveErr != nil {
						return nil, "", true, liveErr
					}
					meetup["supervision_live"] = live
					place := object(meetup["place"])
					rationale, err := s.Social.ChildVenueRationale(ctx, s.DB, accountRowID(place["id"]))
					if err != nil {
						return nil, "", true, err
					}
					meetup["child_venue_rationale"] = rationale
				}
				preview, err := s.Social.GuardrailPreview(ctx, a, wardID, 50)
				if err != nil {
					return nil, "", true, err
				}
				ward["guardrail_preview"] = preview
			}
			ward["meetups"] = meetups
			ward["can_set_topics"] = ward["cohort"] == "child"
			topics, err := s.DB.Query(ctx, `SELECT c.slug FROM recommendations_topicpreference p JOIN taxonomy_activitycategory c ON c.id=p.category_id WHERE p.user_id=$1 ORDER BY c.slug`, wardID)
			if err != nil {
				return nil, "", true, err
			}
			slugs := []string{}
			for topics.Next() {
				var slug string
				if err = topics.Scan(&slug); err != nil {
					topics.Close()
					return nil, "", true, err
				}
				slugs = append(slugs, slug)
			}
			err = topics.Err()
			topics.Close()
			if err != nil {
				return nil, "", true, err
			}
			ward["topic_slugs"] = slugs
		}
		data["wards"] = wards
		data["minor_onboarding"] = s.Accounts.Config.AllowMinorOnboarding
		hours := []int{}
		for hour := 0; hour < 24; hour++ {
			hours = append(hours, hour)
		}
		data["guardrail_hours"] = hours
		data["guardrail_weekdays"] = []any{[]any{1, "Mon"}, []any{2, "Tue"}, []any{3, "Wed"}, []any{4, "Thu"}, []any{5, "Fri"}, []any{6, "Sat"}, []any{7, "Sun"}}
		rows, err := s.DB.Query(ctx, `SELECT slug,name FROM taxonomy_activitycategory WHERE parent_id IS NULL ORDER BY name`)
		if err != nil {
			return nil, "", true, err
		}
		categories := []any{}
		for rows.Next() {
			var slug, label string
			if err = rows.Scan(&slug, &label); err != nil {
				rows.Close()
				return nil, "", true, err
			}
			categories = append(categories, []string{slug, label})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, "", true, err
		}
		data["guardrail_categories"] = categories
	case "my_guardians", "guardianship":
		guardians, err := s.accountObjects(ctx, `SELECT jsonb_build_object('id',u.id,'username',u.username,'display_name',u.display_name) FROM accounts_guardianrelationship g JOIN accounts_user u ON u.id=g.guardian_id WHERE g.ward_id=$1 AND g.status='active' ORDER BY u.display_name,u.id`, a.ID)
		if err != nil {
			return nil, "", true, err
		}
		for _, guardian := range guardians {
			caps, err := s.accountCapabilities(ctx, accountRowID(guardian["id"]), a.ID)
			if err != nil {
				return nil, "", true, err
			}
			guardian["caps"] = caps
		}
		data["guardians"] = guardians
	}
	return data, template, true, nil
}

func (s *Server) accountCapabilities(ctx context.Context, guardianID, wardID int64) (map[string]any, error) {
	var relationship string
	var child, requires, consent, supervised bool
	var earliest, latest, cap *int
	var weekdays string
	var categories []string
	err := s.DB.QueryRow(ctx, `SELECT g.relationship,u.cohort='child',u.age_band='under_16',EXISTS(SELECT 1 FROM accounts_parentalconsent c WHERE c.minor_id=u.id AND c.status='active' AND (c.expires_at IS NULL OR c.expires_at>$3)),COALESCE(r.supervised_only,false),r.earliest_start_hour,r.latest_start_hour,r.max_open_joins,COALESCE(r.allowed_weekdays,''),COALESCE(r.allowed_categories,'{}'::varchar[]) FROM accounts_guardianrelationship g JOIN accounts_user u ON u.id=g.ward_id LEFT JOIN accounts_guardianguardrail r ON r.relationship_id=g.id WHERE g.guardian_id=$1 AND g.ward_id=$2 AND g.status='active'`, guardianID, wardID, time.Now()).Scan(&relationship, &child, &requires, &consent, &supervised, &earliest, &latest, &cap, &weekdays, &categories)
	if err != nil {
		return nil, platform.ErrNotFound
	}
	var actor platform.Actor
	err = s.DB.QueryRow(ctx, `SELECT id,cohort,age_band,is_identity_verified,is_active FROM accounts_user WHERE id=$1`, guardianID).Scan(&actor.ID, &actor.Cohort, &actor.AgeBand, &actor.IdentityVerified, &actor.IsActive)
	if err != nil {
		return nil, err
	}
	can := platform.Participate(ctx, s.DB, actor)
	if can != nil && !errors.Is(can, platform.ErrForbidden) {
		return nil, can
	}
	if relationship == "" {
		relationship = "guardian"
	}
	days := []int{}
	for _, day := range weekdays {
		days = append(days, int(day-'0'))
	}
	blocks := false
	if child && s.Social != nil {
		effective, err := s.Social.EffectiveGuardrail(ctx, wardID)
		if err != nil {
			return nil, err
		}
		blocks = effective.Weekdays != nil && len(effective.Weekdays) == 0 || effective.Categories != nil && len(effective.Categories) == 0 || effective.Earliest != nil && effective.Latest != nil && *effective.Earliest > *effective.Latest
	}
	return map[string]any{"relationship": relationship, "consent_active": consent, "requires_consent": requires, "can_see_manifest": true, "can_get_arrival_pings": child, "can_observe_messaging": child && consent, "can_grant_consent": requires && s.Accounts.Config.AllowMinorOnboarding && can == nil, "can_set_guardrails": child, "guardrail_supervised_only": child && supervised, "guardrail_latest_start_hour": accountOptionalInt(latest), "guardrail_max_open_joins": accountOptionalInt(cap), "guardrail_allowed_weekdays": weekdays, "guardrail_allowed_weekday_ints": days, "guardrail_earliest_start_hour": accountOptionalInt(earliest), "guardrail_allowed_categories": categories, "guardrail_combined_blocks_all": blocks}, nil
}
func durationText(seconds int) string {
	for _, unit := range []struct {
		size int
		name string
	}{{86400, "day"}, {3600, "hour"}, {60, "minute"}} {
		if seconds%unit.size == 0 {
			n := seconds / unit.size
			suffix := ""
			if n != 1 {
				suffix = "s"
			}
			return fmt.Sprintf("%d %s%s", n, unit.name, suffix)
		}
	}
	return fmt.Sprintf("%d seconds", seconds)
}
func (s *Server) accountRetention(a platform.Actor, provenance map[string]any) []map[string]string {
	config := s.Config.AccountRetention
	if config.ArrivalHours <= 0 {
		config.ArrivalHours = 6
	}
	floor := config.AdultPhotoMinimumSeconds
	if floor <= 0 {
		floor = 3600
	}
	if a.Cohort == "child" || a.Cohort == "teen" {
		floor = config.MinorPhotoMinimumSeconds
		if floor <= 0 {
			floor = 86400
		}
	}
	msg := "Kept until you delete them — there is no automatic deletion. You can set a per-conversation disappearing timer to remove them sooner."
	if config.MessagingDays > 0 {
		msg = fmt.Sprintf("Automatically deleted %d days after they're sent. You can also set a shorter per-conversation disappearing timer.", config.MessagingDays)
	}
	age := "Your current age check has no set expiry. We store an age band only, never a date of birth."
	if expiry, ok := provenance["expires_at"].(time.Time); ok {
		age = "Your current age check expires on " + expiry.Format("02 Jan 2006") + "; after that you re-verify (we still never store your date of birth)."
	}
	return []map[string]string{{"category": "Guardian invitations", "ttl_description": "Deleted " + durationText(int(s.Accounts.Config.GuardianInviteTTL.Seconds())) + " after they're sent (an accepted one's guardian link lives on as a separate record)."}, {"category": "Device app access", "ttl_description": "A device's sign-in is automatically revoked " + durationText(int(s.Accounts.Config.APITokenTTL.Seconds())) + " after it's granted."}, {"category": "Arrival / on-my-way status", "ttl_description": fmt.Sprintf("Cleared about %d hours after a meetup starts — it never becomes a lasting record of where you were.", config.ArrivalHours)}, {"category": "Disappearing photos", "ttl_description": "A photo you mark as disappearing is removed after your chosen timer (at least " + durationText(floor) + ")."}, {"category": "Private (encrypted) messages", "ttl_description": msg}, {"category": "Age verification", "ttl_description": age}}
}
func accountOptionalInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
func (s *Server) accountErasurePreview(ctx context.Context, user int64) (map[string]any, error) {
	fields := []struct{ key, table, column string }{{"memberships", "social_membership", "user_id"}, {"owned_activities", "social_activity", "owner_id"}, {"owned_groups", "social_group", "owner_id"}, {"group_memberships", "social_groupmembership", "user_id"}, {"thread_posts", "social_post", "author_id"}, {"messages_sent", "messaging_message", "sender_id"}, {"photos", "media_photo", "uploader_id"}, {"attachments", "media_attachment", "uploader_id"}, {"age_assurance_records", "accounts_ageassurance", "user_id"}, {"parental_consents", "accounts_parentalconsent", "minor_id"}}
	destroyed := map[string]int64{}
	for _, field := range fields {
		var count int64
		if err := s.DB.QueryRow(ctx, "SELECT count(*) FROM "+field.table+" WHERE "+field.column+"=$1", user).Scan(&count); err != nil {
			return nil, err
		}
		destroyed[field.key] = count
	}
	var links, donations, audits int64
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts_guardianrelationship WHERE guardian_id=$1)+(SELECT count(*) FROM accounts_guardianrelationship WHERE ward_id=$1),(SELECT count(*) FROM donations_donation WHERE donor_id=$1),(SELECT count(*) FROM safety_auditlog WHERE actor_ref=$1)`, user).Scan(&links, &donations, &audits); err != nil {
		return nil, err
	}
	destroyed["guardian_links"] = links
	return map[string]any{"destroyed": destroyed, "retained": map[string]int64{"donations_anonymised": donations, "audit_entries_retained": audits}}, nil
}

var accountNotificationReasons = map[string]string{
	"join_requested":      "Someone asked to join an activity you organise.",
	"join_approved":       "You were admitted to an activity.",
	"event_reminder":      "An activity you joined is starting soon.",
	"activity_cancelled":  "An activity you joined was cancelled.",
	"activity_updated":    "An activity you joined changed.",
	"announcement":        "An organiser posted an announcement in an activity you joined.",
	"group_announcement":  "An organiser posted an announcement in a group you're a member of.",
	"meetup_confirmed":    "A meetup you joined reached the organiser's minimum number of people going.",
	"arrival":             "Someone arrived at or is on their way to a meetup you're part of, or that someone you look after joined.",
	"connection_request":  "Someone you've shared an activity with asked to connect.",
	"connection_accepted": "Someone accepted your connection request.",
	"mention":             "Someone @mentioned you in an activity thread and chose to notify you (you can turn these off).",
	"organizer_role":      "An organiser of an activity made you a co-organiser, removed that role, or handed the activity over to you.",
	"activity_match":      "A new activity matched a search you saved (you can turn these off).",
	"gauge_match":         "A new interest gauge matched a search you saved (you can turn these off).",
	"group_question":      "A member of an under-18 group you organise sent you one of a fixed set of questions (you can turn these off).",
	"interest_converted":  "A meetup you signalled interest in became a real activity (you can turn these off).",
	"rsvp_nudge":          "A meetup you joined is coming up and you haven't said whether you're coming (you can turn these off).",
	"organizer_prep":      "A meetup you organise is coming up and still has no meeting point (you can turn these off).",
	"supervisor_needed":   "A child you look after is organising a meetup that needs an adult to supervise (you can turn these off).",
	"formative_note":      "A few members quietly flagged one of your posts as not quite fitting; we tell you gently, once, with no names or numbers.",
	"mod_alert":           "You are a moderator: the safety sensors or the concern queue need a human look.",
	"moderation":          "A moderation decision affected your content or account (you cannot turn these off).",
	"system":              "An important account or safety notice (you cannot turn these off).",
}
var accountNotificationOrder = []string{"join_requested", "join_approved", "event_reminder", "activity_cancelled", "activity_updated", "announcement", "group_announcement", "meetup_confirmed", "arrival", "connection_request", "connection_accepted", "mention", "organizer_role", "activity_match", "gauge_match", "group_question", "interest_converted", "rsvp_nudge", "organizer_prep", "supervisor_needed", "formative_note", "mod_alert"}
var accountNotificationLabels = map[string]string{"join_requested": "Join requested", "join_approved": "Join approved", "event_reminder": "Event reminder", "activity_cancelled": "Activity cancelled", "activity_updated": "Activity updated", "announcement": "Organizer announcement", "group_announcement": "Group announcement", "meetup_confirmed": "Meetup confirmed", "arrival": "Arrival", "connection_request": "Connection request", "connection_accepted": "Connection accepted", "mention": "Mention", "organizer_role": "Organizer role changed", "activity_match": "Saved-search match", "gauge_match": "Saved-search gauge match", "group_question": "Group question", "interest_converted": "Gauge converted", "rsvp_nudge": "RSVP nudge", "organizer_prep": "Organizer prep reminder", "supervisor_needed": "Supervisor needed", "formative_note": "Formative note", "mod_alert": "Moderator alert"}

func accountRowID(value any) int64 { id, _ := strconv.ParseInt(fmt.Sprint(value), 10, 64); return id }
