package social

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"
)

func normalizeName(v string) string {
	var out strings.Builder
	space := true
	for _, c := range norm.NFKD.String(strings.ToLower(v)) {
		if unicode.Is(unicode.Mn, c) {
			continue
		}
		if unicode.IsLetter(c) || unicode.IsNumber(c) || c == '_' {
			out.WriteRune(c)
			space = false
		} else if !space {
			out.WriteRune(' ')
			space = true
		}
	}
	return strings.TrimSpace(out.String())
}

// sequenceRatio preserves difflib.SequenceMatcher's longest-block, stable-tie
// matching and popular-character handling for the existing conservative dedup.
func sequenceRatio(left, right string) float64 {
	a, b := []rune(normalizeName(left)), []rune(normalizeName(right))
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	indices := map[rune][]int{}
	for j, c := range b {
		indices[c] = append(indices[c], j)
	}
	if len(b) >= 200 {
		for c, positions := range indices {
			if len(positions) > len(b)/100+1 {
				delete(indices, c)
			}
		}
	}
	type window struct{ alo, ahi, blo, bhi int }
	stack := []window{{0, len(a), 0, len(b)}}
	matched := 0
	for len(stack) > 0 {
		w := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		bi, bj, best := w.alo, w.blo, 0
		previous := map[int]int{}
		for i := w.alo; i < w.ahi; i++ {
			current := map[int]int{}
			for _, j := range indices[a[i]] {
				if j < w.blo {
					continue
				}
				if j >= w.bhi {
					break
				}
				k := previous[j-1] + 1
				current[j] = k
				if k > best {
					bi, bj, best = i-k+1, j-k+1, k
				}
			}
			previous = current
		}
		for bi > w.alo && bj > w.blo && a[bi-1] == b[bj-1] {
			bi--
			bj--
			best++
		}
		for bi+best < w.ahi && bj+best < w.bhi && a[bi+best] == b[bj+best] {
			best++
		}
		if best == 0 {
			continue
		}
		matched += best
		if w.alo < bi && w.blo < bj {
			stack = append(stack, window{w.alo, bi, w.blo, bj})
		}
		if bi+best < w.ahi && bj+best < w.bhi {
			stack = append(stack, window{bi + best, w.ahi, bj + best, w.bhi})
		}
	}
	return 2 * float64(matched) / float64(len(a)+len(b))
}

type DuplicatePlace struct {
	ID   int64
	Name string
	Soft bool
}

func (d *DuplicatePlace) Error() string { return "A nearby venue already exists." }

type PlaceProposalInput struct {
	Name         string  `json:"name"`
	Lon          float64 `json:"lon"`
	Lat          float64 `json:"lat"`
	ActivityType int64   `json:"activity_type"`
	AllowNearby  bool    `json:"allow_nearby"`
}

const proposalColumns = `jsonb_build_object('id',pp.id,'place_id',pp.place_id,'place_name',p.name,'lon',ST_X(p.location::geometry),'lat',ST_Y(p.location::geometry),'status',pp.status,'required_confirmations',pp.required_confirmations,'confirmations_count',(SELECT COUNT(*) FROM social_placeconfirmation pc WHERE pc.proposal_id=pp.id),'created_at',pp.created_at)`

