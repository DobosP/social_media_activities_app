package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestNativeMessengerBootstrapKeepsCohortBlockAndCryptoShape(t *testing.T) {
	if *webDomainDSN == "" {
		t.Skip("explicit web fixture DSN required")
	}
	db := testdb.New(t, *webDomainDSN, nil)
	ctx := context.Background()
	a := testdb.Actor(t, db, "messenger-owner", "adult")
	good := testdb.Actor(t, db, "same-cohort-connection", "adult")
	blocked := testdb.Actor(t, db, "blocked-connection", "adult")
	minor := testdb.Actor(t, db, "cross-cohort-connection", "child")
	for _, other := range []platform.Actor{good, blocked, minor} {
		if _, err := db.Exec(ctx, `INSERT INTO connections_connection(requester_id,addressee_id,status,created_at,decided_at) VALUES($1,$2,'accepted',now(),now())`, a.ID, other.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, blocked.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../../../../")
	s := &Server{DB: db, Social: social.New(db, platform.RecordAudit), Renderer: NewRenderer(root)}
	r := platform.WithActor(httptest.NewRequest("GET", "/messages/", nil), a)
	data, template, err := s.messagingView(r, a)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(data["messaging_config"])
	for _, want := range []string{a.PublicID, good.PublicID, `"username":"same-cohort-connection"`, `"reaction_emojis":["👍","❤️","🎉","👏","🙏"]`, `data:image/svg+xml`} {
		if !strings.Contains(string(raw), want) {
			t.Fatal("messenger bootstrap missing", want)
		}
	}
	for _, private := range []string{blocked.PublicID, minor.PublicID, `"age_band":`, `"cohort":`, "private_key", "passphrase"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("messenger bootstrap leak", private)
		}
	}
	data["csrf"] = "synthetic-csrf"
	w := httptest.NewRecorder()
	if err := s.Renderer.Render(w, r, template, data); err != nil {
		t.Fatal(err)
	}
	html := w.Body.String()
	for _, want := range []string{`id="mz-config"`, `type="application/json"`, `/static/js/e2ee-messaging.js`, `id="mz-backup"`, `id="mz-composer"`, `id="mz-guardian-list"`} {
		if !strings.Contains(html, want) {
			t.Fatal("E2EE client hook missing", want)
		}
	}
	if data["can_create_group"] != false {
		t.Fatal("ordinary user group affordance bypass")
	}
}
