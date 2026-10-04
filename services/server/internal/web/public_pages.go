package web

// Public pages are projections of the native catalog, never an alternate
// publication or cohort policy. A pending venue has only the source's explicit
// proposer/staff carve-out and never emits structured data.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/flosch/pongo2/v6"
)

var publicViewNames = map[string]bool{"places_map": true, "places_list": true, "place_detail": true, "place_detail_slug": true, "place_propose": true, "places_pending": true, "place_claim": true, "place_official_image": true, "events_list": true, "event_detail": true, "event_detail_slug": true, "things_to_do_index": true, "things_to_do_city": true, "things_to_do": true, "discover": true, "open_data": true}

func (s *Server) PublicView(r *http.Request, a platform.Actor, name string) (pongo2.Context, string, bool, error) {
	if !publicViewNames[name] {
		return nil, "", false, nil
	}
	data := pongo2.Context{"user": socialActor(a)}
	ctx := r.Context()
	template := "web/" + name + ".html"
	fail := func(err error) (pongo2.Context, string, bool, error) { return nil, "", true, err }
	switch name {
	case "places_map":
		categories, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('slug',coalesce(parent.slug,c.slug),'name',coalesce(parent.name,c.name)) FROM places_placeactivity pa JOIN places_place p ON p.id=pa.place_id JOIN taxonomy_activitytype t ON t.id=pa.activity_id JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitycategory parent ON parent.id=c.parent_id WHERE NOT pa.is_disputed AND `+catalog.PublicPlaceSQL+` AND ($1::text='' OR lower(p.address_city)=lower($1)) GROUP BY coalesce(parent.slug,c.slug),coalesce(parent.name,c.name) ORDER BY lower(coalesce(parent.name,c.name))`, r.URL.Query().Get("city"))
		if err != nil {
			return fail(err)
		}
		vocabulary, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('slug',t.slug,'name',t.name,'aliases',CASE WHEN jsonb_typeof(t.aliases)='array' THEN t.aliases ELSE '[]'::jsonb END,'category',c.slug,'categoryName',c.name,'topCategory',coalesce(parent.slug,c.slug),'topCategoryName',coalesce(parent.name,c.name)) FROM taxonomy_activitytype t JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitycategory parent ON parent.id=c.parent_id WHERE t.is_active ORDER BY c.name,t.name`)
		if err != nil {
			return fail(err)
		}
		data["categories"], data["type_vocabulary"] = categories, vocabulary
		template = "web/places.html"
	case "places_list":
		places, near, err := s.publicPlaces(r, a, "", "", 200)
		if err != nil {
			return fail(err)
		}
		pref, err := s.Catalog.Access(ctx, a)
		if err != nil {
			return fail(err)
		}
		for _, p := range places {
			p["access_tags"] = []any{}
			p["access_match"] = catalog.MatchesAccess(catalog.AccessibilityFacts(spaMap(p["_tags"])), pref) == "match"
		}
		sort.SliceStable(places, func(i, j int) bool { return spaBool(places[i]["access_match"]) && !spaBool(places[j]["access_match"]) })
		data["places"], data["near_active"], data["truncated"] = places, near, len(places) == 200
		data["filters"] = map[string]any{"activity": r.URL.Query().Get("activity"), "city": r.URL.Query().Get("city"), "source": r.URL.Query().Get("source")}
		data["filtered"] = near || r.URL.Query().Get("activity") != "" || r.URL.Query().Get("city") != "" || r.URL.Query().Get("source") != ""
		if len(places) > 0 {
			data["structured_data"] = publicLD(s.publicItemList(r, places, false))
		}
	case "place_detail", "place_detail_slug":
		var err error
		data, err = s.publicPlaceDetail(r, a)
		if err != nil {
			return fail(err)
		}
		template = "web/place_detail.html"
	case "place_propose":
		if err := platform.Participate(ctx, s.DB, a); err != nil {
			if errors.Is(err, platform.ErrForbidden) {
				data["redirect"] = "/profile/"
				break
			}
			return fail(err)
		}
		form, err := s.form(r, a, "PlaceProposeForm", map[string]any{})
		if err != nil {
			return fail(err)
		}
		data["form"] = form
		if r.URL.Query().Get("return") == "organize" {
			data["return_to"] = "organize"
		}
	case "places_pending":
		rows, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',pp.id,'place',jsonb_build_object('id',p.id,'name',p.name,'address_city',p.address_city),'confirmations_count',(SELECT count(*) FROM social_placeconfirmation pc WHERE pc.proposal_id=pp.id),'required_confirmations',pp.required_confirmations) FROM social_userplaceproposal pp JOIN places_place p ON p.id=pp.place_id WHERE pp.status='pending' AND pp.proposer_id<>$1 ORDER BY pp.created_at,pp.id`, a.ID)
		if err != nil {
			return fail(err)
		}
		data["proposals"] = rows
	case "place_claim", "place_official_image":
		p, public, err := s.publicPlace(r, a, id(r, "pk"), false)
		if err != nil {
			return fail(err)
		}
		if !public {
			return fail(platform.ErrNotFound)
		}
		if name == "place_official_image" {
			var allowed bool
			err = s.DB.QueryRow(ctx, `SELECT $1 OR EXISTS(SELECT 1 FROM places_placeclaim claim JOIN places_partner partner ON partner.id=claim.partner_id WHERE claim.place_id=$2 AND claim.claimant_id=$3 AND claim.status='approved' AND claim.kind='business' AND partner.is_verified AND partner.is_active AND partner.kind='business' AND partner.place_id=claim.place_id)`, a.IsStaff, id(r, "pk"), a.ID).Scan(&allowed)
			if err != nil {
				return fail(err)
			}
			if !allowed {
				return fail(platform.ErrNotFound)
			}
			data["redirect"] = routeURL("place_detail", id(r, "pk"))
			template = "web/place_detail.html"
		} else {
			form, err := s.form(r, a, "PlaceClaimForm", map[string]any{})
			if err != nil {
				return fail(err)
			}
			data["place"], data["form"] = p, form
		}
	case "events_list":
		events, err := s.publicEvents(r, "", 0, 100)
		if err != nil {
			return fail(err)
		}
		areas, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',id,'slug',slug,'name',name,'city',city) FROM communities_area ORDER BY name`)
		if err != nil {
			return fail(err)
		}
		data["events"], data["areas"], data["query"], data["activity"] = events, areas, strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("activity")
		data["area"], data["area_name"] = "", ""
		for _, area := range areas {
			if area["slug"] == r.URL.Query().Get("area") {
				data["area"], data["area_name"] = area["slug"], area["name"]
			}
		}
		data["filtered"] = data["query"] != "" || data["activity"] != "" || r.URL.Query().Get("area") != ""
		if len(events) > 0 {
			data["structured_data"] = publicLD(s.publicItemList(r, events, true))
		}
		template = "web/events.html"
	case "event_detail", "event_detail_slug":
		var err error
		data, err = s.publicEventDetail(r, a)
		if err != nil {
			return fail(err)
		}
		template = "web/event_detail.html"
	case "things_to_do_index", "things_to_do_city", "things_to_do":
		var err error
		data, template, err = s.publicLanding(r, a, name)
		if err != nil {
			return fail(err)
		}
	case "discover":
		// Full presentation names still pass the identical public adult/listing gate;
		// no organizer, membership, identity or private logistics are selected.
		activities, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',a.id,'title',a.title,'starts_at',a.starts_at,'activity_type',jsonb_build_object('name',t.name),'place',jsonb_build_object('name',p.name)) FROM social_activity a JOIN accounts_user u ON u.id=a.owner_id JOIN taxonomy_activitytype t ON t.id=a.activity_type_id LEFT JOIN places_place p ON p.id=a.place_id WHERE a.cohort='adult' AND a.is_publicly_listed AND a.status='open' AND NOT a.is_hidden AND a.starts_at>=now() AND u.is_active ORDER BY a.starts_at,a.id LIMIT 100`)
		if err != nil {
			return fail(err)
		}
		groups, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',g.id,'title',g.title,'description',g.description,'activity_type',CASE WHEN t.id IS NULL THEN NULL ELSE jsonb_build_object('name',t.name) END,'category',jsonb_build_object('name',c.name),'area',jsonb_build_object('name',ar.name)) FROM social_group g JOIN accounts_user u ON u.id=g.owner_id LEFT JOIN taxonomy_activitytype t ON t.id=g.activity_type_id LEFT JOIN taxonomy_activitycategory c ON c.id=g.category_id LEFT JOIN communities_area ar ON ar.id=g.area_id WHERE g.cohort='adult' AND g.is_publicly_listed AND g.status='active' AND NOT g.is_hidden AND u.is_active ORDER BY g.title,g.id LIMIT 100`)
		if err != nil {
			return fail(err)
		}
		data["activities"], data["groups"] = activities, groups
	case "open_data":
		available := s.publicSnapshotAvailable()
		data["snapshot_available"] = available
		data["structured_data"] = publicLD(s.publicDataset(r, available))
	}
	return data, template, true, nil
}

