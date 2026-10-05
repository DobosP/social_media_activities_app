// Package admin supplies native, audited operator workflows. It does not expose
// generic SQL editing or credentials, and cannot turn an unverified account or
// missing parental consent into product participation.
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB      *pgxpool.Pool
	Catalog *catalog.Service
	Social  *social.Service
	Safety  *safety.Service
	Media   *media.Service
}

func New(db *pgxpool.Pool, cat *catalog.Service, soc *social.Service, safe *safety.Service, med *media.Service) *Service {
	return &Service{db, cat, soc, safe, med}
}

type Model struct {
	Name, Table, Fields string
	Actions             []string
	Editable            bool
}

var models = map[string]Model{}

func init() {
	add := func(name, table, fields string, actions ...string) {
		models[name] = Model{name, table, "id," + fields, actions, false}
	}
	add("accounts.user", "accounts_user", "username,display_name,role,age_band,cohort,is_identity_verified,is_active,is_staff,is_superuser,public_id,identity_verified_at,last_login,date_joined")
	add("accounts.ageassurance", "accounts_ageassurance", "user_id,age_band,provider,method,verified_at,expires_at")
	add("accounts.parentalconsent", "accounts_parentalconsent", "minor_id,status,guardian_identifier,granted_at,expires_at")
	add("accounts.identitybinding", "accounts_identitybinding", "holder_hash,user_id,created_at,released_at", "release_bindings")
	add("accounts.bannedidentity", "accounts_bannedidentity", "holder_hash,created_at", "lift_bans")
	add("accounts.guardianrelationship", "accounts_guardianrelationship", "guardian_id,ward_id,relationship,status,created_at")
	add("social.activity", "social_activity", "title,cohort,status,owner_id,place_id,starts_at,join_threshold")
	add("social.membership", "social_membership", "activity_id,user_id,role,state,decided_at")
	add("social.joinvote", "social_joinvote", "membership_id,voter_id,approve,created_at")
	add("social.post", "social_post", "thread_id,author_id,created_at,is_hidden,is_author_deleted", "hide_post", "unhide_post")
	add("social.thread", "social_thread", "activity_id,group_id,created_at")
	add("social.placeconfirmation", "social_placeconfirmation", "proposal_id,user_id,created_at")
	add("social.userplaceproposal", "social_userplaceproposal", "place_id,proposer_id,status,required_confirmations,published_at,created_at", "publish_selected", "reject_selected")
	add("places.place", "places_place", "name,source,address_city,osm_type,osm_id,external_id,last_seen_at", "clear_hours_reports", "clear_closure_reports")
	add("places.placeactivity", "places_placeactivity", "place_id,activity_id,origin,is_disputed,confidence,source,mapping_rule", "edge_demote", "edge_restore", "edge_reset")
	add("places.activityedgevote", "places_activityedgevote", "edge_id,user_id,vote,created_at")
	add("places.opennowreport", "places_opennowreport", "place_id,reporter_id,created_at")
	add("places.partner", "places_partner", "name,kind,place_id,is_verified,is_active,website,blurb")
	add("places.placecorrection", "places_placecorrection", "place_id,proposer_id,field,proposed_value,status,created_at,published_at", "publish_corrections", "reject_corrections")
	add("places.childvenueclass", "places_childvenueclass", "key,label,is_active")
	add("places.approvedchildvenue", "places_approvedchildvenue", "place_id,approved_by_id,note,created_at")
	add("places.placeclaim", "places_placeclaim", "place_id,org_name,kind,claimant_id,status,contact_email,cui,evidence,official_website,created_at,decided_by_id,decided_at,partner_id", "approve_claims", "reject_claims")
	add("places.placecover", "places_placecover", "place_id,source,uploaded_by_id,byte_size,updated_at,attribution,license_name,source_page_url", "delete_cover")
	add("events.event", "events_event", "title,starts_at,ends_at,place_id,activity_type_id,source,lifecycle_status,is_import_held,is_tombstone,license_name,attribution,provenance_url")
	add("events.eventfeed", "events_eventfeed", "name,url,is_active,place_id,activity_type_id,last_synced_at,last_status,created_at")
	add("events.eventreport", "events_eventreport", "event_id,kind,reporter_id,created_at", "clear_event_reports")
	add("events.roedueventsyncstate", "events_roedueventsyncstate", "pack_id,city,snapshot_id,release_id,snapshot_generated_at,completed_at")
	add("taxonomy.activitycategory", "taxonomy_activitycategory", "name,slug,parent_id,description")
	add("taxonomy.activitytype", "taxonomy_activitytype", "name,slug,category_id,parent_id,is_active,aliases,wellness,family_friendly")
	add("taxonomy.activityrelation", "taxonomy_activityrelation", "source_id,kind,target_id,symmetric,note")
	add("safety.report", "safety_report", "reason,status,target_type_id,target_id,reporter_id,created_at,resolution,handled_by_id,handled_at", "mark_reviewing", "dismiss", "ban_target")
	add("safety.moderationaction", "safety_moderationaction", "action,reason,target_type_id,target_id,moderator_id,created_at")
	add("safety.moderationappeal", "safety_moderationappeal", "action_id,status,appellant_id,statement,created_at,decided_at,decided_by_id", "uphold", "overturn")
	add("safety.block", "safety_block", "blocker_id,blocked_id,created_at")
	add("safety.authorityreferral", "safety_authorityreferral", "authority,reason,subject_ref,referred_by_id,created_at,reference,report_id,audit_anchor_hash,notes")
	add("safety.auditlog", "safety_auditlog", "event,actor_id,target_ref,created_at,data,prev_hash,hash")
	add("media.photo", "media_photo", "kind,uploader_id,thread_id,scan_status,byte_size,created_at")
	add("media.attachment", "media_attachment", "kind,uploader_id,post_id,content_type,byte_size,created_at")
	add("media.activitycover", "media_activitycover", "activity_id,uploader_id,content_type,byte_size,created_at")
	add("donations.donation", "donations_donation", "amount_cents,currency,provider,status,donor_id,campaign_id,created_at,completed_at")
	add("donations.campaign", "donations_campaign", "title,slug,goal_cents,currency,is_active,partner_id,closed_at,created,outcome")
	add("donations.spendentry", "donations_spendentry", "category,amount_cents,currency,period,campaign_id,created_at,note")
	add("donations.costanchor", "donations_costanchor", "label,amount_cents,currency,spend_category,is_active,created_at")
	add("donations.inkindcontribution", "donations_inkindcontribution", "category,quantity,unit_text,value_cents,currency,period,partner_id,created_at,note")
	add("donations.civicoutcome", "donations_civicoutcome", "headline,detail,period,partner_id,is_active,created_at")
	add("booking.placebookinginfo", "booking_placebookinginfo", "place_id,provider,deep_link")
	add("booking.booking", "booking_booking", "user_id,place_id,provider,status,starts_at")
	add("communities.area", "communities_area", "name,slug,city,derive_method,min_radius_m,is_active,created_at")
	add("communities.community", "communities_community", "name,slug,cohort,tier,area_id,is_published,last_evaluated_at,created_at")
	add("messaging.publickey", "messaging_publickey", "user_id,algorithm,active,created_at,key_id")
	add("messaging.conversation", "messaging_conversation", "kind,cohort,title,creator_id,created_at,updated_at")
	add("messaging.message", "messaging_message", "conversation_id,sender_id,algorithm,created_at")
	add("messaging.messagekey", "messaging_messagekey", "message_id,recipient_id,created_at")
	add("notifications.notification", "notifications_notification", "kind,recipient_id,title,read_at,created_at")
	for name := range curatedFields {
		m := models[name]
		m.Editable = true
		models[name] = m
	}
	m := models["events.event"]
	m.Actions = []string{"hold_event", "release_event"}
	m.Editable = true
	models["events.event"] = m
}

