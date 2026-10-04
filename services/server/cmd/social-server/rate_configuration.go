package main

import "time"

type configuredRate struct {
	Limit  int
	Window time.Duration
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