func (s *Service) Proposal(ctx context.Context, id int64) (json.RawMessage, error) {
	return object(ctx, s.DB, `SELECT `+proposalColumns+` FROM social_userplaceproposal pp JOIN places_place p ON p.id=pp.place_id WHERE pp.id=$1`, id)
}
func (s *Service) listProposals(ctx context.Context, a Actor, limit, offset int) ([]json.RawMessage, error) {
	return objects(ctx, s.DB, `SELECT `+proposalColumns+` FROM social_userplaceproposal pp JOIN places_place p ON p.id=pp.place_id WHERE pp.status='pending' AND pp.proposer_id<>$1 ORDER BY pp.created_at,pp.id LIMIT $2 OFFSET $3`, a.ID, min(limit, 201), offset)
}
func (s *Service) ProposePlace(ctx context.Context, a Actor, in PlaceProposalInput) (int64, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 255 || in.ActivityType <= 0 || math.IsNaN(in.Lat) || math.IsNaN(in.Lon) || math.IsInf(in.Lat, 0) || math.IsInf(in.Lon, 0) || in.Lat < -90 || in.Lat > 90 || in.Lon < -180 || in.Lon > 180 {
		return 0, platform.ErrInvalid
	}
	var id int64
	err := s.transaction(ctx, a, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(683475951215)`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,name,ST_Distance(location,ST_SetSRID(ST_MakePoint($1,$2),4326)::geography) FROM places_place WHERE ST_DWithin(location,ST_SetSRID(ST_MakePoint($1,$2),4326)::geography,60) ORDER BY ST_Distance(location,ST_SetSRID(ST_MakePoint($1,$2),4326)::geography) LIMIT 50`, in.Lon, in.Lat)
		if err != nil {
			return err
		}
		var duplicate *DuplicatePlace
		for rows.Next() {
			var pid int64
			var name string
			var distance float64
			if err := rows.Scan(&pid, &name, &distance); err != nil {
				rows.Close()
				return err
			}
			if sequenceRatio(in.Name, name) >= 0.82 {
				duplicate = &DuplicatePlace{pid, name, false}
				break
			}
			if distance <= 25 && !in.AllowNearby && duplicate == nil {
				duplicate = &DuplicatePlace{pid, name, true}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if duplicate != nil {
			return duplicate
		}
		ok, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM taxonomy_activitytype WHERE id=$1)`, in.ActivityType)
		if err := errorIfFalse(ok, err); err != nil {
			return err
		}
		var pid int64
		err = tx.QueryRow(ctx, `INSERT INTO places_place(name,location,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,source,osm_type,osm_id,external_id,raw_tags,first_seen_at,last_seen_at,phone,website,attribution,license_name,provenance_url) VALUES($1,ST_SetSRID(ST_MakePoint($2,$3),4326),'','','','','','','{}','user','',NULL,'','{}',now(),now(),'','','','','') RETURNING id`, in.Name, in.Lon, in.Lat).Scan(&pid)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO places_placeactivity(place_id,activity_id,origin,confidence,source,mapping_rule,is_disputed,created_at,updated_at) VALUES($1,$2,'manual',1,'user','',false,now(),now())`, pid, in.ActivityType); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO social_userplaceproposal(place_id,proposer_id,required_confirmations,status,created_at,published_at) VALUES($1,$2,3,'pending',now(),NULL) RETURNING id`, pid, a.ID).Scan(&id)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "place.proposed", "userplaceproposal", id, nil)
	})
	return id, err
}
func (s *Service) ConfirmPlace(ctx context.Context, a Actor, id int64) error {
	return s.transaction(ctx, a, func(tx pgx.Tx) error {
		var proposer int64
		var state string
		var required int
		if err := tx.QueryRow(ctx, `SELECT proposer_id,status,required_confirmations FROM social_userplaceproposal WHERE id=$1 FOR UPDATE`, id).Scan(&proposer, &state, &required); err != nil {
			return err
		}
		if proposer == a.ID || state != "pending" {
			return platform.ErrInvalid
		}
		if _, err := tx.Exec(ctx, `INSERT INTO social_placeconfirmation(proposal_id,user_id,created_at) VALUES($1,$2,now()) ON CONFLICT(proposal_id,user_id) DO NOTHING`, id, a.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE social_userplaceproposal pp SET status='published',published_at=now() WHERE pp.id=$1 AND (SELECT COUNT(*) FROM social_placeconfirmation WHERE proposal_id=pp.id)>=$2`, id, required); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "place.confirmed", "userplaceproposal", id, nil)
	})
}
func (s *Service) StaffProposal(ctx context.Context, a Actor, id int64, publish bool, reason string) error {
	if !a.IsStaff {
		return platform.ErrForbidden
	}
	return s.privacyTransaction(ctx, a, func(tx pgx.Tx) error {
		var place int64
		err := tx.QueryRow(ctx, `SELECT place_id FROM social_userplaceproposal WHERE id=$1 AND status='pending' FOR UPDATE`, id).Scan(&place)
		if err != nil {
			return err
		}
		state, event := "rejected", "place.staff_rejected"
		if publish {
			state, event = "published", "place.staff_published"
		}
		if _, err := tx.Exec(ctx, `UPDATE social_userplaceproposal SET status=$2::text,published_at=CASE WHEN $2::text='published' THEN now() ELSE published_at END WHERE id=$1`, id, state); err != nil {
			return err
		}
		return s.Audit(ctx, tx, a, event, fmt.Sprintf("places.place:%d", place), map[string]string{"reason": preview(reason, 200)})
	})
}
func (s *Service) proposalsList(w http.ResponseWriter, r *http.Request, a Actor) {
	value, err := s.cursorRows(r, 200, func(ctx context.Context, limit, offset int) ([]json.RawMessage, error) {
		return s.listProposals(ctx, a, limit, offset)
	})
	response(w, value, err, 200)
}
func (s *Service) proposalCreate(w http.ResponseWriter, r *http.Request, a Actor) {
	var in PlaceProposalInput
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	id, err := s.ProposePlace(r.Context(), a, in)
	if d, ok := err.(*DuplicatePlace); ok {
		body := map[string]any{"detail": d.Error(), "soft": d.Soft, "duplicate_place_name": d.Name}
		if !d.Soft {
			body["duplicate_place_id"] = d.ID
		}
		platform.JSON(w, 400, body)
		return
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Proposal(r.Context(), id)
	response(w, v, err, 201)
}
func (s *Service) proposalConfirm(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err := s.ConfirmPlace(r.Context(), a, id); err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Proposal(r.Context(), id)
	response(w, v, err, 200)
}
