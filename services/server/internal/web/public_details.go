package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/flosch/pongo2/v6"
)

func (s *Server) publicPlaceDetail(r *http.Request, a platform.Actor) (pongo2.Context, error) {
	p, isPublic, err := s.publicPlace(r, a, id(r, "pk"), true)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	place := spaID(p)
	data := pongo2.Context{"user": socialActor(a), "place": p, "place_label": p["name"], "category_chips": p["category_chips"], "open_now": p["open_now"], "place_visual": p["visual"], "attribution_credit": p["attribution_credit"], "share_kind": "place", "share_obj_id": place, "meetups": []any{}, "share_targets": []any{}}
	pref, err := s.Catalog.Access(ctx, a)
	if err != nil {
		return nil, err
	}
	facts := catalog.AccessibilityFacts(spaMap(p["_tags"]))
	data["has_access_pref"], data["access_match"] = pref != nil, catalog.MatchesAccess(facts, pref)
	labels := map[string]string{"step_free": "Step-free access", "accessible_toilet": "Accessible toilet", "changing_table": "Baby changing table", "tactile_paving": "Tactile paving", "hearing_loop": "Hearing loop", "automatic_door": "Automatic door"}
	access, recorded := []map[string]any{}, []map[string]any{}
	for _, key := range []string{"step_free", "accessible_toilet", "changing_table", "tactile_paving", "hearing_loop", "automatic_door"} {
		row := map[string]any{"key": key, "label": labels[key], "state": facts[key]}
		access = append(access, row)
		if facts[key] != "unknown" {
			recorded = append(recorded, row)
		}
	}
	data["access_facts"], data["recorded_access_facts"] = access, recorded
	venue, err := s.publicVenueFacts(r, a, p, isPublic)
	if err != nil {
		return nil, err
	}
	data["venue_facts"] = venue
	recorded = []map[string]any{}
	kid := false
	brief := [][]any{{"Place", p["name"]}}
	if address := spaText(p["address"]); address != "" {
		brief = append(brief, []any{"Where", "It is at " + address + "."})
	}
	for _, row := range append(append([]map[string]any{}, access...), venue...) {
		sentence := map[string]string{"true": "yes", "false": "no", "limited": "limited"}[spaText(row["state"])]
		if sentence == "" {
			sentence = "not recorded"
		}
		brief = append(brief, []any{row["label"], sentence})
	}
	for _, row := range venue {
		if row["state"] != "unknown" {
			recorded = append(recorded, row)
		}
		if row["state"] == "true" && (row["key"] == "fenced" || row["key"] == "baby_changing" || row["key"] == "playground") {
			kid = true
		}
	}
	data["recorded_venue_facts"], data["has_kid_facts"], data["place_brief"] = recorded, kid, brief
	data["can_contribute"] = isPublic && a.ID > 0 && platform.Participate(ctx, s.DB, a) == nil
	edges, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',pa.id,'is_disputed',pa.is_disputed,'activity',jsonb_build_object('id',t.id,'slug',t.slug,'name',t.name),'summary',jsonb_build_object('is_confirmed',pa.origin='confirmed','confirms',(SELECT count(*) FROM places_activityedgevote v WHERE v.edge_id=pa.id AND v.vote='confirm'),'disputes',(SELECT count(*) FROM places_activityedgevote v WHERE v.edge_id=pa.id AND v.vote='dispute'),'required',3,'my_vote',(SELECT v.vote FROM places_activityedgevote v WHERE v.edge_id=pa.id AND v.user_id=$2))) FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id WHERE pa.place_id=$1 AND (NOT pa.is_disputed OR $3) ORDER BY pa.id`, place, a.ID, a.IsStaff && a.IsActive)
	if err != nil {
		return nil, err
	}
	data["edges"] = edges
	corrections, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',c.id,'field',c.field,'field_label',CASE c.field WHEN 'name' THEN 'Name' WHEN 'address' THEN 'Address' ELSE 'Opening hours' END,'proposed_value',c.proposed_value,'confirms',(SELECT count(*) FROM places_placecorrectionconfirmation cc WHERE cc.correction_id=c.id),'required',c.required_confirmations,'is_proposer',c.proposer_id=$2,'confirmed_by_me',EXISTS(SELECT 1 FROM places_placecorrectionconfirmation cc WHERE cc.correction_id=c.id AND cc.user_id=$2)) FROM places_placecorrection c WHERE c.place_id=$1 AND c.status='pending' ORDER BY c.created_at,c.id`, place, a.ID)
	if err != nil {
		return nil, err
	}
	data["corrections"] = corrections
	proposals, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',pp.id,'status',pp.status,'required_confirmations',pp.required_confirmations,'confirmations_count',(SELECT count(*) FROM social_placeconfirmation pc WHERE pc.proposal_id=pp.id)) FROM social_userplaceproposal pp WHERE pp.place_id=$1 AND pp.status<>'published'`, place)
	if err != nil {
		return nil, err
	}
	if len(proposals) > 0 {
		data["pending_proposal"] = proposals[0]
	}
	partners, err := s.Catalog.Partners(ctx, &place, false)
	if err != nil {
		return nil, err
	}
	for _, partner := range partners {
		if partner["kind"] == "business" {
			if data["official_partner"] == nil {
				data["official_partner"] = partner
			}
		}
		if data["partner"] == nil {
			partner["get_kind_display"] = strings.Title(spaText(partner["kind"]))
			data["partner"] = partner
		}
	}
	claims, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',claim.id) FROM places_placeclaim claim JOIN places_partner partner ON partner.id=claim.partner_id WHERE claim.place_id=$1 AND claim.claimant_id=$2 AND claim.status='approved' AND claim.kind='business' AND partner.is_verified AND partner.is_active AND partner.place_id=claim.place_id ORDER BY claim.decided_at DESC,claim.id DESC LIMIT 1`, place, a.ID)
	if err != nil {
		return nil, err
	}
	if len(claims) > 0 {
		data["official_claim"] = claims[0]
	}
	form, err := s.form(r, a, "PlaceOfficialImageForm", map[string]any{})
	if err != nil {
		return nil, err
	}
	data["official_image_form"] = form
	events, err := s.publicEvents(r, "", place, 1000)
	if err != nil {
		return nil, err
	}
	data["events"] = events
	if a.ID > 0 && a.IsActive && a.Cohort != "" && a.Cohort != "unassigned" {
		local, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',a.id) FROM social_activity a JOIN accounts_user u ON u.id=a.owner_id WHERE `+social.ActivityVisibilitySQL()+` AND a.place_id=$3 AND a.status='open' AND a.starts_at>=now() ORDER BY a.starts_at,a.id LIMIT 10000`, a.ID, a.Cohort, place)
		if err != nil {
			return nil, err
		}
		data["meetups"], err = s.HydrateActivities(ctx, a, local)
		if err != nil {
			return nil, err
		}
	}
	data["canonical_url"] = s.publicAbsolute(r, r.URL.Path)
	if isPublic {
		data["canonical_url"] = s.publicAbsolute(r, publicPlacePath(p))
		node := s.publicPlaceNode(r, p)
		node["@context"] = "https://schema.org"
		nested := []any{}
		for _, e := range events[:min(10, len(events))] {
			entry := map[string]any{"@type": "Event", "name": e["title"], "startDate": publicDate(e["starts_at"]), "url": s.publicAbsolute(r, publicEventPath(e))}
			if e["ends_at"] != nil {
				entry["endDate"] = publicDate(e["ends_at"])
			}
			nested = append(nested, entry)
		}
		if len(nested) > 0 {
			node["event"] = nested
		}
		data["structured_data"] = publicLD(node)
		data["breadcrumb_data"] = publicLD(s.publicBreadcrumb(r, []map[string]any{{"name": "Home", "url": "/"}, {"name": "Places", "url": "/places/list/"}, {"name": p["name"], "url": publicPlacePath(p)}}))
		combos, err := s.publicLandingCombos(r)
		if err != nil {
			return nil, err
		}
		links := []any{}
		for _, combo := range combos {
			area := spaMap(combo["area"])
			typ := spaMap(combo["activity"])
			if strings.EqualFold(spaText(area["city"]), spaText(p["city"])) {
				data["related_city"] = map[string]any{"name": area["name"], "url": routeURL("things_to_do_city", area["slug"])}
				for _, edge := range edges {
					if spaMap(edge["activity"])["slug"] == typ["slug"] && !spaBool(edge["is_disputed"]) {
						links = append(links, map[string]any{"name": typ["name"], "url": routeURL("things_to_do", area["slug"], typ["slug"])})
						break
					}
				}
			}
		}
		data["related_landings"] = links
	}
	return data, nil
}

