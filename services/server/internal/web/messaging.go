package web

import (
	"encoding/json"
	"net/http"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// messagingView preserves the original E2EE browser bootstrap. Only public
// identities and generated avatars cross this page; no private key or clear
// message body is available to the controller.
func (s *Server) messagingView(r *http.Request, a platform.Actor) (pongo2.Context, string, error) {
	connections, err := s.Social.Connections(r.Context(), a)
	if err != nil {
		return nil, "", err
	}
	publicIDs := []string{}
	for _, raw := range connections {
		var connection struct {
			PublicID string `json:"public_id"`
		}
		if err := json.Unmarshal(raw, &connection); err != nil {
			return nil, "", err
		}
		publicIDs = append(publicIDs, connection.PublicID)
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id,public_id::text,username,coalesce(nullif(display_name,''),username) FROM accounts_user WHERE public_id::text=ANY($1) ORDER BY display_name,username,id`, publicIDs)
	if err != nil {
		return nil, "", err
	}
	conns := []map[string]any{}
	userIDs := []int64{a.ID}
	for rows.Next() {
		var id int64
		var publicID, username, displayName string
		if err := rows.Scan(&id, &publicID, &username, &displayName); err != nil {
			rows.Close()
			return nil, "", err
		}
		conns = append(conns, map[string]any{"id": id, "public_id": publicID, "username": username, "display_name": displayName})
		userIDs = append(userIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, "", err
	}
	avatars, err := accounts.Avatars(r.Context(), s.DB, userIDs)
	if err != nil {
		return nil, "", err
	}
	for _, connection := range conns {
		connection["avatar"] = avatars[connection["id"].(int64)]
		delete(connection, "id")
	}
	displayName := a.DisplayName
	if displayName == "" {
		displayName = a.Username
	}
	config := map[string]any{"me": map[string]any{"public_id": a.PublicID, "username": a.Username, "display_name": displayName, "avatar": avatars[a.ID]}, "connections": conns, "reaction_emojis": []string{"👍", "❤️", "🎉", "👏", "🙏"}}
	return pongo2.Context{"messaging_config": config, "can_create_group": a.IsStaff || a.Cohort == "adult" && s.Social.AllowUserGroups}, "web/messages.html", nil
}