// Gate admits only an active staff superuser (ADR-0035, 2026-10-05). Incoming
// flags are never proof; all three are re-read from the account row.
func (s *Service) Gate(ctx context.Context, a platform.Actor) error {
	if a.ID < 1 || !a.IsActive || !a.IsStaff || !a.IsSuperuser {
		return platform.ErrForbidden
	}
	var active, staff, superuser bool
	if err := s.DB.QueryRow(ctx, `SELECT is_active,is_staff,is_superuser FROM accounts_user WHERE id=$1`, a.ID).Scan(&active, &staff, &superuser); err != nil {
		return err
	}
	if !active || !staff || !superuser {
		return platform.ErrForbidden
	}
	return nil
}

// gateTx orders owned console writes against permission revocation. The actor
// row remains locked until both the domain mutation and audit have committed.
func (s *Service) gateTx(ctx context.Context, tx pgx.Tx, a platform.Actor) error {
	var active, staff, superuser bool
	if err := tx.QueryRow(ctx, `SELECT is_active,is_staff,is_superuser FROM accounts_user WHERE id=$1 FOR UPDATE`, a.ID).Scan(&active, &staff, &superuser); err != nil {
		return err
	}
	if !active || !staff || !superuser {
		return platform.ErrForbidden
	}
	return nil
}
func (s *Service) Models(ctx context.Context, a platform.Actor) ([]Model, error) {
	if err := s.Gate(ctx, a); err != nil {
		return nil, err
	}
	out := []Model{}
	for _, m := range models {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (s *Service) List(ctx context.Context, a platform.Actor, model string, limit, offset int) ([]json.RawMessage, error) {
	if err := s.Gate(ctx, a); err != nil {
		return nil, err
	}
	m, ok := models[model]
	if !ok {
		return nil, platform.ErrNotFound
	}
	fields := []string{}
	for _, f := range strings.Split(m.Fields, ",") {
		fields = append(fields, "'"+f+"',"+pgx.Identifier{f}.Sanitize())
	}
	rows, err := s.DB.Query(ctx, `SELECT jsonb_build_object(`+strings.Join(fields, ",")+`) FROM `+pgx.Identifier{m.Table}.Sanitize()+` ORDER BY id DESC LIMIT $1 OFFSET $2`, max(1, min(limit, 200)), max(0, min(offset, 1000000)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw json.RawMessage
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, rows.Err()
}

type ActionResult struct {
	ID      int64  `json:"id"`
	Applied bool   `json:"applied"`
	Error   string `json:"error,omitempty"`
}

func (s *Service) Execute(ctx context.Context, a platform.Actor, model, action string, ids []int64, reason string) ([]ActionResult, error) {
	if err := s.Gate(ctx, a); err != nil {
		return nil, err
	}
	m, ok := models[model]
	if !ok {
		return nil, platform.ErrNotFound
	}
	valid := false
	for _, v := range m.Actions {
		valid = valid || v == action
	}
	if !valid || len(ids) == 0 || len(ids) > 200 || len([]rune(reason)) > 2000 {
		return nil, platform.ErrInvalid
	}
	results := []ActionResult{}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id < 1 || seen[id] {
			return nil, platform.ErrInvalid
		}
		seen[id] = true
		err := s.executeOne(ctx, a, model, action, id, reason)
		result := ActionResult{ID: id, Applied: err == nil}
		if err != nil {
			result.Error = "Action could not be applied to this record."
		}
		results = append(results, result)
	}
	return results, nil
}
func (s *Service) executeOne(ctx context.Context, a platform.Actor, model, action string, id int64, reason string) error {
	switch model {
	case "events.event":
		return s.ReviewEvent(ctx, a, id, action == "release_event", reason)
	case "social.userplaceproposal":
		return s.Social.StaffProposal(ctx, a, id, action == "publish_selected", reason)
	case "places.placecorrection":
		return s.Catalog.StaffCorrection(ctx, a, id, action == "publish_corrections", reason)
	case "places.placeclaim":
		return s.Catalog.DecideClaim(ctx, a, id, action == "approve_claims", reason)
	case "places.placeactivity":
		return s.Catalog.ReverseEdge(ctx, a, id, strings.TrimPrefix(action, "edge_"))
	case "places.place":
		_, err := s.Catalog.ClearVenueReports(ctx, a, id, action == "clear_closure_reports")
		return err
	case "events.eventreport":
		var event int64
		if err := s.DB.QueryRow(ctx, `SELECT event_id FROM events_eventreport WHERE id=$1`, id).Scan(&event); err != nil {
			return err
		}
		_, err := s.Catalog.ClearEventReports(ctx, a, event)
		return err
	case "places.placecover":
		var place int64
		if err := s.DB.QueryRow(ctx, `SELECT place_id FROM places_placecover WHERE id=$1`, id).Scan(&place); err != nil {
			return err
		}
		return s.Media.DeletePlaceCover(ctx, a, place)
	case "safety.moderationappeal":
		_, err := s.Safety.ResolveAppeal(ctx, a, id, action == "overturn", reason)
		return err
	case "safety.report":
		if action == "ban_target" {
			var app, model, reason string
			var target int64
			if err := s.DB.QueryRow(ctx, `SELECT ct.app_label,ct.model,r.target_id,r.reason FROM safety_report r JOIN django_content_type ct ON ct.id=r.target_type_id WHERE r.id=$1`, id).Scan(&app, &model, &target, &reason); err != nil {
				return err
			}
			t, err := s.Safety.ResolveTarget(ctx, s.DB, app, model, target)
			if err != nil {
				return err
			}
			_, err = s.Safety.TakeAction(ctx, a, t, safety.ActionInput{Decision: "ban", Reason: reason, Notes: reason}, id)
			return err
		}
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if err := s.gateTx(ctx, tx, a); err != nil {
			return err
		}
		target := fmt.Sprintf("%s:%d", model, id)
		event := "admin." + action
		switch model {
		case "accounts.identitybinding":
			var user *int64
			var released bool
			if err := tx.QueryRow(ctx, `SELECT user_id,released_at IS NOT NULL FROM accounts_identitybinding WHERE id=$1 FOR UPDATE`, id).Scan(&user, &released); err != nil {
				return err
			}
			if user == nil || released {
				return nil
			}
			if _, err := tx.Exec(ctx, `UPDATE accounts_identitybinding SET released_at=now() WHERE id=$1`, id); err != nil {
				return err
			}
			event = "identity.binding_released"
			target = fmt.Sprintf("accounts.user:%d", *user)
		case "accounts.bannedidentity":
			var hash string
			if err := tx.QueryRow(ctx, `DELETE FROM accounts_bannedidentity WHERE id=$1 RETURNING holder_hash`, id).Scan(&hash); err != nil {
				return err
			}
			return platform.RecordAudit(ctx, tx, a, "identity.ban_released", "", map[string]string{"hash_prefix": hash[:min(12, len(hash))]})
		case "social.post":
			hidden := action == "hide_post"
			var authorDeleted bool
			if err := tx.QueryRow(ctx, `SELECT is_author_deleted FROM social_post WHERE id=$1 FOR UPDATE`, id).Scan(&authorDeleted); err != nil {
				return err
			}
			if !hidden && authorDeleted {
				return platform.ErrForbidden
			}
			if _, err := tx.Exec(ctx, `UPDATE social_post SET is_hidden=$2 WHERE id=$1`, id, hidden); err != nil {
				return err
			}
		case "safety.report":
			var reporter *int64
			if err := tx.QueryRow(ctx, `SELECT reporter_id FROM safety_report WHERE id=$1 FOR UPDATE`, id).Scan(&reporter); err != nil {
				return err
			}
			if action == "mark_reviewing" {
				if _, err := tx.Exec(ctx, `UPDATE safety_report SET status='reviewing' WHERE id=$1`, id); err != nil {
					return err
				}
			} else {
				if _, err := tx.Exec(ctx, `UPDATE safety_report SET status='dismissed',handled_by_id=$2,handled_at=now(),resolution=$3 WHERE id=$1`, id, a.ID, reason); err != nil {
					return err
				}
				event = "report.dismissed"
				if reporter != nil {
					if _, err := platform.Notify(ctx, tx, *reporter, "system", "Your report was reviewed", "Thanks for your report. Our moderation team reviewed it and found no action was needed.", ""); err != nil {
						return err
					}
					rows, err := tx.Query(ctx, `SELECT guardian_id FROM accounts_guardianrelationship gr JOIN accounts_user u ON u.id=gr.ward_id WHERE gr.ward_id=$1 AND gr.status='active' AND u.cohort='child'`, *reporter)
					if err != nil {
						return err
					}
					guardians := []int64{}
					for rows.Next() {
						var guardian int64
						if err = rows.Scan(&guardian); err != nil {
							rows.Close()
							return err
						}
						guardians = append(guardians, guardian)
					}
					err = rows.Err()
					rows.Close()
					if err != nil {
						return err
					}
					for _, g := range guardians {
						if _, err = platform.Notify(ctx, tx, g, "system", "A report involving your ward was reviewed", "You can review the safety record with your ward.", "/wards/"); err != nil {
							return err
						}
					}
				}
			}
		default:
			return platform.ErrInvalid
		}
		return platform.RecordAudit(ctx, tx, a, event, target, map[string]string{"reason": reason})
	})
}
