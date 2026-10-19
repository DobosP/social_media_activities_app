package web

import (
	"bytes"
	"strings"
	"testing"
)

// Pure native-template fixtures: no account/database/provider or HTTP authority.
func familyTemplateFixture() familyPage {
	copy := map[string]string{}
	for _, key := range []string{
		"Account",
		"Activities",
		"An alert when you tap “I've arrived” at a meetup.",
		"Child's username",
		"Children you are the registered parent/legal guardian of. Guardianship is set up alongside parental consent. You can see where and when they're meeting in person — not their conversations, photos or who else is there.",
		"Combined with another guardian's limits, no meetups currently match — so they can join nothing right now. Loosen a limit if that isn't what you intend.",
		"Demo environment: do not enter real personal data.",
		"Earliest start time",
		"End guardianship",
		"Ends:",
		"Enter your child's username to send a guardianship request. They confirm it from their own home page. You must be a verified adult.",
		"Get an alert when they tap “I've arrived” at a meetup.",
		"If they have set up secure messaging, they can read your group chats as a visible observer (you can see them in the chat).",
		"If you've set up secure messaging, read their group chats as a visible, read-only observer.",
		"Language",
		"Latest start time",
		"Legal guardian",
		"Limits they've set on the meetups you can join",
		"Link a child's account",
		"Log out",
		"Menu",
		"Most open meetups at once",
		"No limit",
		"No one is registered as your guardian.",
		"No upcoming meetups to check against right now.",
		"No upcoming meetups.",
		"Only guardian-accompanied meetups",
		"Only on these days (none ticked = any day)",
		"Only these kinds of activity (none ticked = any kind)",
		"Optional, conservative limits on the meetups your child can join. These only ever make their options narrower — they never open anything up. If more than one guardian sets a limit, the strictest one applies.",
		"Parent",
		"Parental consent is active.",
		"Parental consent is not active.",
		"Pick the kinds of activity their suggestions should lean toward. This only nudges the order of what's suggested and adds an honest note — it never hides anything, and it never changes what they're allowed to join (use the participation limits above for that).",
		"Primary",
		"Privacy",
		"Relationship",
		"Save limits",
		"Save topics",
		"See their upcoming meetups (place, time, activity type) — shown above.",
		"Send guardianship request",
		"Set language",
		"Set participation limits",
		"Settings",
		"Skip to main content",
		"Suggested topics for their feed",
		"The adults registered as your parent/legal guardian, and exactly what each one can and cannot see about you. This page is for your information only.",
		"They cannot read your activity threads, see your photos or who else is there, post as you, or join your activities. They are never in your cohort.",
		"This page doesn't have a remove button. To change a guardianship link, talk to your guardian. If something feels wrong, use Report on an activity or person, or leave the activity.",
		"Topics to lean toward (none ticked = no steering)",
		"Upcoming meetups",
		"What they can see about you",
		"What this guardianship lets you do",
		"Where and when your upcoming meetups are (place, time, activity type).",
		"Where to meet:",
		"You aren't a registered guardian of any account.",
		"You can grant or revoke it.",
		"You cannot see their thread posts, photos, who else is there, or chat content (beyond the observer view above); you never enter their activities or cohort.",
		"Your guardians",
		"Your wards",
		"guardian-supervised",
		"supervised — no adult seated yet",
	} {
		copy[key] = key
	}
	return familyPage{
		WardsPage: true, Copy: copy, Invite: true,
		Shell: familyShell{Language: "en", English: true, Theme: "contrast", Text: "larger", Motion: "reduce", SiteName: "Synthetic Activities", CSRF: "synthetic-family-csrf", DisplayName: "Generated guardian", Canonical: "/wards/", Navigation: []familyLink{{Label: "Browse", URL: "/activities/"}}, AccountTabs: []familyLink{{Label: "Your wards", URL: "/wards/", Current: true}}, Footer: []familyLink{{Label: "Privacy", URL: "/privacy/"}}, Messages: []string{"Synthetic status"}},
		Wards: []familyWard{{ID: 41, Name: "Generated child", Cohort: "Child", Arrival: true, Observe: true, RequiresConsent: true, ConsentActive: true, GrantConsent: true, CanLimits: true, CanTopics: true, CombinedBlocks: true, ChosenLimits: []string{"Only on chosen days"}, LimitsURL: "/wards/41/limits/", TopicsURL: "/wards/41/topics/", RevokeURL: "/wards/41/revoke/", RevokeExplanation: "End guardianship of Generated child? This also revokes the consent you granted.", Earliest: []familyOption{{Value: "", Label: "No limit"}, {Value: "8", Label: "8:00", Selected: true}}, Latest: []familyOption{{Value: "", Label: "No limit", Selected: true}, {Value: "20", Label: "20:00"}}, MaxOpen: "2", Supervised: true, Weekdays: []familyOption{{Value: "1", Label: "Monday", Selected: true}, {Value: "2", Label: "Tuesday"}}, Categories: []familyOption{{Value: "sport", Label: "Sport", Selected: true}}, Topics: []familyOption{{Value: "sport", Label: "Sport", Selected: true}}, Preview: &familyPreview{Total: 4, Eligible: 1, Text: "Right now, they could join 1 of 4 upcoming meetups with room in their cohort."}, Meetups: []familyMeetup{{Start: "Tue 20 Oct, 10:00", End: "Tue 20 Oct, 11:00", Activity: "Synthetic outdoor activity", Place: "Generated public venue", City: "Cluj-Napoca", MeetingPoint: "At the entrance", Rationale: "Child-approved venue (staff-verified)", Supervised: true, SupervisionLive: false}}}},
	}
}