var publicEventModel = ` || jsonb_build_object('created_at',e.created_at,'updated_at',e.updated_at,'activity_type',CASE WHEN t.id IS NULL THEN NULL ELSE jsonb_build_object('id',t.id,'slug',t.slug,'name',t.name,'is_active',t.is_active) END,'place',CASE WHEN p.id IS NULL THEN NULL ELSE (` + catalog.PlaceExportProjectionSQL() + publicPlaceFields + `) END)`

const publicEventsJoin = ` FROM events_event e LEFT JOIN places_place p ON p.id=e.place_id LEFT JOIN taxonomy_activitytype t ON t.id=e.activity_type_id `
const publicUpcomingSQL = `e.starts_at>=now() AND e.lifecycle_status IN('scheduled','rescheduled','sold_out') AND NOT e.is_tombstone AND NOT e.is_import_held`

func (s *Server) publicEvents(r *http.Request, city string, place int64, cap int) ([]map[string]any, error) {
	q := r.URL.Query()
	activity := q.Get("activity")
	search := strings.TrimSpace(q.Get("q"))
	if len([]rune(search)) < 2 {
		search = ""
	}
	search = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search)
	rows, err := socialRows(r.Context(), s.DB, `SELECT `+catalog.PublicEventProjectionSQL()+publicEventModel+publicEventsJoin+` WHERE `+catalog.PolicyFromContext(r.Context()).EventSQL()+` AND `+publicUpcomingSQL+` AND ($1::text='' OR t.slug=$1) AND ($2::bigint=0 OR e.place_id=$2) AND ($3::text='' OR lower(p.address_city)=lower($3)) AND ($4::text='' OR upper(e.title) LIKE '%'||upper($4)||'%' OR upper(e.description) LIKE '%'||upper($4)||'%' OR upper(p.name) LIKE '%'||upper($4)||'%') AND (NOT EXISTS(SELECT 1 FROM communities_area ar WHERE ar.slug=$5) OR EXISTS(SELECT 1 FROM communities_area ar WHERE ar.slug=$5 AND ((ar.derive_method='city' AND lower(p.address_city)=lower(ar.city))))) ORDER BY e.starts_at,e.id LIMIT $6`, activity, place, city, search, q.Get("area"), cap)
	if err != nil {
		return nil, err
	}
	for _, e := range rows {
		if p := spaMap(e["place"]); len(p) > 0 {
			if err = s.publicDecoratePlaces(r.Context(), []map[string]any{p}); err != nil {
				return nil, err
			}
		}
	}
	return rows, nil
}
func (s *Server) publicEventDetail(r *http.Request, a platform.Actor) (pongo2.Context, error) {
	rows, err := socialRows(r.Context(), s.DB, `SELECT `+catalog.PublicEventProjectionSQL()+publicEventModel+` || jsonb_build_object('_public',(`+catalog.PolicyFromContext(r.Context()).EventSQL()+`),'_discoverable',(`+publicUpcomingSQL+`))`+publicEventsJoin+` WHERE e.id=$1 AND ((`+catalog.PolicyFromContext(r.Context()).EventSQL()+`) OR $2 OR EXISTS(SELECT 1 FROM social_userplaceproposal pp WHERE pp.place_id=p.id AND pp.proposer_id=$3))`, id(r, "pk"), a.IsStaff && a.IsActive, a.ID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, platform.ErrNotFound
	}
	e := rows[0]
	p := spaMap(e["place"])
	if len(p) > 0 {
		if err = s.publicDecoratePlaces(r.Context(), []map[string]any{p}); err != nil {
			return nil, err
		}
	}
	e["get_source_display"] = strings.Title(spaText(e["source"]))
	e["get_lifecycle_status_display"] = strings.Title(strings.ReplaceAll(spaText(e["lifecycle_status"]), "_", " "))
	public, discoverable := spaBool(e["_public"]), spaBool(e["_discoverable"])
	eligible := a.ID > 0 && platform.Participate(r.Context(), s.DB, a) == nil
	reliability, err := s.Catalog.EventReliability(r.Context(), spaID(e))
	if err != nil {
		return nil, err
	}
	data := pongo2.Context{"user": socialActor(a), "event": e, "event_discoverable": discoverable, "event_reliability": reliability, "attribution_credit": publicCredit(e), "can_report_event": public && discoverable && eligible, "can_convene": public && discoverable && eligible && len(p) > 0, "share_targets": []any{}, "share_kind": "event", "share_obj_id": spaID(e), "canonical_url": s.publicAbsolute(r, r.URL.Path)}
	if public {
		data["canonical_url"] = s.publicAbsolute(r, publicEventPath(e))
		data["structured_data"] = publicLD(s.publicEventLD(r, e))
		data["breadcrumb_data"] = publicLD(s.publicBreadcrumb(r, []map[string]any{{"name": "Home", "url": "/"}, {"name": "Events", "url": "/events/"}, {"name": e["title"], "url": publicEventPath(e)}}))
		if len(p) > 0 && spaBool(spaMap(e["activity_type"])["is_active"]) {
			areas, err := socialRows(r.Context(), s.DB, `SELECT jsonb_build_object('slug',slug,'name',name) FROM communities_area WHERE is_active AND lower(city)=lower($1) ORDER BY id LIMIT 1`, p["city"])
			if err != nil {
				return nil, err
			}
			if len(areas) > 0 {
				typ := spaMap(e["activity_type"])
				data["related_landing"] = map[string]any{"name": fmt.Sprint(typ["name"]) + " — " + fmt.Sprint(areas[0]["name"]), "url": routeURL("things_to_do", areas[0]["slug"], typ["slug"])}
			}
		}
	}
	return data, nil
}
func publicDate(value any) string {
	if t, ok := spaDateValue(value); ok {
		return t.Format(time.RFC3339Nano)
	}
	return spaText(value)
}

