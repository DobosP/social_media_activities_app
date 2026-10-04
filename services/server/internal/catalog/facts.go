package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func (s *Service) rateTransaction(ctx context.Context, a platform.Actor, action string, limit int, window time.Duration, f func(pgx.Tx, func() error) error) error {
	return budgets.Reserve(func(reserve func() error) error {
		return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error { return f(tx, reserve) })
	}, func() (budgets.Decision, error) {
		policy, err := budgets.Resolve(s.RatePolicies, action, budgets.Policy{Limit: limit, Window: window})
		if err != nil {
			return budgets.Decision{}, err
		}
		return s.Budgets.Actor(ctx, a.ID, "catalog."+action, policy)
	})
}

func publicVenue(ctx context.Context, q platform.Querier, id int64) error {
	var current int64
	return q.QueryRow(ctx, `SELECT p.id FROM places_place p WHERE p.id=$1 AND `+PublicPlaceSQL, id).Scan(&current)
}
func safeExternal(value string) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return value
	}
	return ""
}

type AccessPreference struct {
	StepFree    bool `json:"needs_step_free"`
	Toilet      bool `json:"needs_accessible_toilet"`
	HearingLoop bool `json:"needs_hearing_loop"`
	Quiet       bool `json:"prefers_quiet"`
}

func tristate(raw any, limited bool) string {
	value, ok := raw.(string)
	if !ok {
		return "unknown"
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes":
		return "true"
	case "no":
		return "false"
	case "limited":
		if limited {
			return "limited"
		}
	}
	return "unknown"
}
func AccessibilityFacts(tags map[string]any) map[string]string {
	return map[string]string{"step_free": tristate(tags["wheelchair"], true), "accessible_toilet": tristate(tags["toilets:wheelchair"], false), "changing_table": tristate(tags["changing_table"], false), "tactile_paving": tristate(tags["tactile_paving"], false), "hearing_loop": tristate(tags["hearing_loop"], false), "automatic_door": tristate(tags["automatic_door"], false)}
}
func MatchesAccess(facts map[string]string, pref *AccessPreference) string {
	if pref == nil {
		return "unknown"
	}
	needs := []string{}
	if pref.StepFree {
		needs = append(needs, "step_free")
	}
	if pref.Toilet {
		needs = append(needs, "accessible_toilet")
	}
	if pref.HearingLoop {
		needs = append(needs, "hearing_loop")
	}
	if len(needs) == 0 {
		return "unknown"
	}
	unknown := false
	for _, key := range needs {
		if facts[key] == "false" {
			return "mismatch"
		}
		if facts[key] != "true" {
			unknown = true
		}
	}
	if unknown {
		return "unknown"
	}
	return "match"
}
func (s *Service) Access(ctx context.Context, a platform.Actor) (*AccessPreference, error) {
	if a.ID <= 0 {
		return nil, nil
	}
	var pref AccessPreference
	err := s.DB.QueryRow(ctx, `SELECT needs_step_free,needs_accessible_toilet,needs_hearing_loop,prefers_quiet FROM places_accesspreference WHERE user_id=$1`, a.ID).Scan(&pref.StepFree, &pref.Toilet, &pref.HearingLoop, &pref.Quiet)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &pref, err
}
func (s *Service) SetAccess(ctx context.Context, a platform.Actor, pref AccessPreference) error {
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if a.ID < 1 || !a.IsActive {
			return platform.ErrForbidden
		}
		if _, err := tx.Exec(ctx, `INSERT INTO places_accesspreference(user_id,needs_step_free,needs_accessible_toilet,needs_hearing_loop,prefers_quiet,created_at,updated_at) VALUES($1,$2,$3,$4,$5,now(),now()) ON CONFLICT(user_id) DO UPDATE SET needs_step_free=EXCLUDED.needs_step_free,needs_accessible_toilet=EXCLUDED.needs_accessible_toilet,needs_hearing_loop=EXCLUDED.needs_hearing_loop,prefers_quiet=EXCLUDED.prefers_quiet,updated_at=now()`, a.ID, pref.StepFree, pref.Toilet, pref.HearingLoop, pref.Quiet); err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, a, "places.access_preference_updated", fmt.Sprintf("accounts.user:%d", a.ID), nil)
	})
}

var FactKeys = []string{"drinking_water", "toilets", "lit_at_night", "indoor_shelter", "fenced", "shade", "playground", "bike_parking", "car_parking", "bus_tram_nearby"}
var factLabels = map[string]string{"drinking_water": "Drinking water", "toilets": "Toilets", "lit_at_night": "Lit at night", "playground": "Playground nearby", "fenced": "Fenced / away from traffic", "shade": "Shade", "indoor_shelter": "Indoor shelter", "bike_parking": "Bike parking", "car_parking": "Car parking", "bus_tram_nearby": "Bus/tram nearby"}