const publicDisplayNameSQL = `coalesce((SELECT proposed_value FROM places_placecorrection WHERE place_id=p.id AND field='name' AND status='published' ORDER BY coalesce(published_at,created_at) DESC,id DESC LIMIT 1),p.name)`

const publicPlaceFields = ` || jsonb_build_object('_display_name',` + publicDisplayNameSQL + `,'_seed_hint',coalesce((SELECT t.slug FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id WHERE pa.place_id=p.id AND NOT pa.is_disputed ORDER BY pa.id LIMIT 1),p.address_city),'source',p.source,'address_street',p.address_street,'address_housenumber',p.address_housenumber,'address_city',p.address_city,'address_country',p.address_country,'address_postcode',p.address_postcode,'opening_hours',p.opening_hours,'_tags',p.raw_tags,'_reports',(SELECT count(*) FROM places_opennowreport rep WHERE rep.place_id=p.id AND rep.created_at>=now()-interval '14 days'),'category_chips',coalesce((SELECT jsonb_agg(chip) FROM (SELECT DISTINCT jsonb_build_object('slug',coalesce(parent.slug,c.slug),'name',coalesce(parent.name,c.name)) chip FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitycategory parent ON parent.id=c.parent_id WHERE pa.place_id=p.id AND NOT pa.is_disputed) chips),'[]'::jsonb))`

