package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
	"github.com/jackc/pgx/v5"
)

// populatePersonContext runs only after Social.Profile has authorized the card.
// It supplies the native template aliases and an internal target ID for guarded
// report/block forms. Optional profile photos still use media's current gate and
// signer, and remain exclusive to the connected-adult full-page surface.
func (s *Server) populatePersonContext(r *http.Request, card map[string]any, data pongo2.Context, fullPage bool) error {
	publicID, ok := card["public_id"].(string)
	if !ok || publicID == "" {
		return platform.ErrNotFound
	}
	var targetID int64
	if err := s.DB.QueryRow(r.Context(), `SELECT id FROM accounts_user WHERE public_id::text=$1 AND is_active`, publicID).Scan(&targetID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrNotFound
		}
		return err
	}
	data["card"] = card
	data["person_user"] = map[string]any{"id": targetID}
	if !fullPage || card["show_photo"] != true || s.Media == nil || s.API == nil {
		return nil
	}
	var photoID int64
	err := s.DB.QueryRow(r.Context(), `SELECT id FROM media_photo WHERE uploader_id=$1 AND kind='profile' AND scan_status='clean' ORDER BY created_at DESC,id DESC LIMIT 1`, targetID).Scan(&photoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	photo, err := s.get(r, "/api/media/photos/"+strconv.FormatInt(photoID, 10)+"/")
	if err != nil {
		if errors.Is(err, platform.ErrForbidden) || errors.Is(err, platform.ErrNotFound) {
			return platform.ErrNotFound
		}
		return err
	}
	data["photo_url"] = object(photo)["url"]
	return nil
}
