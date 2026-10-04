package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// Recorders cannot expose net/http's unread-body drain. This fixture writes
// headers over real TCP, deliberately sends ZERO body bytes, and requires the
// final refusal/connection close before the server's ten-second read timeout.
func TestPostgresNativeAppTCPRefusesBodyWithoutUpload(t *testing.T) {
	db := testdb.New(t, *appDSN, nil)
	ctx := context.Background()
	config := integrationConfig(t)
	config.MaxRequestBodyBytes, config.DataUploadMemoryBytes = 1024, 1024
	a, err := New(ctx, db, config, true)
	if err != nil {
		t.Fatal(err)
	}
	owner := testdb.Actor(t, db, "tcp-owner", "adult")
	peer := testdb.Actor(t, db, "tcp-peer", "adult")
	place := testdb.Place(t, db, "TCP synthetic venue", "osm")
	var typ int64
	if err = db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	activity, err := a.Social.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "TCP synthetic activity", StartsAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	csrf := strings.Repeat("c", 52)
	cookieFor := func(actor platform.Actor, value string) string {
		t.Helper()
		sum := sha256.Sum256([]byte(value))
		if err := a.Store.CreateSession(ctx, authcore.Session{TokenHash: hex.EncodeToString(sum[:]), UserID: strconv.FormatInt(actor.ID, 10), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		return "sessionid=" + value + "; csrftoken=" + csrf
	}
	ownerCookie := cookieFor(owner, strings.Repeat("o", 52))
	peerCookie := cookieFor(peer, strings.Repeat("p", 52))
	server := httptest.NewUnstartedServer(a)
	server.Config.ReadHeaderTimeout = time.Second
	server.Config.ReadTimeout = 10 * time.Second
	server.Config.WriteTimeout = 10 * time.Second
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.Start()
	t.Cleanup(server.Close)
	cases := []struct {
		name, method, path, mime, cookie, authorization, csrf string
		length, want                                          int
	}{
		{"unauthenticated-social", "POST", "/api/v1/social/activities/", "application/json", "", "", "", 1000, 401},
		{"unauthenticated-upload", "POST", "/api/v1/media/photos/", "multipart/form-data; boundary=fixture", "", "", "", 1000, 401},
		{"invalid-current-token", "POST", "/api/v1/media/photos/", "multipart/form-data; boundary=fixture", "", "Token " + strings.Repeat("a", 40), "", 1000, 401},
		{"cookie-json-csrf", "POST", "/api/v1/social/activities/", "application/json", ownerCookie, "", "", 1000, 403},
		{"moderator-role-before-decode", "POST", "/api/v1/safety/moderation/reports/1/resolve/", "application/json", ownerCookie, "", csrf, 1000, 403},
		{"cover-owner-before-upload", "PUT", fmt.Sprintf("/api/v1/media/activity-covers/%d/", activity), "multipart/form-data; boundary=fixture", peerCookie, "", csrf, 1000, 403},
		{"declared-size-before-auth", "POST", "/api/v1/social/activities/", "application/json", "", "Token " + strings.Repeat("a", 40), "", 1025, 413},
		{"declared-get-size", "GET", "/api/v1/events/", "application/json", "", "", "", 1025, 413},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal("synthetic TCP connection failed")
			}
			defer conn.Close()
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			request := fmt.Sprintf("%s %s HTTP/1.1\r\nHost: app.example\r\nContent-Type: %s\r\nContent-Length: %d\r\nOrigin: https://app.example\r\n", item.method, item.path, item.mime, item.length)
			if item.cookie != "" {
				request += "Cookie: " + item.cookie + "\r\n"
			}
			if item.authorization != "" {
				request += "Authorization: " + item.authorization + "\r\n"
			}
			if item.csrf != "" {
				request += "X-CSRFToken: " + item.csrf + "\r\n"
			}
			request += "\r\n"
			if _, err = io.WriteString(conn, request); err != nil {
				t.Fatal("synthetic request headers failed")
			}
			response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: item.method})
			if err != nil {
				t.Fatal("refusal waited for unsent body bytes or server read timeout")
			}
			defer response.Body.Close()
			if response.StatusCode != item.want {
				t.Fatalf("header-only refusal status=%d want=%d", response.StatusCode, item.want)
			}
			body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
			if err != nil || len(body) > 4096 {
				t.Fatal("refusal body failed bounded delivery")
			}
			if !response.Close {
				t.Fatal("body-bearing refusal did not close unread request connection")
			}
			if response.Header.Get("X-Social-Runtime") != "go" {
				t.Fatal("refusal bypassed native application")
			}
		})
	}
}