func validFact(key string) bool {
	for _, value := range FactKeys {
		if key == value {
			return true
		}
	}
	return false
}
func osmFact(tags map[string]any, key string) string {
	lookup := map[string]string{"drinking_water": "drinking_water", "toilets": "toilets", "lit_at_night": "lit", "bike_parking": "bicycle_parking", "car_parking": "parking"}
	if rawKey, ok := lookup[key]; ok {
		return tristate(tags[rawKey], false)
	}
	present := map[string][2]string{"playground": {"leisure", "playground"}, "fenced": {"barrier", "fence"}, "shade": {"natural", "tree"}}
	if condition, ok := present[key]; ok && tags[condition[0]] == condition[1] {
		return "true"
	}
	return "unknown"
}
func (s *Service) VenueFacts(ctx context.Context, a platform.Actor, id int64, detail bool) ([]map[string]any, error) {
	if err := publicVenue(ctx, s.DB, id); err != nil {
		return nil, err
	}
	var raw []byte
	if err := s.DB.QueryRow(ctx, `SELECT raw_tags FROM places_place WHERE id=$1`, id).Scan(&raw); err != nil {
		return nil, err
	}
	tags := map[string]any{}
	_ = json.Unmarshal(raw, &tags)
	rows, err := s.DB.Query(ctx, `SELECT fact_key,COUNT(*) FILTER(WHERE value),COUNT(*) FILTER(WHERE NOT value),bool_or(value) FILTER(WHERE user_id=$2),COUNT(*) FILTER(WHERE user_id=$2) FROM places_placefactvote WHERE place_id=$1 GROUP BY fact_key`, id, a.ID)
	if err != nil {
		return nil, err
	}
	type votes struct {
		Yes, No int
		Mine    *bool
	}
	tallies := map[string]votes{}
	for rows.Next() {
		var key string
		var vote votes
		var n int
		if err := rows.Scan(&key, &vote.Yes, &vote.No, &vote.Mine, &n); err != nil {
			rows.Close()
			return nil, err
		}
		if n == 0 {
			vote.Mine = nil
		}
		tallies[key] = vote
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, key := range FactKeys {
		v := tallies[key]
		state := osmFact(tags, key)
		sourced := state != "unknown"
		if !sourced && max(v.Yes, v.No) >= 3 && v.Yes != v.No {
			state = "false"
			if v.Yes > v.No {
				state = "true"
			}
		}
		row := map[string]any{"key": key, "label": factLabels[key], "state": state}
		if detail {
			row["yes"] = v.Yes
			row["no"] = v.No
			row["required"] = 3
			row["my_vote"] = v.Mine
			row["osm_sourced"] = sourced
		}
		out = append(out, row)
	}
	return out, nil
}
func (s *Service) VoteFact(ctx context.Context, a platform.Actor, id int64, key string, value bool) error {
	if !validFact(key) {
		return platform.ErrInvalid
	}
	err := s.rateTransaction(ctx, a, "place_fact_vote", 40, time.Hour, func(tx pgx.Tx, reserve func() error) error {
		if err := platform.Participate(ctx, tx, a); err != nil {
			return err
		}
		if err := publicVenue(ctx, tx, id); err != nil {
			return err
		}
		if err := reserve(); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO places_placefactvote(place_id,user_id,fact_key,value,created_at,updated_at) VALUES($1,$2,$3,$4,now(),now()) ON CONFLICT(place_id,user_id,fact_key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, id, a.ID, key, value); err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, a, "place.fact_voted", fmt.Sprintf("places.place:%d", id), map[string]any{"fact": key, "value": value})
	})
	if errors.Is(err, budgets.ErrDenied) {
		return platform.ErrInvalid
	}
	return err
}

func (s *Service) VoteEdge(ctx context.Context, a platform.Actor, id int64, vote string) error {
	if vote != "confirm" && vote != "dispute" {
		return platform.ErrInvalid
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if err := platform.Participate(ctx, tx, a); err != nil {
			return err
		}
		var place int64
		var origin string
		var disputed bool
		if err := tx.QueryRow(ctx, `SELECT place_id,origin,is_disputed FROM places_placeactivity WHERE id=$1 FOR UPDATE`, id).Scan(&place, &origin, &disputed); err != nil {
			return err
		}
		if err := publicVenue(ctx, tx, place); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO places_activityedgevote(edge_id,user_id,vote,created_at) VALUES($1,$2,$3,now()) ON CONFLICT(edge_id,user_id) DO UPDATE SET vote=EXCLUDED.vote`, id, a.ID, vote); err != nil {
			return err
		}
		var confirms, disputes int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FILTER(WHERE vote='confirm'),COUNT(*) FILTER(WHERE vote='dispute') FROM places_activityedgevote WHERE edge_id=$1`, id).Scan(&confirms, &disputes); err != nil {
			return err
		}
		if origin == "inferred" {
			if disputes >= 3 && !disputed {
				if _, err := tx.Exec(ctx, `UPDATE places_placeactivity SET is_disputed=true,updated_at=now() WHERE id=$1`, id); err != nil {
					return err
				}
			} else if confirms >= 3 {
				if _, err := tx.Exec(ctx, `UPDATE places_placeactivity SET origin='confirmed',is_disputed=false,updated_at=now() WHERE id=$1`, id); err != nil {
					return err
				}
			}
		}
		return platform.RecordAudit(ctx, tx, a, "place.edge_voted", fmt.Sprintf("places.place:%d", place), map[string]string{"vote": vote})
	})
}
