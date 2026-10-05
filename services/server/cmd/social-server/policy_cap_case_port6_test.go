package main

import (
	"strings"
	"testing"
)

func TestPrivacyCasePort6CLISourceMemberCapOneAndBounds(t *testing.T) {
	c, err := configuration(fixtureEnv(map[string]string{"MESSAGING_MAX_GROUP_MEMBERS": "1"}), fixtureOptions())
	if err != nil || c.MessagingPolicy.MaxGroupMembers != 1 || c.MessagingPolicy.Validate() != nil {
		t.Fatal("native CLI rejected exact original send/group cap1", err)
	}
	for _, value := range []string{"0", "-1", "257"} {
		if _, err := configuration(fixtureEnv(map[string]string{"MESSAGING_MAX_GROUP_MEMBERS": value}), fixtureOptions()); err == nil || !strings.Contains(err.Error(), "MESSAGING_MAX_GROUP_MEMBERS") {
			t.Fatal("native CLI silently accepted invalid source member cap")
		}
	}
}