func (s *Server) publicPlaces(r *http.Request, a platform.Actor, city, activity string, cap int) ([]map[string]any, bool, error) {
	q := r.URL.Query()
	if city == "" {
		city = q.Get("city")
	}
	if activity == "" {
		activity = q.Get("activity")
	}
	near, err := catalog.ParseNear(q, false)
	if err != nil {
		near = catalog.Near{}
	}
	var confidence *float64
	if raw := q.Get("min_confidence"); raw != "" {
		var f float64
		_, err = fmt.Sscan(raw, &f)
		if err != nil {
			return nil, false, platform.ErrInvalid
		}
		confidence = &f
	}
	where := catalog.PublicPlaceSQL + ` AND ($1::text='' OR lower(p.address_city)=lower($1)) AND ($2::text='' OR lower(p.source)=lower($2)) AND ($3::text='' OR EXISTS(SELECT 1 FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id WHERE pa.place_id=p.id AND NOT pa.is_disputed AND lower(t.slug)=lower($3) AND ($4::float8 IS NULL OR pa.confidence>=$4))) AND ($4::float8 IS NULL OR EXISTS(SELECT 1 FROM places_placeactivity pa WHERE pa.place_id=p.id AND pa.confidence>=$4)) AND ($5::float8 IS NULL OR $6::float8 IS NULL OR $7::float8 IS NULL OR ST_DWithin(p.location,ST_SetSRID(ST_MakePoint($5,$6),4326)::geography,$7))`
	where += ` AND ($9::text='' OR EXISTS(SELECT 1 FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitycategory parent ON parent.id=c.parent_id WHERE pa.place_id=p.id AND NOT pa.is_disputed AND (lower(c.slug)=lower($9) OR lower(parent.slug)=lower($9)) AND ($4::float8 IS NULL OR pa.confidence>=$4)))`
	var upcoming *bool
	if raw := strings.ToLower(q.Get("has_upcoming")); raw == "true" || raw == "false" {
		v := raw == "true"
		upcoming = &v
	}
	activityGate := `a.cohort='adult' AND a.is_publicly_listed AND NOT a.is_hidden AND u.is_active`
	if a.ID > 0 && a.IsActive {
		activityGate = strings.ReplaceAll(strings.ReplaceAll(social.ActivityVisibilitySQL(), "$1", "$11"), "$2", "$12")
	}
	where += ` AND ($10::boolean IS NULL OR (EXISTS(SELECT 1 FROM social_activity a JOIN accounts_user u ON u.id=a.owner_id WHERE a.place_id=p.id AND a.status='open' AND a.starts_at>=now() AND ` + activityGate + `) OR EXISTS(SELECT 1 FROM events_event e WHERE e.place_id=p.id AND e.starts_at>=now() AND NOT e.is_import_held AND NOT e.is_tombstone AND e.lifecycle_status IN('scheduled','rescheduled','sold_out')))=$10) AND $11::bigint>=0 AND $12::text IS NOT NULL`
	order := "p.name,p.id"
	distance := "NULL::float8"
	active := near.Lon != nil && near.Lat != nil
	if active {
		distance = `ST_Distance(p.location,ST_SetSRID(ST_MakePoint($5,$6),4326)::geography)`
		order = distance + ",p.id"
	}
	rows, err := socialRows(r.Context(), s.DB, `SELECT `+catalog.PlaceExportProjectionSQL()+publicPlaceFields+` || jsonb_build_object('distance_m',`+distance+`) FROM places_place p WHERE `+where+` ORDER BY `+order+` LIMIT $8`, city, q.Get("source"), activity, confidence, near.Lon, near.Lat, near.Radius, cap, q.Get("category"), upcoming, a.ID, a.Cohort)
	if err != nil {
		return nil, active, err
	}
	if err = s.publicDecoratePlaces(r.Context(), rows); err != nil {
		return nil, active, err
	}
	return rows, active, nil
}
func (s *Server) publicDecoratePlaces(ctx context.Context, places []map[string]any) error {
	ids := []int64{}
	for _, p := range places {
		ids = append(ids, spaID(p))
	}
	visuals := map[int64]any{}
	var err error
	if s.Catalog.PlaceVisuals != nil && len(ids) > 0 {
		for offset := 0; offset < len(ids); offset += 1000 {
			batch, e := s.Catalog.PlaceVisuals(ctx, s.DB, ids[offset:min(offset+1000, len(ids))])
			if e != nil {
				return e
			}
			for id, v := range batch {
				visuals[id] = v
			}
		}
	}
	zone, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		return err
	}
	for _, p := range places {
		p["display_name"], p["display_address"], p["label"] = p["_display_name"], p["address"], p["name"]
		p["get_source_display"] = map[string]string{"osm": "OpenStreetMap", "overture": "Overture", "user": "User contributed", "roedu": "RO-EDU"}[spaText(p["source"])]
		hours := spaText(p["opening_hours_text"])
		p["posted_hours_text"] = hours
		p["display_opening_hours"] = hours
		schedule := catalog.ParseOpeningHours(hours)
		if hours == "" {
			raw, _ := json.Marshal(p["opening_hours"])
			_ = json.Unmarshal(raw, &schedule)
		}
		p["display_opening_hours"] = schedule
		var open any
		op := catalog.OpenAt(schedule, time.Now().In(zone))
		if op != nil {
			open = *op
			if spaInt(p["_reports"]) >= 3 {
				open = "unverified"
			}
		}
		p["open_now"] = open
		if visual, ok := visuals[spaID(p)]; ok {
			p["visual"] = visual
			if spaMap(visual)["kind"] == "place_cover_photo" {
				p["cover"] = true
			}
		}
		if p["visual"] == nil {
			seedName := spaText(p["_display_name"])
			if seedName == "" {
				seedName = fmt.Sprint(p["pk"])
			}
			p["visual"] = map[string]any{"kind": "accent", "svg": activitySVG("place:" + spaText(p["_seed_hint"]) + ":" + seedName)}
		} else if vis := spaMap(p["visual"]); vis["alt"] == "" {
			vis["alt"] = p["display_name"]
		}
		p["attribution_credit"] = publicCredit(p)
	}
	return nil
}
func (s *Server) publicPlace(r *http.Request, a platform.Actor, place int64, carveout bool) (map[string]any, bool, error) {
	where := `p.id=$1 AND (` + catalog.PublicPlaceSQL + `)`
	if carveout {
		where = `p.id=$1 AND ((` + catalog.PublicPlaceSQL + `) OR $2 OR EXISTS(SELECT 1 FROM social_userplaceproposal pp WHERE pp.place_id=p.id AND pp.proposer_id=$3))`
	}
	args := []any{place}
	if carveout {
		args = append(args, a.IsActive && a.IsStaff, a.ID)
	}
	rows, err := socialRows(r.Context(), s.DB, `SELECT `+catalog.PlaceExportProjectionSQL()+publicPlaceFields+` || jsonb_build_object('_public',(`+catalog.PublicPlaceSQL+`)) FROM places_place p WHERE `+where, args...)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, platform.ErrNotFound
	}
	if err = s.publicDecoratePlaces(r.Context(), rows); err != nil {
		return nil, false, err
	}
	return rows[0], spaBool(rows[0]["_public"]), nil
}
func publicCredit(p map[string]any) any {
	v := map[string]any{"attribution": strings.TrimSpace(spaText(p["attribution"])), "license_name": strings.TrimSpace(spaText(p["license_name"])), "provenance_url": publicExternal(spaText(p["provenance_url"]))}
	if v["attribution"] == "" && v["license_name"] == "" && v["provenance_url"] == "" {
		return nil
	}
	return v
}
func publicExternal(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(raw), "https://") || strings.HasPrefix(strings.ToLower(raw), "http://") {
		return raw
	}
	return ""
}
