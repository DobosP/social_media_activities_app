package web

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func webCasePort3Member(t *testing.T, s *Server, owner platform.Actor, activity int64, name string) platform.Actor {
	t.Helper()
	member := testdb.Actor(t, s.DB, name, "adult")
	membership, err := s.Social.Join(context.Background(), member, activity)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Social.Vote(context.Background(), owner, membership, true, false); err != nil {
		t.Fatal(err)
	}
	return member
}

func TestWebCasePort3MemberLogisticsRSVPAndPresence(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	activity, err := s.Social.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Case port3 near meetup", StartsAt: time.Now().Add(5 * time.Minute), MeetingPoint: "North gate by the fountain", WhatToBring: "Water"})
	if err != nil {
		t.Fatal(err)
	}
	member := webCasePort3Member(t, s, owner, activity, "case-port3-controls-member")
	path := fmt.Sprintf("/activities/%d/", activity)
	t.Run("logistics_visible_to_member_hidden_from_peer", func(t *testing.T) {
		webCasePortContains(t, webCasePortHTML(t, mux, member, path), "Meetup logistics", "North gate by the fountain")
		stranger := testdb.Actor(t, s.DB, "case-port3-controls-stranger", "adult")
		webCasePortAbsent(t, webCasePortHTML(t, mux, stranger, path), "North gate by the fountain")
	})
	t.Run("rsvp_updates_exact_member_count", func(t *testing.T) {
		w := webCasePort2Post(t, mux, member, path, path+"rsvp/", url.Values{"intent": {"going"}})
		if w.Code != 302 {
			t.Fatal("source RSVP redirect", w.Code, w.Body.String())
		}
		webCasePortContains(t, webCasePortHTML(t, mux, member, path), "Coming:", "1</strong> of 2")
	})
	// The transit assertion precedes arrival: arrival intentionally hides both
	// transit controls and must not make the progression test pass accidentally.
	t.Run("transit_forward_only_html", func(t *testing.T) {
		webCasePortContains(t, webCasePortHTML(t, mux, member, path), "On my way", "Running ~10 min late")
		w := webCasePort2Post(t, mux, member, path, path+"transit/", url.Values{"status": {"on_my_way"}})
		if w.Code != 302 {
			t.Fatal("source transit redirect", w.Code, w.Body.String())
		}
		body := webCasePortHTML(t, mux, member, path)
		webCasePortContains(t, body, "you're on the way", "Running ~10 min late")
		webCasePortAbsent(t, body, ">&#128694; On my way</button>")
	})
	t.Run("arrival_button_then_confirmation", func(t *testing.T) {
		webCasePortContains(t, webCasePortHTML(t, mux, member, path), "I've arrived")
		w := webCasePort2Post(t, mux, member, path, path+"arrived/", url.Values{})
		if w.Code != 302 {
			t.Fatal("source arrival redirect", w.Code, w.Body.String())
		}
		webCasePortContains(t, webCasePortHTML(t, mux, member, path), "marked yourself here")
	})
	t.Run("future_window_hides_arrival_and_transit", func(t *testing.T) {
		future, err := s.Social.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Case port3 distant meetup", StartsAt: time.Now().Add(72 * time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		futureMember := webCasePort3Member(t, s, owner, future, "case-port3-future-member")
		webCasePortAbsent(t, webCasePortHTML(t, mux, futureMember, fmt.Sprintf("/activities/%d/", future)), "I've arrived", "On my way")
	})
}

func TestWebCasePort3MetCardLifecycleAndExactCount(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	activity, err := s.Social.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Case port3 finished meetup", StartsAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	member := webCasePort3Member(t, s, owner, activity, "case-port3-met-member")
	path := fmt.Sprintf("/activities/%d/", activity)
	webCasePortAbsent(t, webCasePortHTML(t, mux, member, path), "Did this meet up?")
	if completed, err := s.Social.AutoComplete(ctx, time.Now().Add(time.Hour), 0); err != nil || completed != 1 {
		t.Fatal("fixture completion through native lifecycle", err, completed)
	}
	webCasePortContains(t, webCasePortHTML(t, mux, member, path), "Did this meet up?")
	w := webCasePort2Post(t, mux, member, path, path+"met/", url.Values{})
	if w.Code != 302 {
		t.Fatal("source did-we-meet redirect", w.Code, w.Body.String())
	}
	webCasePortContains(t, webCasePortHTML(t, mux, member, path), "you confirmed", "Confirmed: <strong>1</strong> of 2")
}
