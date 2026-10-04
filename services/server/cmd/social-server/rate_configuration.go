package main

import (
	"errors"
	"github.com/DobosP/social_media_activities_app/services/server/internal/app"
	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"strings"
	"time"
)

type configuredRate struct {
	Limit  int
	Window time.Duration
}

func (c runtimeConfig) applyRates(a *app.App) error {
	// Required mode asserts that every configurable admission service was
	// assembled against the same PostgreSQL pool. This is also checked for
	// manually assembled applications; an absent/mismatched store is never a
	// reason to fall back to a local budget.
	if c.RequireSharedState {
		if a == nil || a.DB == nil || a.Accounts == nil || a.Messaging == nil || a.Safety == nil || a.Social == nil || a.Catalog == nil || a.Recommendations == nil || a.Accounts.DB != a.DB || a.Messaging.DB != a.DB || a.Safety.DB != a.DB || a.Social.DB != a.DB || a.Catalog.DB != a.DB || a.Recommendations.DB != a.DB ||
			a.Social.Budgets == nil || a.Social.Budgets.DB != a.DB || a.Catalog.Budgets == nil || a.Catalog.Budgets.DB != a.DB || a.Recommendations.Budgets == nil || a.Recommendations.Budgets.DB != a.DB {
			return errors.New("DJANGO_REQUIRE_SHARED_STATE requires coherent PostgreSQL admission")
		}
	}
	targets := map[string]map[string]budgets.Policy{}
	for key, value := range c.Rates {
		target, action, _ := strings.Cut(key, "/")
		policy := budgets.Policy{Limit: value.Limit, Window: value.Window}
		if err := policy.Valid(); err != nil {
			return err
		}
		if targets[target] == nil {
			targets[target] = map[string]budgets.Policy{}
		}
		targets[target][action] = policy
	}
	a.Social.RatePolicies = targets["social"]
	a.Catalog.RatePolicies = targets["catalog"]
	a.Recommendations.RatePolicies = targets["recommendations"]
	a.Accounts.RatePolicies = targets["accounts"]
	a.Messaging.RatePolicies = targets["messaging"]
	a.Safety.RatePolicies = targets["safety"]
	return nil
}

// Hooks are applied to the PostgreSQL-backed action policies before serving.
// Keeping the inventory together prevents an accepted override from losing its
// matching domain action or silently changing a different budget.
func configureRates(d *decoder) map[string]configuredRate {
	out := map[string]configuredRate{}
	for _, entry := range []struct {
		target, action, limitName, windowName string
		limit, seconds                        int
	}{
		{"social", "thread_post", "THREAD_POST_RATE_LIMIT", "THREAD_POST_RATE_WINDOW_SECONDS", 30, 60},
		{"social", "thread_react", "THREAD_REACT_RATE_LIMIT", "THREAD_REACT_RATE_WINDOW_SECONDS", 60, 60},
		{"social", "connection_request", "CONNECTIONS_REQUEST_RATE_LIMIT", "CONNECTIONS_REQUEST_RATE_WINDOW_SECONDS", 20, 3600},
		{"social", "group_create", "GROUP_CREATE_RATE_LIMIT", "GROUP_CREATE_RATE_WINDOW_SECONDS", 5, 3600},
		{"social", "group_join", "GROUP_JOIN_RATE_LIMIT", "GROUP_JOIN_RATE_WINDOW_SECONDS", 20, 3600},
		{"social", "group_question", "GROUP_QUESTION_RATE_LIMIT", "GROUP_QUESTION_RATE_WINDOW_SECONDS", 6, 3600},
		{"catalog", "open_now_report", "OPEN_NOW_REPORT_RATE_LIMIT", "OPEN_NOW_REPORT_RATE_WINDOW_SECONDS", 10, 3600},
		{"catalog", "place_closure_report", "CLOSURE_REPORT_RATE_LIMIT", "CLOSURE_REPORT_RATE_WINDOW_SECONDS", 10, 3600},
		{"catalog", "place_fact_vote", "FACT_VOTE_RATE_LIMIT", "FACT_VOTE_RATE_WINDOW_SECONDS", 40, 3600},
		{"catalog", "event_report", "EVENT_REPORT_RATE_LIMIT", "EVENT_REPORT_RATE_WINDOW_SECONDS", 10, 3600},
		{"recommendations", "saved_search_create", "SAVED_SEARCH_RATE_LIMIT", "SAVED_SEARCH_RATE_WINDOW_SECONDS", 20, 3600},
		{"accounts", "guardian_invite", "GUARDIAN_INVITE_RATE_LIMIT", "GUARDIAN_INVITE_RATE_WINDOW_SECONDS", 20, 3600},
		{"accounts", "guardian_guardrail", "GUARDIAN_GUARDRAIL_RATE_LIMIT", "GUARDIAN_GUARDRAIL_RATE_WINDOW_SECONDS", 30, 3600},
		{"accounts", "guardian_ward_topics", "GUARDIAN_GUARDRAIL_RATE_LIMIT", "GUARDIAN_GUARDRAIL_RATE_WINDOW_SECONDS", 30, 3600},
		{"messaging", "messaging_start", "MESSAGING_START_RATE_LIMIT", "MESSAGING_RATE_WINDOW_SECONDS", 20, 60},
		{"messaging", "messaging_send", "MESSAGING_SEND_RATE_LIMIT", "MESSAGING_RATE_WINDOW_SECONDS", 60, 60},
		{"safety", "unsafe_report", "UNSAFE_REPORT_RATE_LIMIT", "UNSAFE_REPORT_RATE_WINDOW_SECONDS", 12, 3600},
	} {
		out[entry.target+"/"+entry.action] = configuredRate{
			Limit:  d.integer(entry.limitName, entry.limit, 1, 10000),
			Window: d.seconds(entry.windowName, entry.seconds, 1, 86400),
		}
	}
	return out
}
