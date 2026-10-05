package catalog

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5/pgconn"
)

func casePortPlaceProperties(t *testing.T, s *Service, place int64) map[string]any {
	t.Helper()
	w := request(t, s, fmt.Sprintf("/api/places/%d/", place), nil)
	if w.Code != 200 {
		t.Fatal("public place", w.Code)
	}
	return jsonObject(t, w.Body.Bytes())["properties"].(map[string]any)
}

func TestCasePortPostgresVenueCorrectionLifecycleAndValidation(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	author := user(t, s, "case-correction-author", "adult")
	staff := user(t, s, "case-correction-staff", "adult")
	staff.IsStaff = true
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1`, staff.ID); err != nil {
		t.Fatal(err)
	}
	peers := []platform.Actor{}
	for i := 0; i < 3; i++ {
		peers = append(peers, user(t, s, fmt.Sprintf("case-correction-peer%d", i), "adult"))
	}
	place := placeFixture(t, s, "Old Name", "osm", 23.6, 46.77)
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET address_street='Str. Veche',address_housenumber='5',address_city='Cluj' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	props := casePortPlaceProperties(t, s, place)
	if props["name"] != "Old Name" || props["display_address"] != "Str. Veche 5, Cluj" {
		t.Fatal("uncorrected source label/address")
	}
	correction, err := s.ProposeCorrection(ctx, author, place, "name", " Corrected Name ")
	if err != nil {
		t.Fatal(err)
	}
	if casePortPlaceProperties(t, s, place)["name"] != "Old Name" {
		t.Fatal("pending name published prematurely")
	}
	if err = s.ConfirmCorrection(ctx, author, correction); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("proposer confirmed own correction", err)
	}
	for i, peer := range peers {
		if err = s.ConfirmCorrection(ctx, peer, correction); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err = s.ConfirmCorrection(ctx, peer, correction); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM places_placecorrectionconfirmation WHERE correction_id=$1`, correction).Scan(&count); err != nil || count != 1 {
				t.Fatal("repeat confirmation changed unique vote", count, err)
			}
		}
	}
	var rawName, state, value string
	var published bool
	if err = s.DB.QueryRow(ctx, `SELECT p.name,c.status,c.proposed_value,c.published_at IS NOT NULL FROM places_place p JOIN places_placecorrection c ON c.place_id=p.id WHERE c.id=$1`, correction).Scan(&rawName, &state, &value, &published); err != nil || rawName != "Old Name" || state != "published" || value != "Corrected Name" || !published {
		t.Fatal("published overlay mutated raw data or lost state", err)
	}
	props = casePortPlaceProperties(t, s, place)
	if props["name"] != "Corrected Name" || props["display_address"] != "Str. Veche 5, Cluj" {
		t.Fatal("published name absent from serializer")
	}
	second, err := s.ProposeCorrection(ctx, author, place, "name", "Second")
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range peers {
		if err = s.ConfirmCorrection(ctx, peer, second); err != nil {
			t.Fatal(err)
		}
	}
	if casePortPlaceProperties(t, s, place)["name"] != "Second" {
		t.Fatal("latest published correction did not win")
	}
	if err = s.StaffCorrection(ctx, staff, second, false, ""); err != nil {
		t.Fatal(err)
	}
	if casePortPlaceProperties(t, s, place)["name"] != "Corrected Name" {
		t.Fatal("reject failed to restore prior published overlay")
	}
	if err = s.StaffCorrection(ctx, staff, correction, false, ""); err != nil {
		t.Fatal(err)
	}
	if casePortPlaceProperties(t, s, place)["name"] != "Old Name" {
		t.Fatal("reject failed to restore raw name")
	}
	if err = s.DB.QueryRow(ctx, `SELECT status,published_at IS NOT NULL FROM places_placecorrection WHERE id=$1`, correction).Scan(&state, &published); err != nil || state != "rejected" || published {
		t.Fatal("published rejection retained publication", err)
	}
	if err = s.StaffCorrection(ctx, staff, correction, false, ""); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("repeat rejection accepted", err)
	}
	address, err := s.ProposeCorrection(ctx, author, place, "address", "Str. Noua 9, Cluj")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.StaffCorrection(ctx, staff, address, true, ""); err != nil {
		t.Fatal(err)
	}
	if casePortPlaceProperties(t, s, place)["display_address"] != "Str. Noua 9, Cluj" {
		t.Fatal("address correction absent from serializer")
	}
	rejected, err := s.ProposeCorrection(ctx, author, place, "name", "Rejected before publication")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.StaffCorrection(ctx, staff, rejected, false, "spam"); err != nil {
		t.Fatal(err)
	}
	if casePortPlaceProperties(t, s, place)["name"] != "Old Name" {
		t.Fatal("pending rejected correction affected display")
	}
	fastPlace := placeFixture(t, s, "Old", "osm", 23.6, 46.77)
	fast, err := s.ProposeCorrection(ctx, author, fastPlace, "name", "Staff New")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.StaffCorrection(ctx, staff, fast, true, ""); err != nil {
		t.Fatal(err)
	}
	if casePortPlaceProperties(t, s, fastPlace)["name"] != "Staff New" {
		t.Fatal("staff name publish did not apply")
	}
	for _, cell := range [][2]string{{"phone", "x"}, {"name", "   "}, {"hours", "definitely not hours"}} {
		if _, err = s.ProposeCorrection(ctx, author, place, cell[0], cell[1]); !errors.Is(err, platform.ErrInvalid) {
			t.Fatal("malformed correction accepted", cell, err)
		}
	}
	unknown := user(t, s, "case-correction-unknown", "adult")
	unknown.IdentityVerified = false
	if _, err = s.DB.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=false WHERE id=$1`, unknown.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProposeCorrection(ctx, unknown, place, "name", "x"); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("unverified correction", err)
	}
	private := placeFixture(t, s, "Unpublished proposal", "user", 23.6, 46.77)
	if _, err = s.ProposeCorrection(ctx, author, private, "name", "x"); err == nil {
		t.Fatal("nonpublic correction accepted")
	}
	capped, err := s.ProposeCorrection(ctx, author, place, "name", strings.Repeat("z", 400))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(ctx, `SELECT proposed_value FROM places_placecorrection WHERE id=$1`, capped).Scan(&value); err != nil || value != strings.Repeat("z", 255) {
		t.Fatal("correction cap drift", err)
	}
	if _, err = s.ProposeCorrection(ctx, peers[0], place, "name", "duplicate"); err == nil {
		t.Fatal("multiple pending corrections for same field")
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO places_placecorrection(place_id,proposer_id,field,proposed_value,required_confirmations,status,created_at,published_at) VALUES($1,$2,'name','duplicate',3,'pending',now(),NULL)`, place, peers[1].ID)
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || constraint.Code != "23505" {
		t.Fatal("missing native pending uniqueness constraint", err)
	}
}

