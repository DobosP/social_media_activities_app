package catalog

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type ClaimInput struct {
	OrgName         string `json:"org_name"`
	Kind            string `json:"kind"`
	OfficialWebsite string `json:"official_website"`
	ContactEmail    string `json:"contact_email"`
	CUI             string `json:"cui"`
	Evidence        string `json:"evidence"`
}

func (s *Service) FileClaim(ctx context.Context, a platform.Actor, placeID int64, in ClaimInput) (int64, error) {
	in.OrgName = strings.TrimSpace(in.OrgName)
	if in.OrgName == "" || utf8.RuneCountInString(in.OrgName) > 255 || utf8.RuneCountInString(in.ContactEmail) > 254 || utf8.RuneCountInString(in.CUI) > 16 || utf8.RuneCountInString(in.Evidence) > 500 {
		return 0, platform.ErrInvalid
	}
	if in.Kind == "" {
		in.Kind = "business"
	}
	if !map[string]bool{"business": true, "ngo": true, "library": true, "school": true, "civic": true, "healthcare": true, "cultural": true, "other": true}[in.Kind] {
		return 0, platform.ErrInvalid
	}
	in.OfficialWebsite = safeExternal(in.OfficialWebsite)
	var id int64
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if a.Cohort != "adult" {
			return platform.ErrForbidden
		}
		if err := platform.Participate(ctx, tx, a); err != nil {
			return err
		}
		if err := s.publicVenue(ctx, tx, placeID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `INSERT INTO places_placeclaim(place_id,claimant_id,org_name,kind,official_website,contact_email,cui,evidence,status,decided_at,created_at,decided_by_id,partner_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',NULL,now(),NULL,NULL) RETURNING id`, placeID, a.ID, in.OrgName, in.Kind, in.OfficialWebsite, in.ContactEmail, strings.TrimSpace(in.CUI), strings.TrimSpace(in.Evidence)).Scan(&id)
		if err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, a, "places.claim_filed", fmt.Sprintf("places.place:%d", placeID), map[string]int64{"claim_id": id})
	})
	return id, err
}
func (s *Service) DecideClaim(ctx context.Context, a platform.Actor, id int64, approve bool, reason string) error {
	if !a.IsStaff || !a.IsActive {
		return platform.ErrForbidden
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var place, user int64
		var name, kind, website, status string
		if err := tx.QueryRow(ctx, `SELECT place_id,claimant_id,org_name,kind,official_website,status FROM places_placeclaim WHERE id=$1 FOR UPDATE`, id).Scan(&place, &user, &name, &kind, &website, &status); err != nil {
			return err
		}
		if status != "pending" {
			return platform.ErrInvalid
		}
		event, next, title, body := "places.claim_rejected", "rejected", "Your venue claim was not approved", "It couldn't be verified."
		var partner *int64
		if approve {
			var locked int64
			if err := tx.QueryRow(ctx, `SELECT id FROM places_place WHERE id=$1 FOR UPDATE`, place).Scan(&locked); err != nil {
				return err
			}
			next, event, title = "approved", "places.claim_approved", "Your venue claim was approved"
			var partnerID int64
			err := tx.QueryRow(ctx, `SELECT id FROM places_partner WHERE place_id=$1 AND name=$2 ORDER BY id LIMIT 1 FOR UPDATE`, place, name).Scan(&partnerID)
			if err == pgx.ErrNoRows {
				err = tx.QueryRow(ctx, `INSERT INTO places_partner(place_id,name,kind,blurb,website,is_verified,is_active,created_at,updated_at) VALUES($1,$2,$3,'',$4,true,true,now(),now()) RETURNING id`, place, name, kind, website).Scan(&partnerID)
			} else if err == nil {
				_, err = tx.Exec(ctx, `UPDATE places_partner SET kind=$2,website=$3,is_verified=true,is_active=true,updated_at=now() WHERE id=$1`, partnerID, kind, website)
			}
			if err != nil {
				return err
			}
			partner = &partnerID
			if _, err := tx.Exec(ctx, `UPDATE places_place SET website=$2,last_seen_at=now() WHERE id=$1 AND website='' AND $2<>''`, place, website); err != nil {
				return err
			}
			var placeName string
			if err := tx.QueryRow(ctx, `SELECT `+placeNameSQL+` FROM places_place p WHERE p.id=$1`, place).Scan(&placeName); err != nil {
				return err
			}
			body = name + " now stewards “" + placeName + "”."
		} else if reason = strings.TrimSpace(reason); reason != "" {
			r := []rune(reason)
			if len(r) > 200 {
				reason = string(r[:200])
			}
			body = "It couldn't be verified: " + reason
		}
		if _, err := tx.Exec(ctx, `UPDATE places_placeclaim SET status=$2,partner_id=$3,decided_by_id=$4,decided_at=now() WHERE id=$1`, id, next, partner, a.ID); err != nil {
			return err
		}
		if err := platform.RecordAudit(ctx, tx, a, event, fmt.Sprintf("places.place:%d", place), map[string]any{"claim_id": id, "reason": reason}); err != nil {
			return err
		}
		_, err := platform.Notify(ctx, tx, user, "system", title, body, fmt.Sprintf("/places/%d/", place))
		return err
	})
}
func (s *Service) Partners(ctx context.Context, placeID *int64, businessOnly bool) ([]map[string]any, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,name,kind,blurb,website,place_id FROM places_partner WHERE is_verified AND is_active AND ($1::bigint IS NULL OR place_id=$1) AND (NOT $2 OR kind='business') ORDER BY name,id LIMIT 200`, placeID, businessOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var place *int64
		var name, kind, blurb, website string
		if err := rows.Scan(&id, &name, &kind, &blurb, &website, &place); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "name": name, "kind": kind, "blurb": blurb, "website": safeExternal(website), "place_id": place})
	}
	return out, rows.Err()
}
