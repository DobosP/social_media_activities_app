package export

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

var agentapiBinary = flag.String("agentapi-test-binary", "", "Explicit separately built native agentapi executable for disposable process fixture")

func TestPostgresNativeExporterToAgentAPIExecutableContract(t *testing.T) {
	if *exportDSN == "" {
		t.Skip("explicit disposable PostgreSQL fixture not supplied")
	}
	if *agentapiBinary == "" || !filepath.IsAbs(*agentapiBinary) {
		t.Fatal("database qualification requires an absolute -agentapi-test-binary")
	}
	info, err := os.Stat(*agentapiBinary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		t.Fatal("native agentapi fixture executable unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db := testdb.New(t, *exportDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	owner := testdb.Actor(t, db, "agent-process-owner", "adult")
	library := testdb.Place(t, db, "Biblioteca Științei", "osm")
	// Native producer and separately running consumer must preserve PostgreSQL
	// bigint primary and foreign keys beyond IEEE-754's exact integer range.
	smallLibrary := library
	library = 9007199254740993
	if _, err = db.Exec(ctx, `UPDATE places_place SET id=$2 WHERE id=$1`, smallLibrary, library); err != nil {
		t.Fatal(err)
	}
	park := testdb.Place(t, db, "Dobo Park", "osm")
	pending := testdb.Place(t, db, "Private pending venue", "user")
	for id, lat := range map[int64]float64{library: 46.7712, park: 46.7810881} {
		if _, err = db.Exec(ctx, `UPDATE places_place SET location=ST_SetSRID(ST_MakePoint(23.6236,$2),4326),license_name='ODbL-1.0',attribution='OpenStreetMap contributors' WHERE id=$1`, id, lat); err != nil {
			t.Fatal(err)
		}
	}
	var typ int64
	if err = db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	anchor := time.Now().UTC().Truncate(time.Second).Add(10 * 24 * time.Hour)
	seedEvent := func(title, status string, place *int64, at time.Time) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRow(ctx, `INSERT INTO events_event(title,description,starts_at,ends_at,url,source,external_id,attribution,license_name,provenance_url,created_at,updated_at,place_id,activity_type_id,source_category,source_confidence,is_import_held,lifecycle_status,is_tombstone,source_venue_id,source_city,source_pack_id,source_snapshot_id,source_release_id,source_recurrence,source_timezone,source_price_min,source_price_max,source_currency,source_is_free,source_availability) VALUES($1,'A reviewed descriptive keyword',$2,NULL,'https://example.invalid/event','manual','','Public source','CC-BY-4.0','https://example.invalid/facts/first',now(),now(),$3,$4,'',NULL,false,$5,false,'','','','','','','Europe/Bucharest',20.00,50.00,'RON',false,'limited') RETURNING id`, title, at, place, typ, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := seedEvent("Public first event", "scheduled", &library, anchor)
	smallFirst := first
	first = 9007199254740995
	if _, err = db.Exec(ctx, `UPDATE events_event SET id=$2 WHERE id=$1`, smallFirst, first); err != nil {
		t.Fatal(err)
	}
	second := seedEvent("Later park event", "scheduled", &park, anchor.Add(2*24*time.Hour))
	standalone := seedEvent("No venue event", "scheduled", nil, anchor.Add(24*time.Hour))
	excluded := []int64{seedEvent("Excluded pending venue", "scheduled", &pending, anchor), seedEvent("Excluded cancelled event", "cancelled", &library, anchor), seedEvent("Excluded past event", "scheduled", &library, time.Now().Add(-24*time.Hour))}
	for _, field := range []string{"is_import_held", "is_tombstone"} {
		id := seedEvent("Excluded held/tombstoned event", "scheduled", &library, anchor)
		if _, err = db.Exec(ctx, `UPDATE events_event SET `+field+`=true WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		excluded = append(excluded, id)
	}
	soc := social.New(db, platform.RecordAudit)
	var publicActivity int64
	for _, cohort := range []string{"adult", "child", "teen"} {
		actor := owner
		if cohort != "adult" {
			actor = testdb.Actor(t, db, "agent-process-"+cohort, cohort)
		}
		id, e := soc.CreateActivity(ctx, actor, social.ActivityInput{Place: library, ActivityType: typ, Title: "Fixture activity " + cohort, Description: "Never export private description", StartsAt: anchor})
		if e != nil {
			t.Fatal(e)
		}
		// Deliberately hostile persisted flag proves public hardcoded ADULT gate.
		if _, err = db.Exec(ctx, `UPDATE social_activity SET is_publicly_listed=true WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if cohort == "adult" {
			publicActivity = id
		}
	}
	if _, err = soc.CreateActivity(ctx, owner, social.ActivityInput{Place: library, ActivityType: typ, Title: "Private adult card", StartsAt: anchor}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE social_activity SET is_publicly_listed=false WHERE title='Private adult card'`); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	producer := New(db, "https://example.invalid")
	summary, err := producer.Snapshot(ctx, directory)
	if err != nil || summary.Events != 3 || summary.Places != 2 || summary.Activities != 1 || summary.Truncated {
		t.Fatal("native snapshot fixture publication differs", summary, err)
	}
	readDataset := func(name string) struct {
		Generated string            `json:"generated_at"`
		Records   []json.RawMessage `json:"records"`
	} {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(directory, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var data struct {
			Generated string            `json:"generated_at"`
			Records   []json.RawMessage `json:"records"`
		}
		if json.Unmarshal(raw, &data) != nil {
			t.Fatal("invalid exporter JSON")
		}
		if bytes.Contains(raw, []byte("Never export private")) || bytes.Contains(raw, []byte("agent-process-owner")) || bytes.Contains(raw, []byte("Private pending venue")) || bytes.Contains(raw, []byte("owner_id")) || bytes.Contains(raw, []byte("raw_tags")) {
			t.Fatal("native producer leaked nonpublic fields")
		}
		return data
	}
	events := readDataset("events")
	records := map[int64]json.RawMessage{}
	for _, raw := range events.Records {
		var row struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(raw, &row) != nil {
			t.Fatal("event ID")
		}
		records[row.ID] = raw
	}
	for _, id := range []int64{first, second, standalone} {
		if records[id] == nil {
			t.Fatal("native exporter omitted public fixture")
		}
	}
	for _, id := range excluded {
		if records[id] != nil {
			t.Fatal("native exporter published forbidden event")
		}
	}
	base, client := startAgentProcess(t, ctx, directory, *agentapiBinary)
	mux := http.NewServeMux()
	catalog.New(db).Register(mux)
	cases := []url.Values{{}, {"place": {fmt.Sprint(library)}}, {"place": {fmt.Sprint(park)}, "city": {"cluj-napoca"}}, {"activity": {"basketball"}}, {"q": {"descriptive"}}, {"q": {"Științei"}}, {"from": {anchor.Format(time.RFC3339)}, "to": {anchor.Add(2 * 24 * time.Hour).Format(time.RFC3339)}}, {"near_lat": {"46.7810881"}, "near_lon": {"23.6236"}, "radius_m": {"2000"}}}
	for index, params := range cases {
		t.Run("native-query-"+strconv.Itoa(index), func(t *testing.T) {
			out := httptest.NewRecorder()
			mux.ServeHTTP(out, httptest.NewRequest("GET", "/api/v1/events/?"+params.Encode(), nil))
			if out.Code != 200 {
				t.Fatalf("native source query rejected: %d", out.Code)
			}
			var reference struct {
				Count   int `json:"count"`
				Results []struct {
					ID int64 `json:"id"`
				} `json:"results"`
			}
			if json.Unmarshal(out.Body.Bytes(), &reference) != nil {
				t.Fatal("invalid native source query envelope")
			}
			response, raw := agentGet(t, client, base+"/agent/v1/events?"+params.Encode(), nil)
			var envelope struct {
				Generated string            `json:"generated_at"`
				Total     int               `json:"total"`
				Data      []json.RawMessage `json:"data"`
			}
			if response.StatusCode != 200 || json.Unmarshal(raw, &envelope) != nil {
				t.Fatal("invalid native sidecar envelope")
			}
			if envelope.Generated != events.Generated || envelope.Total != reference.Count || len(envelope.Data) != len(reference.Results) {
				t.Fatal("producer/consumer generation or cardinality mismatch")
			}
			if index == 5 && (reference.Count != 1 || envelope.Total != 1 || len(reference.Results) != 1 || reference.Results[0].ID != first) {
				t.Fatal("Unicode venue query must find the exact public event")
			}
			for i, result := range reference.Results {
				if !bytes.Equal(envelope.Data[i], records[result.ID]) {
					t.Fatal("native consumer changed exact public record bytes/order")
				}
			}
			if response.Header.Get("Set-Cookie") != "" || response.Header.Get("Cache-Control") != "public, max-age=300" {
				t.Fatal("public native response created session or cache contract drift")
			}
		})
	}
	response, raw := agentGet(t, client, base+fmt.Sprintf("/agent/v1/events/%d", first), nil)
	if response.StatusCode != 200 {
		t.Fatal("native detail unavailable")
	}
	var detail struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &detail) != nil || !bytes.Equal(detail.Data, records[first]) {
		t.Fatal("native detail changed exported bytes")
	}
	var exact struct {
		Min          string `json:"source_price_min"`
		Max          string `json:"source_price_max"`
		Currency     string `json:"source_currency"`
		Availability string `json:"source_availability"`
		License      string `json:"license_name"`
		Provenance   string `json:"provenance_url"`
	}
	if json.Unmarshal(detail.Data, &exact) != nil || exact.Min != "20.00" || exact.Max != "50.00" || exact.Currency != "RON" || exact.Availability != "limited" || exact.License != "CC-BY-4.0" || exact.Provenance != "https://example.invalid/facts/first" {
		t.Fatal("price/license/source facts changed at serving boundary")
	}
	var exactIDs struct {
		ID      int64  `json:"id"`
		Place   int64  `json:"place_id"`
		Path    string `json:"path"`
		Summary struct {
			ID int64 `json:"id"`
		} `json:"place_summary"`
	}
	if json.Unmarshal(detail.Data, &exactIDs) != nil || exactIDs.ID != first || exactIDs.Place != library || exactIDs.Summary.ID != library || exactIDs.Path != fmt.Sprintf("/events/%d/public-first-event/", first) {
		t.Fatal("native process boundary rounded a bigint key or event path")
	}
	placeResponse, placeRaw := agentGet(t, client, base+fmt.Sprintf("/agent/v1/places/%d", library), nil)
	var placeDetail struct {
		Data struct {
			ID   int64  `json:"id"`
			Path string `json:"path"`
		} `json:"data"`
	}
	if placeResponse.StatusCode != 200 || json.Unmarshal(placeRaw, &placeDetail) != nil || placeDetail.Data.ID != library || placeDetail.Data.Path != fmt.Sprintf("/places/%d/biblioteca-stiintei/", library) {
		t.Fatal("native sidecar rounded bigint place URL or key")
	}
	for _, name := range []string{"activities", "places"} {
		exported := readDataset(name)
		response, raw := agentGet(t, client, base+"/agent/v1/"+name, nil)
		var envelope struct {
			Data []json.RawMessage `json:"data"`
		}
		if response.StatusCode != 200 || json.Unmarshal(raw, &envelope) != nil {
			t.Fatal("native collection record bytes differ", name)
		}
		if name == "places" {
			// The place API sorts names; the producer writes primary-key order.
			// Compare exact record bytes by int64 ID and assert API order separately.
			expected := map[int64]json.RawMessage{}
			for _, raw := range exported.Records {
				var row struct {
					ID int64 `json:"id"`
				}
				if json.Unmarshal(raw, &row) != nil {
					t.Fatal("exported place ID")
				}
				expected[row.ID] = raw
			}
			gotIDs := []int64{}
			for _, raw := range envelope.Data {
				var row struct {
					ID int64 `json:"id"`
				}
				if json.Unmarshal(raw, &row) != nil || !bytes.Equal(raw, expected[row.ID]) {
					t.Fatal("native sidecar changed place record bytes or bigint IDs")
				}
				gotIDs = append(gotIDs, row.ID)
			}
			if len(envelope.Data) != len(expected) || !reflect.DeepEqual(gotIDs, []int64{library, park}) {
				t.Fatal("native sidecar changed place set/name ordering")
			}
		} else if !reflect.DeepEqual(envelope.Data, exported.Records) {
			t.Fatal("native activity bytes/order differ")
		}
		if name == "activities" {
			var row struct {
				ID     int64  `json:"id"`
				Cohort string `json:"cohort"`
			}
			if len(envelope.Data) != 1 || json.Unmarshal(envelope.Data[0], &row) != nil || row.ID != publicActivity || row.Cohort != "adult" {
				t.Fatal("minor/private activity escaped producer gates")
			}
		}
	}
	_, raw = agentGet(t, client, base+"/agent/v1/events?limit=1&offset=1", nil)
	var page struct {
		Total, Count, Limit, Offset int
		Data                        []json.RawMessage
	}
	if json.Unmarshal(raw, &page) != nil || page.Total != 3 || page.Count != 1 || page.Limit != 1 || page.Offset != 1 || len(page.Data) != 1 || !bytes.Equal(page.Data[0], records[standalone]) {
		t.Fatal("native paging continuation changed")
	}
	response, _ = agentGet(t, client, base+"/agent/v1/events", nil)
	etag := response.Header.Get("ETag")
	if etag == "" {
		t.Fatal("native ETag missing")
	}
	response, raw = agentGet(t, client, base+"/agent/v1/events", map[string]string{"If-None-Match": etag})
	if response.StatusCode != 304 || len(raw) != 0 || response.Header.Get("ETag") != etag {
		t.Fatal("native conditional GET contract")
	}
	response, raw = agentGet(t, client, base+"/agent/v1/events", map[string]string{"Accept-Encoding": "gzip"})
	if response.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("native compression unavailable")
	}
	unzip, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(io.LimitReader(unzip, 1<<20))
	_ = unzip.Close()
	if err != nil || !json.Valid(decoded) {
		t.Fatal("native gzip corrupted response")
	}
	response, _ = agentGet(t, client, base+"/api/v1/events/", nil)
	if response.StatusCode != 404 {
		t.Fatal("sidecar falsely aliases native private/DRF API")
	}
	response, raw = agentGet(t, client, base+"/agent/v1/healthz", nil)
	var health struct {
		Status string  `json:"status"`
		Age    float64 `json:"snapshot_age_seconds"`
	}
	if response.StatusCode != 200 || json.Unmarshal(raw, &health) != nil || health.Status != "ok" || health.Age >= 30 {
		t.Fatal("native sidecar snapshot health")
	}
}

func startAgentProcess(t *testing.T, ctx context.Context, directory, binary string) (string, *http.Client) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal("synthetic loopback listener unavailable")
	}
	address := listener.Addr().String()
	_ = listener.Close()
	command := exec.CommandContext(ctx, binary)
	command.Env = []string{"AGENT_API_ADDR=" + address, "AGENT_SNAPSHOT_DIR=" + directory, "AGENT_API_RELOAD_SECONDS=30", "AGENT_API_RATE_PER_MIN=300", "AGENT_API_RATE_BURST=60"}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err = command.Start(); err != nil {
		t.Fatal("native sidecar process could not start")
	}
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Error("native fixture process did not reap")
			}
		}
	})
	transport := &http.Transport{Proxy: nil, DisableCompression: true, ResponseHeaderTimeout: 2 * time.Second, MaxResponseHeaderBytes: 16 << 10}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(transport.CloseIdleConnections)
	base := "http://" + address
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-finished:
			t.Fatal("native fixture process exited before readiness")
		default:
		}
		r, err := client.Get(base + "/agent/v1/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 4096))
			_ = r.Body.Close()
			if r.StatusCode == 200 {
				return base, client
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("native fixture process failed snapshot readiness")
	return "", nil
}
func agentGet(t *testing.T, client *http.Client, path string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	r, err := http.NewRequest("GET", path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal("synthetic native sidecar request failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		t.Fatal("native sidecar response exceeded fixture bound")
	}
	return response, raw
}
