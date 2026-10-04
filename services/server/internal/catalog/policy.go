package catalog

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Policy is immutable after service assembly. Reliability reports do not change
// child-venue approval, cohort, consent or public-listing gates.
type Policy struct {
	ClosureReportThreshold int
	ClosureReportDecay     time.Duration
	OpenNowReportThreshold int
	OpenNowReportDecay     time.Duration
	FactQuorum             int
	CorrectionQuorum       int
	EdgeQuorum             int
	EventReportThreshold   int
	EventReportDecay       time.Duration
}

func DefaultPolicy() Policy {
	return Policy{
		ClosureReportThreshold: 3, ClosureReportDecay: 14 * 24 * time.Hour,
		OpenNowReportThreshold: 3, OpenNowReportDecay: 14 * 24 * time.Hour,
		FactQuorum: 3, CorrectionQuorum: 3, EdgeQuorum: 3,
		EventReportThreshold: 3, EventReportDecay: 14 * 24 * time.Hour,
	}
}

// WithDefaults treats a zero field as an omitted programmatic setting. Explicit
// environment values are validated by the configuration boundary before assembly.
func (p Policy) WithDefaults() Policy {
	d := DefaultPolicy()
	if p.ClosureReportThreshold == 0 {
		p.ClosureReportThreshold = d.ClosureReportThreshold
	}
	if p.ClosureReportDecay == 0 {
		p.ClosureReportDecay = d.ClosureReportDecay
	}
	if p.OpenNowReportThreshold == 0 {
		p.OpenNowReportThreshold = d.OpenNowReportThreshold
	}
	if p.OpenNowReportDecay == 0 {
		p.OpenNowReportDecay = d.OpenNowReportDecay
	}
	if p.FactQuorum == 0 {
		p.FactQuorum = d.FactQuorum
	}
	if p.CorrectionQuorum == 0 {
		p.CorrectionQuorum = d.CorrectionQuorum
	}
	if p.EdgeQuorum == 0 {
		p.EdgeQuorum = d.EdgeQuorum
	}
	if p.EventReportThreshold == 0 {
		p.EventReportThreshold = d.EventReportThreshold
	}
	if p.EventReportDecay == 0 {
		p.EventReportDecay = d.EventReportDecay
	}
	return p
}

func (p Policy) Validate() error {
	for _, v := range []struct {
		name string
		n    int
		min  int
		max  int
	}{
		{"CLOSURE_REPORT_THRESHOLD", p.ClosureReportThreshold, 1, 3},
		{"OPEN_NOW_REPORT_THRESHOLD", p.OpenNowReportThreshold, 1, 3},
		{"FACT_QUORUM", p.FactQuorum, 3, 100},
		{"CORRECTION_QUORUM", p.CorrectionQuorum, 3, 100},
		{"EDGE_QUORUM", p.EdgeQuorum, 3, 100},
		{"EVENT_REPORT_THRESHOLD", p.EventReportThreshold, 1, 3},
	} {
		if v.n < v.min || v.n > v.max {
			return fmt.Errorf("%s must be between %d and %d", v.name, v.min, v.max)
		}
	}
	for _, v := range []struct {
		name string
		d    time.Duration
	}{
		{"CLOSURE_REPORT_DECAY_SECONDS", p.ClosureReportDecay},
		{"OPEN_NOW_REPORT_DECAY_SECONDS", p.OpenNowReportDecay},
		{"EVENT_REPORT_DECAY_SECONDS", p.EventReportDecay},
	} {
		if v.d < 14*24*time.Hour || v.d > 365*24*time.Hour || v.d%time.Second != 0 {
			return fmt.Errorf("%s must be whole seconds between 1209600 and 31536000", v.name)
		}
	}
	return nil
}

func intervalSQL(d time.Duration) string {
	return "interval '" + strconv.FormatInt(int64(d/time.Second), 10) + " seconds'"
}

// PlaceSQL is the shared public-place gate. Only typed numbers enter SQL; no
// environment text or user data is interpolated into this expression.
func (p Policy) PlaceSQL() string {
	p = p.WithDefaults()
	if p.Validate() != nil {
		return "false"
	}
	if p.ClosureReportThreshold == 3 && p.ClosureReportDecay == 14*24*time.Hour {
		return PublicPlaceSQL
	}
	return `(p.source<>'user' OR EXISTS(SELECT 1 FROM social_userplaceproposal pp WHERE pp.place_id=p.id AND pp.status='published')) AND NOT EXISTS(SELECT 1 FROM places_placeclosurereport cr WHERE cr.place_id=p.id AND cr.created_at>=now()-` + intervalSQL(p.ClosureReportDecay) + ` GROUP BY cr.place_id HAVING count(*)>=` + strconv.Itoa(p.ClosureReportThreshold) + `)`
}

func (p Policy) EventSQL() string {
	p = p.WithDefaults()
	if p.Validate() != nil {
		return "false"
	}
	return `NOT e.is_tombstone AND NOT e.is_import_held AND (e.place_id IS NULL OR (` + p.PlaceSQL() + `))`
}

func (s *Service) policy() Policy { return s.Policy.WithDefaults() }

type policyContextKey struct{}

// WithPolicy carries one runtime's immutable catalog policy through domain,
// discovery and export calls without process-global mutable visibility state.
func WithPolicy(ctx context.Context, p Policy) context.Context {
	return context.WithValue(ctx, policyContextKey{}, p.WithDefaults())
}

// PolicyFromContext defaults only when no runtime policy was supplied. Invalid
// supplied policies retain their fail-closed PlaceSQL/EventSQL behavior.
func PolicyFromContext(ctx context.Context) Policy {
	if p, ok := ctx.Value(policyContextKey{}).(Policy); ok {
		return p
	}
	return DefaultPolicy()
}
