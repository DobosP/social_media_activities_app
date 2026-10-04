package web

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

var publicSnapshotFiles = map[string]bool{"manifest.json": true, "events.json": true, "places.json": true, "activities.json": true, "taxonomy.json": true}
var publicBots = []string{"*", "GPTBot", "OAI-SearchBot", "ChatGPT-User", "ClaudeBot", "anthropic-ai", "Claude-User", "PerplexityBot", "Perplexity-User", "Google-Extended", "Googlebot", "Bingbot", "Applebot", "Applebot-Extended", "DuckDuckBot"}
var publicDisallowed = []string{"/admin/", "/api/", "/access/", "/account/", "/activities/", "/communities/", "/connections/", "/gauges/", "/groups/", "/guardianship/", "/inbox/", "/interests/", "/login/", "/logout/", "/messages/", "/my-activity-log/", "/my-donations/", "/my-meetups/", "/my-privacy/", "/my-safety-record/", "/my-venues/", "/notifications/", "/organize/", "/places/pending/", "/places/propose/", "/profile/", "/register/", "/report/", "/saved-searches/", "/settings/", "/share/", "/users/", "/verify-age/", "/wards/", "/you/"}

func publicDownload(w http.ResponseWriter, kind string, body []byte) {
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
func (s *Server) publicSnapshotAvailable() bool {
	if strings.TrimSpace(s.Config.SnapshotDir) == "" {
		return false
	}
	root, err := os.OpenRoot(s.Config.SnapshotDir)
	if err != nil {
		return false
	}
	defer root.Close()
	info, err := root.Stat("manifest.json")
	return err == nil && info.Mode().IsRegular()
}
func (s *Server) PublicDownload(w http.ResponseWriter, r *http.Request, a platform.Actor, name string) bool {
	switch name {
	case "robots_txt", "robots":
		var out strings.Builder
		out.WriteString("# Welcome, crawlers and AI agents. Public pages (venues, events, info) are open;\n# everything below is account- or cohort-scoped and must not be crawled.\n\n")
		for _, agent := range publicBots {
			fmt.Fprintf(&out, "User-agent: %s\nAllow: /\n", agent)
			for _, path := range publicDisallowed {
				fmt.Fprintf(&out, "Disallow: %s\n", path)
			}
			out.WriteString("Allow: /api/v1/events\nAllow: /api/v1/places\nAllow: /api/schema/\n\n")
		}
		fmt.Fprintf(&out, "Sitemap: %s\n", s.publicAbsolute(r, "/sitemap.xml"))
		publicDownload(w, "text/plain; charset=utf-8", []byte(out.String()))
	case "indexnow_key_file", "indexnow":
		if s.Config.IndexNowKey == "" {
			http.NotFound(w, r)
		} else {
			publicDownload(w, "text/plain; charset=utf-8", []byte(s.Config.IndexNowKey))
		}
	case "llms_txt", "llms":
		publicDownload(w, "text/markdown; charset=utf-8", []byte(s.publicLLMs(r)))
	case "open_data_snapshot":
		file := r.PathValue("name")
		if !publicSnapshotFiles[file] || s.Config.SnapshotDir == "" {
			http.NotFound(w, r)
			return true
		}
		root, err := os.OpenRoot(s.Config.SnapshotDir)
		if err != nil {
			http.NotFound(w, r)
			return true
		}
		defer root.Close()
		f, err := root.Open(file)
		if err != nil {
			http.NotFound(w, r)
			return true
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil || !stat.Mode().IsRegular() {
			http.NotFound(w, r)
			return true
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, file, stat.ModTime(), f)
	case "events_feed", "events_feed_atom":
		// Feeds ignore q and all non-source filters, retaining only area/activity.
		request := r.Clone(r.Context())
		u := *r.URL
		request.URL = &u
		q := u.Query()
		for key := range q {
			if key != "area" && key != "activity" {
				q.Del(key)
			}
		}
		u.RawQuery = q.Encode()
		events, err := s.publicEvents(request, "", 0, 100)
		if err != nil {
			platform.Fail(w, err)
			return true
		}
		body, err := s.publicFeed(r, events, name == "events_feed_atom")
		if err != nil {
			platform.Fail(w, err)
			return true
		}
		kind := "application/rss+xml; charset=utf-8"
		if name == "events_feed_atom" {
			kind = "application/atom+xml; charset=utf-8"
		}
		publicDownload(w, kind, body)
	case "sitemap", "sitemap_xml":
		body, err := s.publicSitemap(r)
		if err != nil {
			platform.Fail(w, err)
			return true
		}
		publicDownload(w, "application/xml; charset=utf-8", body)
	case "schema", "openapi_schema":
		body, err := json.Marshal(s.publicSchema())
		if err != nil {
			platform.Fail(w, err)
			return true
		}
		publicDownload(w, "application/json", body)
	case "docs", "api_docs":
		publicDownload(w, "text/html; charset=utf-8", []byte(`<!doctype html><html lang="en"><meta charset="utf-8"><title>Activities API</title><h1>Activities API</h1><p>Native Go service. Public venue and event reads preserve source licensing and publication gates. Private endpoints require authentication and cohort authorization. Mutations require same-origin CSRF.</p><p><a href="/api/schema/">OpenAPI schema</a></p><ul><li><a href="/api/v1/places/">Places GeoJSON</a></li><li><a href="/api/v1/events/">Upcoming events</a></li><li><a href="/open-data/">Licensing and bulk snapshots</a></li></ul></html>`))
	default:
		return false
	}
	return true
}
func (s *Server) publicLLMs(r *http.Request) string {
	var out strings.Builder
	out.WriteString("# Activities\n\n> A nonprofit, text-first platform that helps people — children first, also adults — meet\n> in person for real group activities (sport, outdoors, games, reading, culture) at real,\n> known places. First city: Cluj-Napoca, Romania (EU). No ads, no tracking, donation-funded.\n\n## What is public and citeable\n\n- Venues (parks, libraries, sports halls), seeded from open data.\n- Events: what's happening at those venues, soonest first.\n- Info: civic partners, donation transparency, privacy and terms, open data.\n\n## Key pages\n\n")
	for _, link := range [][2]string{{"Places (venues)", "places_list"}, {"Events (what's happening)", "events_list"}, {"Things to do by city & activity", "things_to_do_index"}, {"Events feed (RSS)", "events_feed"}, {"Events feed (Atom)", "events_feed_atom"}, {"Open data", "open_data"}, {"Civic partners", "partners"}, {"Donation transparency", "transparency"}} {
		fmt.Fprintf(&out, "- [%s](%s)\n", link[0], s.publicAbsolute(r, routeURL(link[1])))
	}
	fmt.Fprintf(&out, "- [Sitemap](%s)\n\n## Machine-readable APIs\n\n- [Events JSON API](%s) — public, read-only. Filter with `?place=<id>`, `?activity=<slug>`, `?city=`, `?from=`/`?to=` (ISO date or datetime; `to` exclusive, a bare date covers that whole day), `?q=` (free text over title/description/venue, min 2 chars), `?near_lon=&near_lat=` (nearest-first) + `?radius_m=`, `?include_past=true`.\n- [Places JSON API](%s) — public, read-only, GeoJSON. Filter with `?activity=<slug>`, `?city=`, `?near_lon=&near_lat=` (nearest-first) and `?radius_m=` (metres).\n- Public activity cards (adult, opt-in listings only): `/api/v1/discovery/public/activities/` — filter with `?activity=<slug>`, `?from=`/`?to=`, and the same `near_*` proximity params.\n- [OpenAPI schema](%s) describes every field on all of them.\n- Anonymous requests are rate-limited to 60 requests/minute; cache responses and poll politely.\n- `/agent/v1/` (where deployed) serves a cached, read-only mirror, with its own schema at `/agent/v1/openapi.json`.\n- Cite each venue/event's canonical page on this site, rather than only a raw API endpoint. An event's `url` field is its external ticket/source page.\n\n## Notes for agents\n\n- Meetups are private and cohort-isolated for child safety, require a verified account, and are intentionally not crawlable. Public activity cards are hard-scoped to adult, explicit opt-in listings — a child/teen meetup is never exposed.\n- Pages carry schema.org JSON-LD (Event, Place, Dataset).\n", s.publicAbsolute(r, "/sitemap.xml"), s.publicAbsolute(r, "/api/v1/events/"), s.publicAbsolute(r, "/api/v1/places/"), s.publicAbsolute(r, "/api/schema/"))
	return out.String()
}

type publicXMLEntry struct {
	Location   string `xml:"loc"`
	Lastmod    string `xml:"lastmod,omitempty"`
	Changefreq string `xml:"changefreq"`
	Priority   string `xml:"priority"`
}

func (s *Server) publicSitemap(r *http.Request) ([]byte, error) {
	r = r.Clone(r.Context())
	u := *r.URL
	r.URL = &u
	r.URL.RawQuery = ""
	entries := []publicXMLEntry{}
	for _, path := range []string{"/", "/places/list/", "/events/", "/things-to-do/", "/partners/", "/open-data/", "/transparency/", "/privacy/", "/terms/"} {
		entries = append(entries, publicXMLEntry{Location: s.publicAbsolute(r, path), Changefreq: "weekly", Priority: "0.6"})
	}
	places, err := socialRows(r.Context(), s.DB, `SELECT `+catalog.PlaceExportProjectionSQL()+` || jsonb_build_object('_display_name',`+publicDisplayNameSQL+`,'last_seen_at',p.last_seen_at) FROM places_place p WHERE `+catalog.PolicyFromContext(r.Context()).PlaceSQL()+` ORDER BY p.id LIMIT 50000`)
	if err != nil {
		return nil, err
	}
	for _, p := range places {
		entries = append(entries, publicXMLEntry{Location: s.publicAbsolute(r, publicPlacePath(p)), Lastmod: publicDate(p["last_seen_at"]), Changefreq: "weekly", Priority: "0.7"})
	}
	events, err := s.publicEvents(r, "", 0, 50000)
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		entries = append(entries, publicXMLEntry{Location: s.publicAbsolute(r, publicEventPath(e)), Lastmod: publicDate(e["updated_at"]), Changefreq: "daily", Priority: "0.8"})
	}
	combos, err := s.publicLandingCombos(r)
	if err != nil {
		return nil, err
	}
	for _, combo := range combos {
		entries = append(entries, publicXMLEntry{Location: s.publicAbsolute(r, routeURL("things_to_do", spaMap(combo["area"])["slug"], spaMap(combo["activity"])["slug"])), Changefreq: "daily", Priority: "0.6"})
	}
	node := struct {
		XMLName   xml.Name         `xml:"urlset"`
		Namespace string           `xml:"xmlns,attr"`
		Entries   []publicXMLEntry `xml:"url"`
	}{Namespace: "http://www.sitemaps.org/schemas/sitemap/0.9", Entries: entries}
	body, err := xml.Marshal(node)
	return append([]byte(xml.Header), body...), err
}
func xmlText(w io.Writer, value string) { _ = xml.EscapeText(w, []byte(value)) }
func (s *Server) publicFeed(r *http.Request, events []map[string]any, atom bool) ([]byte, error) {
	var out strings.Builder
	out.WriteString(xml.Header)
	updated := time.Unix(0, 0).UTC()
	for _, e := range events {
		if t, ok := spaDateValue(e["updated_at"]); ok && t.After(updated) {
			updated = t
		}
	}
	if atom {
		out.WriteString(`<feed xmlns="http://www.w3.org/2005/Atom"><title>Upcoming activities &amp; events</title><subtitle>Public events at real places — soonest first.</subtitle><id>`)
		xmlText(&out, s.publicAbsolute(r, "/events/"))
		out.WriteString(`</id><link rel="alternate" href="`)
		xmlText(&out, s.publicAbsolute(r, "/events/"))
		out.WriteString(`"/><updated>`)
		xmlText(&out, updated.Format(time.RFC3339))
		out.WriteString(`</updated>`)
	} else {
		out.WriteString(`<rss version="2.0"><channel><title>Upcoming activities &amp; events</title><description>Public events at real places — soonest first.</description><link>`)
		xmlText(&out, s.publicAbsolute(r, "/events/"))
		out.WriteString(`</link><lastBuildDate>`)
		xmlText(&out, updated.Format(time.RFC1123Z))
		out.WriteString(`</lastBuildDate>`)
	}
	for _, e := range events {
		path := s.publicAbsolute(r, publicEventPath(e))
		if atom {
			out.WriteString(`<entry><title>`)
			xmlText(&out, spaText(e["title"]))
			out.WriteString(`</title><id>`)
			xmlText(&out, path)
			out.WriteString(`</id><link rel="alternate" href="`)
			xmlText(&out, path)
			out.WriteString(`"/><summary>`)
			xmlText(&out, spaText(e["description"]))
			out.WriteString(`</summary><published>`)
			xmlText(&out, publicDate(e["created_at"]))
			out.WriteString(`</published><updated>`)
			xmlText(&out, publicDate(e["updated_at"]))
			out.WriteString(`</updated></entry>`)
		} else {
			out.WriteString(`<item><title>`)
			xmlText(&out, spaText(e["title"]))
			out.WriteString(`</title><link>`)
			xmlText(&out, path)
			out.WriteString(`</link><guid>`)
			xmlText(&out, path)
			out.WriteString(`</guid><description>`)
			xmlText(&out, spaText(e["description"]))
			out.WriteString(`</description><pubDate>`)
			if t, ok := spaDateValue(e["created_at"]); ok {
				xmlText(&out, t.Format(time.RFC1123Z))
			}
			out.WriteString(`</pubDate></item>`)
		}
	}
	if atom {
		out.WriteString(`</feed>`)
	} else {
		out.WriteString(`</channel></rss>`)
	}
	return []byte(out.String()), nil
}
