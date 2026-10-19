package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestQualificationDSNAllowsOnlyExactNotificationsGrammar(t *testing.T) {
	for _, prefix := range []string{"012345abcdef", "000000000000", "ffffffffffff"} {
		t.Run(prefix, func(t *testing.T) {
			want := "host=/pgsocket port=55434 user=app dbname=social_family_" + prefix + "_notifications sslmode=disable"
			for _, args := range [][]string{{"-dsn", want}, {"-dsn=" + want}, {"--dsn", want}} {
				got, err := qualificationDSN(args)
				if err != nil || got != want {
					t.Fatalf("exact local notifications grammar refused: got %q, error %v", got, err)
				}
			}
		})
	}
}

func TestQualificationDSNRejectsForeignOrMalformedGrammar(t *testing.T) {
	valid := "host=/pgsocket port=55434 user=app dbname=social_family_012345abcdef_notifications sslmode=disable"
	cases := map[string]string{
		"empty":              "",
		"tcp-host":           strings.Replace(valid, "host=/pgsocket", "host=127.0.0.1", 1),
		"foreign-socket":     strings.Replace(valid, "host=/pgsocket", "host=/socket", 1),
		"socket-traversal":   strings.Replace(valid, "host=/pgsocket", "host=/pgsocket/../socket", 1),
		"wrong-port":         strings.Replace(valid, "port=55434", "port=5432", 1),
		"wrong-user":         strings.Replace(valid, "user=app", "user=postgres", 1),
		"foreign-prefix":     strings.Replace(valid, "social_family_", "production_", 1),
		"short-prefix":       strings.Replace(valid, "012345abcdef", "012345abcde", 1),
		"long-prefix":        strings.Replace(valid, "012345abcdef", "012345abcdef0", 1),
		"uppercase-prefix":   strings.Replace(valid, "012345abcdef", "012345ABCDEf", 1),
		"nonhex-prefix":      strings.Replace(valid, "012345abcdef", "012345abcdeg", 1),
		"suffix-extension":   strings.Replace(valid, "_notifications", "_notifications_extra", 1),
		"ssl-enabled":        strings.Replace(valid, "sslmode=disable", "sslmode=require", 1),
		"extra-option":       valid + " application_name=qualification",
		"duplicate-option":   valid + " dbname=social_family_012345abcdef_notifications",
		"leading-space":      " " + valid,
		"trailing-space":     valid + " ",
		"trailing-newline":   valid + "\n",
		"embedded-newline":   strings.Replace(valid, " user=", "\nuser=", 1),
		"double-space":       strings.Replace(valid, " user=", "  user=", 1),
		"uri":                "postgres://app@localhost:55434/social_family_012345abcdef_notifications?sslmode=disable",
		"quoted-database":    strings.Replace(valid, "dbname=social_family_012345abcdef_notifications", "dbname='social_family_012345abcdef_notifications'", 1),
		"identifier-payload": strings.Replace(valid, "_notifications", "_notifications;DROP DATABASE postgres", 1),
	}
	for _, lane := range []string{"web", "app", "accounts", "recommendations", "social", "messaging"} {
		cases["original-six-"+lane] = strings.Replace(valid, "_notifications", "_"+lane, 1)
	}
	for name, dsn := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := qualificationDSN([]string{"-dsn", dsn})
			if err == nil || got != "" || err.Error() != "explicit isolated qualification DSN required" {
				t.Fatalf("foreign/malformed scope accepted: got %q, error %v", got, err)
			}
		})
	}
}

func TestRunRejectsInvalidArgumentsBeforeDatabaseAndOutput(t *testing.T) {
	cases := map[string][]string{
		"missing":       nil,
		"missing-value": {"-dsn"},
		"unknown-flag":  {"-unknown"},
		"positional":    {"unexpected"},
		"empty-value":   {"-dsn", ""},
		"other-lane":    {"-dsn", "host=/pgsocket port=55434 user=app dbname=social_family_012345abcdef_web sslmode=disable"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			err := run(args, &output)
			if err == nil || err.Error() != "explicit isolated qualification DSN required" || output.Len() != 0 {
				t.Fatalf("invalid invocation reached migration/output: error %v, output %q", err, output.String())
			}
		})
	}
}