func familyTemplateHTML(t *testing.T, name string, page familyPage) string {
	t.Helper()
	templates, err := loadFamilyTemplates()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = templates.ExecuteTemplate(&out, name, page); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestNativeFamilyTemplatesKeepFiniteMutationContracts(t *testing.T) {
	page := familyTemplateFixture()
	body := familyTemplateHTML(t, "family-wards", page)
	for _, want := range []string{`lang="en" data-theme="contrast" data-text="larger" data-motion="reduce"`, `href="#main"`, `aria-current="page"`, `id="main"`, `method="post" action="/wards/41/limits/"`, `method="post" action="/wards/41/topics/"`, `method="post" action="/wards/41/revoke/"`, `method="post" action="/wards/invite/"`, `name="allowed_weekdays" value="1" checked`, `name="allowed_categories" value="sport" checked`, `name="topics" value="sport" checked`, `name="max_open_joins" min="1" max="50"`, `value="8" selected`, `value="2"`, `name="ward_username" required autocomplete="off"`, `value="parent"`, `value="legal_guardian"`, "I've arrived", "Suggested topics for their feed", "Combined with another guardian", "Right now, they could join 1 of 4", "Child-approved venue (staff-verified)", "At the entrance"} {
		if !strings.Contains(body, want) {
			t.Fatalf("native family contract absent: %q", want)
		}
	}
	if strings.Count(body, `name="csrfmiddlewaretoken" value="synthetic-family-csrf"`) != 6 || strings.Count(body, "<form ") != 6 {
		t.Fatal("every fixed POST form needs its own CSRF field")
	}
	for _, forbidden := range []string{`name="allowed_weekdays[]"`, `name="allowed_categories[]"`, `name="topics[]"`, `<script`, `<style`, ` style=`, ` onclick=`, ` onchange=`, `onsubmit=`, `http://`, `https://`, `data-confirm=`, `name="user_id"`, `name="guardian_id"`, `name="ward_id"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("unsupported family markup: %q", forbidden)
		}
	}
	if !strings.Contains(body, `<details class="u-my-half"><summary>End guardianship</summary>`) {
		t.Fatal("revocation explanation must precede deliberate native disclosure form")
	}
}

func TestNativeFamilyTemplatesKeepWardViewReadOnly(t *testing.T) {
	page := familyTemplateFixture()
	page.WardsPage = false
	page.Invite = false
	page.Wards = nil
	page.Shell.Canonical = "/guardianship/"
	page.Guardians = []familyGuardian{{Name: "Generated adult", Relationship: "Parent", Arrival: true, Observe: true, ChosenLimits: []string{"Only on chosen days"}}}
	body := familyTemplateHTML(t, "family-guardianship", page)
	for _, want := range []string{"Your guardians", "Generated adult", "What they can see about you", "I've arrived", "doesn't have a", "Only on chosen days"} {
		if !strings.Contains(body, want) {
			t.Fatalf("ward explanation absent: %q", want)
		}
	}
	for _, forbidden := range []string{"/revoke/", "/limits/", "/topics/", "/wards/invite/", `name="ward_username"`, `name="relationship"`, `name="allowed_weekdays"`, `name="topics"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("ward acquired guardian control: %q", forbidden)
		}
	}
	if strings.Count(body, "<form ") != 2 || strings.Count(body, `name="csrfmiddlewaretoken"`) != 2 {
		t.Fatal("only existing logout/language shell forms may remain on ward explanation")
	}
}

