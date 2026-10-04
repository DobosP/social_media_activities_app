package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

type PlainCallbacks struct {
	Authorize   func(context.Context, platform.Actor, int64) (bool, error)
	Write       func(context.Context, platform.Actor, int64, string, *int64) error
	Typing      func(context.Context, platform.Actor, int64) (map[string]any, error)
	Post        func(context.Context, platform.Actor, int64) (map[string]any, error)
	Attachments func(context.Context, platform.Actor, int64) ([]any, error)
}

// PlainAdapter delegates durable content and permissions to the social/media
// owners. Group notifications contain only IDs; signed URLs are issued per viewer.
func PlainAdapter(b *Broker, c PlainCallbacks) (Adapter, error) {
	if b == nil || c.Authorize == nil || c.Write == nil || c.Typing == nil || c.Post == nil || c.Attachments == nil {
		return Adapter{}, ErrUnavailable
	}
	return Adapter{
		Authorize: c.Authorize,
		Receive: func(ctx context.Context, a platform.Actor, id int64, raw json.RawMessage, sender string) error {
			var body struct {
				Type  string          `json:"type,omitempty"`
				Body  string          `json:"body"`
				Reply json.RawMessage `json:"reply_to,omitempty"`
			}
			d := json.NewDecoder(bytes.NewReader(raw))
			d.DisallowUnknownFields()
			if d.Decode(&body) != nil || d.Decode(new(any)) != io.EOF {
				return platform.ErrInvalid
			}
			if body.Type == "typing" {
				info, e := c.Typing(ctx, a, id)
				if e != nil || info == nil {
					return nil
				}
				_ = b.Publish(ctx, Event{Kind: "chat", Event: "typing", RoomID: id, ActorID: a.ID, Sender: sender})
				return nil
			}
			if body.Type != "" && body.Type != "message" {
				return platform.ErrInvalid
			}
			var reply *int64
			if len(body.Reply) > 0 && string(body.Reply) != "null" {
				var n int64
				if json.Unmarshal(body.Reply, &n) != nil {
					var text string
					if json.Unmarshal(body.Reply, &text) == nil {
						n, _ = strconv.ParseInt(text, 10, 64)
					}
				}
				if n > 0 {
					reply = &n
				}
			}
			return c.Write(ctx, a, id, body.Body, reply)
		},
		Payload: func(ctx context.Context, a platform.Actor, event Event) (map[string]any, error) {
			if event.Kind != "chat" {
				return nil, platform.ErrInvalid
			}
			if event.Event == "typing" {
				info, e := c.Typing(ctx, platform.Actor{ID: event.ActorID}, event.RoomID)
				if e != nil || info == nil {
					return nil, e
				}
				return map[string]any{"type": "typing", "author_id": event.ActorID, "author": info["author"]}, nil
			}
			items, e := c.Attachments(ctx, a, event.MessageID)
			if e != nil {
				return nil, e
			}
			if event.Event == "attachments" {
				return map[string]any{"type": "attachments", "post_id": event.MessageID, "attachments": items}, nil
			}
			post, e := c.Post(ctx, a, event.MessageID)
			if e != nil {
				return nil, e
			}
			if post == nil {
				return nil, platform.ErrNotFound
			}
			delete(post, "attachment_ids")
			post["attachments"] = items
			post["type"] = "message"
			return post, nil
		},
	}, nil
}
