package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/commands"
	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
)

type invocation struct {
	Name                 string
	Options              map[string]json.RawMessage
	Limit, Hours         *int
	WindowHours, MaxURLs *int
	City                 string
}

func jobInvocation(name string, options map[string]json.RawMessage) (invocation, error) {
	call := invocation{Name: name, Options: options}
	if name == "" {
		return call, nil
	}
	for _, manual := range commands.Names() {
		if name == manual {
			return call, nil
		}
	}
	known := false
	for _, due := range jobs.DueNames {
		if name == due {
			known = true
			break
		}
	}
	if !known {
		return call, errors.New("--job is not registered")
	}
	for _, value := range options {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return call, errors.New("--job-options is invalid")
		}
	}
	var shape any
	switch name {
	case "purge_expired_attachments", "transcode_videos", "process_deferred_tasks":
		shape = &struct {
			Limit *int `json:"limit"`
		}{}
	case "auto_complete_activities":
		shape = &struct {
			Hours *int `json:"grace_hours"`
		}{}
	case "expire_arrivals":
		shape = &struct {
			Hours *int `json:"retention_hours"`
		}{}
	case "send_activity_reminders":
		shape = &struct {
			Hours *int `json:"within_hours"`
		}{}
	case "sync_roedu":
		shape = &struct {
			City string `json:"city"`
		}{}
	case "indexnow_batch_submit":
		shape = &struct {
			WindowHours *int `json:"window_hours"`
			MaxURLs     *int `json:"max_urls"`
		}{}
	default:
		if len(options) > 0 {
			return call, errors.New("--job-options is unsupported for this due job")
		}
		return call, nil
	}
	raw, err := json.Marshal(options)
	if err != nil {
		return call, errors.New("--job-options is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(shape) != nil {
		return call, errors.New("--job-options is invalid or contains unsupported fields")
	}
	switch v := shape.(type) {
	case *struct {
		Limit *int `json:"limit"`
	}:
		call.Limit = v.Limit
		max := 10000
		if name == "purge_expired_attachments" {
			max = 1000
		}
		if name == "transcode_videos" {
			max = 50
		}
		if v.Limit != nil && (*v.Limit < 1 || *v.Limit > max) {
			return call, errors.New("--job-options limit is invalid")
		}
	case *struct {
		Hours *int `json:"grace_hours"`
	}:
		call.Hours = v.Hours
	case *struct {
		Hours *int `json:"retention_hours"`
	}:
		call.Hours = v.Hours
		if v.Hours != nil && *v.Hours < 4 {
			return call, errors.New("--job-options retention_hours is invalid")
		}
	case *struct {
		Hours *int `json:"within_hours"`
	}:
		call.Hours = v.Hours
		if v.Hours != nil && *v.Hours < 1 {
			return call, errors.New("--job-options within_hours is invalid")
		}
	case *struct {
		City string `json:"city"`
	}:
		call.City = v.City
		if len(v.City) > 160 || strings.TrimSpace(v.City) != v.City || strings.ContainsAny(v.City, "\r\n\x00") {
			return call, errors.New("--job-options city is invalid")
		}
	case *struct {
		WindowHours *int `json:"window_hours"`
		MaxURLs     *int `json:"max_urls"`
	}:
		call.WindowHours, call.MaxURLs = v.WindowHours, v.MaxURLs
		if v.WindowHours != nil && (*v.WindowHours < 1 || *v.WindowHours > 24*365) || v.MaxURLs != nil && (*v.MaxURLs < 1 || *v.MaxURLs > 1000) {
			return call, errors.New("--job-options IndexNow bounds are invalid")
		}
	}
	if call.Hours != nil && (*call.Hours < 0 || *call.Hours > 24*365) {
		return call, errors.New("--job-options hours is invalid")
	}
	return call, nil
}

func (call invocation) run(ctx context.Context, r *jobs.Runner) (any, error) {
	switch call.Name {
	case "purge_expired_attachments":
		if call.Limit == nil {
			return r.Run(ctx, call.Name, nil)
		}
		n, err := r.Config.Media.PurgeExpiredAttachments(ctx, *call.Limit)
		if err != nil {
			return nil, err
		}
		_, err = r.Config.Media.DrainBlobDeletions(ctx, *call.Limit)
		return n, err
	case "transcode_videos":
		if call.Limit != nil {
			return r.Config.Media.ProcessPendingVideos(ctx, *call.Limit)
		}
	case "process_deferred_tasks":
		if call.Limit != nil {
			return r.Queue.RunPending(ctx, *call.Limit)
		}
	case "auto_complete_activities":
		if call.Hours != nil {
			return r.Config.Social.AutoComplete(ctx, r.Config.Now(), time.Duration(*call.Hours)*time.Hour)
		}
	case "expire_arrivals":
		if call.Hours != nil {
			return r.Config.Social.ExpirePresence(ctx, r.Config.Now(), time.Duration(*call.Hours)*time.Hour)
		}
	case "send_activity_reminders":
		if call.Hours != nil {
			r.Config.ReminderHours = *call.Hours
		}
	case "sync_roedu":
		if call.City != "" {
			r.Config.RoeduCity = call.City
		}
	case "indexnow_batch_submit":
		if call.WindowHours != nil {
			r.Config.IndexNowWindowHours = *call.WindowHours
		}
		if call.MaxURLs != nil {
			r.Config.IndexNowMaxURLs = *call.MaxURLs
		}
	}
	return r.Run(ctx, call.Name, call.Options)
}
