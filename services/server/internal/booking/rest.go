package booking

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RESTProvider implements the existing demo_rest protocol. Non-idempotent create
// and cancel calls are never replayed after an ambiguous provider response.
type RESTProvider struct {
	BaseURL, APIKey string
	Client          *http.Client
	once            sync.Once
	resilient       *ops.Resilient
}
type Slot struct {
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Available bool      `json:"available"`
}

var errProvider = errors.New("booking provider unavailable")

func (p *RESTProvider) request(ctx context.Context, method, path string, params url.Values, body any, result any) error {
	base, err := url.Parse(p.BaseURL)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return errProvider
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	base.RawQuery = params.Encode()
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return errProvider
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(data))
	if err != nil {
		return errProvider
	}
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	// Even caller-supplied transports cannot forward provider credentials through a redirect.
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if bounded.Timeout == 0 {
		bounded.Timeout = 15 * time.Second
	}
	p.once.Do(func() { p.resilient = ops.NewResilient(&bounded) })
	resp, err := p.resilient.Do(ctx, req, ops.RequestOptions{MaxAttempts: 3, RetryStatuses: []int{500, 502, 503, 504}, RetryTimeouts: method == http.MethodGet, BreakerKey: "booking", MaxBodyBytes: 1 << 20})
	if err != nil {
		return errProvider
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errProvider
	}
	if result == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return err
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	decoder.UseNumber()
	if decoder.Decode(result) != nil || decoder.Decode(new(any)) != io.EOF {
		return errProvider
	}
	return nil
}
func (p *RESTProvider) Availability(ctx context.Context, place string, start, end time.Time) ([]Slot, error) {
	var result struct {
		Slots []json.RawMessage `json:"slots"`
	}
	if err := p.request(ctx, "GET", "/availability", url.Values{"venue": {place}, "from": {start.Format(time.RFC3339Nano)}, "to": {end.Format(time.RFC3339Nano)}}, nil, &result); err != nil {
		return nil, err
	}
	slots := []Slot{}
	for _, raw := range result.Slots {
		slot := Slot{Available: true}
		if json.Unmarshal(raw, &slot) != nil || slot.Start.IsZero() || slot.End.IsZero() {
			return nil, errProvider
		}
		slots = append(slots, slot)
	}
	return slots, nil
}
func (p *RESTProvider) Create(ctx context.Context, place, user string, input Request) (Result, error) {
	var result struct {
		ID        any    `json:"id"`
		BookingID any    `json:"booking_id"`
		Status    string `json:"status"`
	}
	err := p.request(ctx, "POST", "/bookings", nil, map[string]any{"venue": place, "start": input.StartsAt, "end": input.EndsAt, "party_size": input.PartySize, "customer_ref": user}, &result)
	if err != nil {
		return Result{}, err
	}
	id := result.ID
	if id == nil {
		id = result.BookingID
	} else if text, ok := id.(string); ok && text == "" {
		id = result.BookingID
	}
	ref := ""
	switch v := id.(type) {
	case string:
		ref = v
	case float64:
		raw, _ := json.Marshal(v)
		ref = string(raw)
	case json.Number:
		ref = v.String()
	}
	if ref == "" || len(ref) > 128 {
		return Result{}, errProvider
	}
	return Result{Reference: ref, Confirmed: result.Status == "confirmed"}, nil
}
func (p *RESTProvider) Cancel(ctx context.Context, reference string) error {
	if reference == "" || strings.ContainsAny(reference, "/\\\r\n") {
		return errProvider
	}
	return p.request(ctx, "POST", "/bookings/"+url.PathEscape(reference)+"/cancel", nil, map[string]any{}, nil)
}