func TestNativeFamilyTemplatesEscapeEveryProjectedTextSurface(t *testing.T) {
	page := familyTemplateFixture()
	hostile := `<script>alert("private")</script>`
	page.Copy["Your wards"] = hostile
	page.Copy["Save limits"] = hostile
	page.Shell.DisplayName = hostile
	page.Shell.Messages = []string{hostile}
	page.Shell.Footer[0].Label = hostile
	page.Shell.CSRF = `synthetic" onfocus="private`
	ward := &page.Wards[0]
	ward.Name = hostile
	ward.Cohort = hostile
	ward.ChosenLimits = []string{hostile}
	ward.RevokeExplanation = hostile
	ward.Preview.Text = hostile
	ward.Meetups[0].MeetingPoint = hostile
	ward.Meetups[0].Rationale = hostile
	ward.Categories[0].Label = hostile
	ward.Categories[0].Value = `sport" onclick="private`
	body := familyTemplateHTML(t, "family-wards", page)
	if strings.Contains(body, "<script>") || strings.Contains(body, ` onfocus="private"`) || strings.Contains(body, ` onclick="private"`) || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("family projection bypassed contextual escaping")
	}
	page.WardsPage = false
	page.Guardians = []familyGuardian{{Name: hostile, Relationship: hostile, ChosenLimits: []string{hostile}}}
	body = familyTemplateHTML(t, "family-guardianship", page)
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("guardian explanation bypassed contextual escaping")
	}
}

func TestNativeFamilyTemplatesHonorCapabilitiesWithoutInventingAuthority(t *testing.T) {
	page := familyTemplateFixture()
	page.Invite = false
	ward := &page.Wards[0]
	ward.CanLimits = false
	ward.CanTopics = false
	ward.Observe = false
	ward.Arrival = false
	ward.RequiresConsent = false
	ward.GrantConsent = false
	ward.CombinedBlocks = false
	ward.Preview = nil
	ward.Meetups = nil
	body := familyTemplateHTML(t, "family-wards", page)
	for _, forbidden := range []string{"/limits/", "/topics/", "/wards/invite/", "I've arrived", "visible, read-only observer", "Parental consent is active.", "Combined with another guardian", "Right now, they could join"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("absent capability acquired visible control/claim: %q", forbidden)
		}
	}
	if !strings.Contains(body, "No upcoming meetups.") || !strings.Contains(body, "/wards/41/revoke/") {
		t.Fatal("empty meetups or own relationship revocation lost")
	}
}

func TestNativeFamilyTemplatesEmptyAndTranslatedCopyAreFinite(t *testing.T) {
	page := familyTemplateFixture()
	page.Wards = nil
	page.Invite = false
	body := familyTemplateHTML(t, "family-wards", page)
	if !strings.Contains(body, "registered guardian of any account") || strings.Contains(body, "/revoke/") {
		t.Fatal("empty guardian page invented relationship")
	}
	page.WardsPage = false
	page.Guardians = nil
	body = familyTemplateHTML(t, "family-guardianship", page)
	if !strings.Contains(body, "No one is registered as your guardian.") || strings.Contains(body, "doesn't have a") {
		t.Fatal("empty ward explanation diverged")
	}
	page = familyTemplateFixture()
	page.Shell.Language = "ro"
	page.Shell.English = false
	page.Copy["Your wards"] = "Copiii aflați în grija ta"
	page.Copy["Get an alert when they tap “I've arrived” at a meetup."] = "Primești o alertă la sosire."
	body = familyTemplateHTML(t, "family-wards", page)
	if !strings.Contains(body, "Copiii aflați în grija ta") || !strings.Contains(body, "Primești o alertă la sosire.") || strings.Contains(body, "I've arrived") {
		t.Fatal("finite translated copy not selected")
	}
}
