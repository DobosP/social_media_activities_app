package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/DobosP/social_media_activities_app/services/server/internal/chat"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// LiveAdapter relays the same durable ciphertext rows as REST. Notifications
// contain IDs; every delivery resolves the recipient's current authorization.
func (s *Service) LiveAdapter() chat.Adapter {
	return chat.Adapter{
		Authorize: func(ctx context.Context, a platform.Actor, id int64) (bool, error) {
			return s.CanView(ctx, s.DB, a, id)
		},
		Receive: func(ctx context.Context, a platform.Actor, id int64, raw json.RawMessage, _ string) error {
			var body struct {
				Type string `json:"type,omitempty"`
				MessageInput
			}
			d := json.NewDecoder(bytes.NewReader(raw))
			d.DisallowUnknownFields()
			if d.Decode(&body) != nil || d.Decode(new(any)) != io.EOF || body.Type != "" && body.Type != "message" {
				return platform.ErrInvalid
			}
			_, e := s.Post(ctx, a, id, body.MessageInput)
			return e
		},
		Payload: func(ctx context.Context, a platform.Actor, event chat.Event) (map[string]any, error) {
			if event.Kind != "messaging" || event.Event != "message" {
				return nil, platform.ErrInvalid
			}
			message, e := s.Message(ctx, a, event.MessageID, true)
			if e != nil {
				return nil, e
			}
			if message["conversation"] != event.RoomID {
				return nil, platform.ErrForbidden
			}
			message["type"] = "message"
			return message, nil
		},
	}
}