// The detail-page carve-out has already admitted only the proposer/staff. The
// canonical public facts method intentionally rejects unpublished places, so
// this narrow projection supplies the source detail shape without publishing it.
func (s *Server) publicVenueFacts(r *http.Request, a platform.Actor, p map[string]any, isPublic bool) ([]map[string]any, error) {
	if isPublic {
		return s.Catalog.VenueFacts(r.Context(), a, spaID(p), true)
	}
	rows, err := socialRows(r.Context(), s.DB, `SELECT jsonb_build_object('key',fact_key,'yes',count(*) FILTER(WHERE value),'no',count(*) FILTER(WHERE NOT value),'my_vote',bool_or(value) FILTER(WHERE user_id=$2)) FROM places_placefactvote WHERE place_id=$1 GROUP BY fact_key`, spaID(p), a.ID)
	if err != nil {
		return nil, err
	}
	votes := map[string]map[string]any{}
	for _, row := range rows {
		votes[spaText(row["key"])] = row
	}
	labels := map[string]string{"drinking_water": "Drinking water", "toilets": "Toilets", "lit_at_night": "Lit at night", "playground": "Playground nearby", "fenced": "Fenced / away from traffic", "shade": "Shade", "indoor_shelter": "Indoor shelter", "bike_parking": "Bike parking", "car_parking": "Car parking", "bus_tram_nearby": "Bus/tram nearby"}
	lookup := map[string]string{"drinking_water": "drinking_water", "toilets": "toilets", "lit_at_night": "lit", "bike_parking": "bicycle_parking", "car_parking": "parking"}
	present := map[string][2]string{"playground": {"leisure", "playground"}, "fenced": {"barrier", "fence"}, "shade": {"natural", "tree"}}
	tags := spaMap(p["_tags"])
	out := []map[string]any{}
	for _, key := range catalog.FactKeys {
		v := votes[key]
		state := "unknown"
		if raw, ok := lookup[key]; ok {
			switch strings.ToLower(strings.TrimSpace(spaText(tags[raw]))) {
			case "yes":
				state = "true"
			case "no":
				state = "false"
			}
		}
		if pair, ok := present[key]; ok && tags[pair[0]] == pair[1] {
			state = "true"
		}
		sourced := state != "unknown"
		yes, no := spaInt(v["yes"]), spaInt(v["no"])
		if !sourced && max(yes, no) >= 3 && yes != no {
			state = "false"
			if yes > no {
				state = "true"
			}
		}
		out = append(out, map[string]any{"key": key, "label": labels[key], "state": state, "yes": yes, "no": no, "required": 3, "my_vote": v["my_vote"], "osm_sourced": sourced})
	}
	return out, nil
}