func TestCasePortPostgresCorrectedHoursAreParsedAndRetireOnlySupersededReports(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	author := user(t, s, "case-hours-author", "adult")
	staff := user(t, s, "case-hours-staff", "adult")
	staff.IsStaff = true
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1`, staff.ID); err != nil {
		t.Fatal(err)
	}
	place := placeFixture(t, s, "Hours venue", "osm", 23.6, 46.77)
	s.Now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET opening_hours=NULL,opening_hours_raw='' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	if props := casePortPlaceProperties(t, s, place); props["opening_hours"] != nil || props["open_now"] != nil {
		t.Fatal("absent raw hours invented schedule")
	}
	if _, err := s.ReportVenue(ctx, author, place, false); err != nil {
		t.Fatal(err)
	}
	name, err := s.ProposeCorrection(ctx, author, place, "name", "New Name")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.StaffCorrection(ctx, staff, name, true, ""); err != nil {
		t.Fatal(err)
	}
	var reports int
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM places_opennowreport WHERE place_id=$1`, place).Scan(&reports); err != nil || reports != 1 {
		t.Fatal("name publish cleared hours report", reports, err)
	}
	hours, err := s.ProposeCorrection(ctx, author, place, "hours", "24/7")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.StaffCorrection(ctx, staff, hours, true, ""); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = s.DB.QueryRow(ctx, `SELECT proposed_value FROM places_placecorrection WHERE id=$1`, hours).Scan(&raw); err != nil || raw != "24/7" {
		t.Fatal("correction must retain raw posted text", err)
	}
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM places_opennowreport WHERE place_id=$1`, place).Scan(&reports); err != nil || reports != 0 {
		t.Fatal("hours publish retained superseded reports", reports, err)
	}
	props := casePortPlaceProperties(t, s, place)
	if props["open_now"] != true {
		t.Fatal("corrected schedule not used for live open state")
	}
	schedule, ok := props["opening_hours"].(map[string]any)
	if !ok || len(schedule) != 7 {
		t.Fatal("corrected hours are not a seven-day parsed object")
	}
	for _, day := range []string{"mo", "tu", "we", "th", "fr", "sa", "su"} {
		intervals, ok := schedule[day].([]any)
		if !ok || len(intervals) != 1 {
			t.Fatal("all-day interval missing", day)
		}
		pair, ok := intervals[0].([]any)
		if !ok || len(pair) != 2 || pair[0] != float64(0) || pair[1] != float64(1440) {
			t.Fatal("all-day interval differs", day)
		}
	}
	if _, err = s.DB.Exec(ctx, `DELETE FROM places_placecorrection WHERE place_id=$1 AND field='hours'`, place); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, `UPDATE places_place SET opening_hours='{"mo":[[540,1080]]}' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	props = casePortPlaceProperties(t, s, place)
	if props["open_now"] != true || len(props["opening_hours"].(map[string]any)) != 1 {
		t.Fatal("raw parsed schedule fallback lost")
	}
	quorumPlace := placeFixture(t, s, "Quorum hours", "osm", 23.6, 46.77)
	if _, err = s.DB.Exec(ctx, `UPDATE places_place SET opening_hours=NULL,opening_hours_raw='' WHERE id=$1`, quorumPlace); err != nil {
		t.Fatal(err)
	}
	quorumCorrection, err := s.ProposeCorrection(ctx, author, quorumPlace, "hours", "24/7")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		peer := user(t, s, fmt.Sprintf("case-hours-peer%d", i), "adult")
		if err = s.ConfirmCorrection(ctx, peer, quorumCorrection); err != nil {
			t.Fatal(err)
		}
	}
	var status, posted string
	if err = s.DB.QueryRow(ctx, `SELECT status,proposed_value FROM places_placecorrection WHERE id=$1`, quorumCorrection).Scan(&status, &posted); err != nil || status != "published" || posted != "24/7" {
		t.Fatal("quorum raw-hour publication", err)
	}
	quorumProps := casePortPlaceProperties(t, s, quorumPlace)
	if quorumProps["open_now"] != true || !reflect.DeepEqual(quorumProps["opening_hours"], schedule) {
		t.Fatal("quorum hours were not reparsed as exact all-day object")
	}
}
