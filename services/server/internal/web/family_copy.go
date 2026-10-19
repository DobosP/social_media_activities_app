package web

import (
	"html"
	"strings"
)

// These display keys point to the existing frozen source catalog msgids. They
// are plain text, including after translation; no trusted HTML crosses the VM.
var familyCopySources = map[string]string{
	"Account":    "Account",
	"Activities": "Activities",
	"An alert when you tap “I've arrived” at a meetup.": "&#10003; An alert when you tap &ldquo;I've arrived&rdquo; at a meetup.",
	"Child's username": "Child's username",
	"Children you are the registered parent/legal guardian of. Guardianship is set up alongside parental consent. You can see where and when they're meeting in person — not their conversations, photos or who else is there.": "Children you are the registered parent/legal guardian of. Guardianship is set up\n  alongside parental consent. You can see <strong>where and when</strong> they're meeting in\n  person &mdash; not their conversations, photos or who else is there.",
	"Combined with another guardian's limits, no meetups currently match — so they can join nothing right now. Loosen a limit if that isn't what you intend.":                                                                   "Combined with another guardian's limits, no meetups currently match — so they can join nothing right now. Loosen a limit if that isn't what you intend.",
	"Demo environment: do not enter real personal data.": "Demo environment: do not enter real personal data.",
	"Earliest start time":                                "Earliest start time",
	"End guardianship":                                   "End guardianship",
	"Ends:":                                              "Ends:",
	"Enter your child's username to send a guardianship request. They confirm it from their own home page. You must be a verified adult.": "Enter your child's username to send a guardianship request. They confirm it from\n  their own home page. You must be a verified adult.",
	"Get an alert when they tap “I've arrived” at a meetup.":                                                                              "&#10003; Get an alert when they tap &ldquo;I've arrived&rdquo; at a meetup.",
	"If they have set up secure messaging, they can read your group chats as a visible observer (you can see them in the chat).":          "&#10003; If they have set up secure messaging, they can read your group chats as a visible observer (you can see them in the chat).",
	"If you've set up secure messaging, read their group chats as a visible, read-only observer.":                                         "&#10003; If you've set up secure messaging, read their group chats as a visible, read-only observer.",
	"Language":          "Language",
	"Latest start time": "Latest start time",
	"Legal guardian":    "Legal guardian",
	"Limits they've set on the meetups you can join": "Limits they've set on the meetups you can join",
	"Link a child's account":                         "Link a child's account",
	"Log out":                                        "Log out",
	"Menu":                                           "Menu",
	"Most open meetups at once":                      "Most open meetups at once",
	"No limit":                                       "No limit",
	"No one is registered as your guardian.":         "No one is registered as your guardian.",
	"No upcoming meetups to check against right now.":       "No upcoming meetups to check against right now.",
	"No upcoming meetups.":                                  "No upcoming meetups.",
	"Only guardian-accompanied meetups":                     "Only guardian-accompanied meetups",
	"Only on these days (none ticked = any day)":            "Only on these days (none ticked = any day)",
	"Only these kinds of activity (none ticked = any kind)": "Only these kinds of activity (none ticked = any kind)",
	"Optional, conservative limits on the meetups your child can join. These only ever make their options narrower — they never open anything up. If more than one guardian sets a limit, the strictest one applies.": "Optional, conservative limits on the meetups\n        your child can join. These only ever make their options narrower &mdash; they never open anything up.\n        If more than one guardian sets a limit, the strictest one applies.",
	"Parent":                          "Parent",
	"Parental consent is active.":     "&#10003; Parental consent is active.",
	"Parental consent is not active.": "&bull; Parental consent is not active.",
	"Pick the kinds of activity their suggestions should lean toward. This only nudges the order of what's suggested and adds an honest note — it never hides anything, and it never changes what they're allowed to join (use the participation limits above for that).": "Pick the kinds of activity their suggestions should lean toward. This only nudges the order of what's suggested and adds an honest note &mdash; it never hides anything, and it never changes what they're allowed to join (use the participation limits above for that).",
	"Primary":      "Primary",
	"Privacy":      "Privacy",
	"Relationship": "Relationship",
	"Save limits":  "Save limits",
	"Save topics":  "Save topics",
	"See their upcoming meetups (place, time, activity type) — shown above.": "&#10003; See their upcoming meetups (place, time, activity type) &mdash; shown above.",
	"Send guardianship request":       "Send guardianship request",
	"Set language":                    "Set language",
	"Set participation limits":        "Set participation limits",
	"Settings":                        "Settings",
	"Skip to main content":            "Skip to main content",
	"Suggested topics for their feed": "Suggested topics for their feed",
	"The adults registered as your parent/legal guardian, and exactly what each one can and cannot see about you. This page is for your information only.":                                "The adults registered as your parent/legal guardian, and exactly what each one can and cannot see about you. This page is for your information only.",
	"They cannot read your activity threads, see your photos or who else is there, post as you, or join your activities. They are never in your cohort.":                                  "&#10007; They cannot read your activity threads, see your photos or who else is there, post as you, or join your activities. They are never in your cohort.",
	"This page doesn't have a remove button. To change a guardianship link, talk to your guardian. If something feels wrong, use Report on an activity or person, or leave the activity.": "This page doesn't have a &ldquo;remove&rdquo; button. To change\n    a guardianship link, talk to your guardian. If something feels wrong, use <strong>Report</strong> on an\n    activity or person, or leave the activity.",
	"Topics to lean toward (none ticked = no steering)": "Topics to lean toward (none ticked = no steering)",
	"Upcoming meetups":                   "Upcoming meetups",
	"What they can see about you":        "What they can see about you",
	"What this guardianship lets you do": "What this guardianship lets you do",
	"Where and when your upcoming meetups are (place, time, activity type).": "&#10003; Where and when your upcoming meetups are (place, time, activity type).",
	"Where to meet:": "Where to meet:",
	"You aren't a registered guardian of any account.": "You aren't a registered guardian of any account.",
	"You can grant or revoke it.":                      "You can grant or revoke it.",
	"You cannot see their thread posts, photos, who else is there, or chat content (beyond the observer view above); you never enter their activities or cohort.": "&#10007; You cannot see their thread posts, photos, who else is there, or chat content (beyond the observer view above); you never enter their activities or cohort.",
	"Your guardians":                   "Your guardians",
	"Your wards":                       "Your wards",
	"guardian-supervised":              "guardian-supervised",
	"supervised — no adult seated yet": "supervised &mdash; no adult seated yet",
}

var familyCopyMarkup = strings.NewReplacer("<strong>", "", "</strong>", "")

func familyPlainCopy(catalog translationCatalog, language, message string, count int) string {
	source := message
	if original, ok := familyCopySources[message]; ok {
		source = original
	}
	if count != 1 {
		switch source {
		case "You can be in at most %(n)s open meetup at a time.":
			source = "You can be in at most %(n)s open meetups at a time."
		case "&#10003; Limit set: at most %(n)s open meetup at a time.":
			source = "&#10003; Limit set: at most %(n)s open meetups at a time."
		}
	}
	value := catalog.translate(language, source, count)
	value = familyCopyMarkup.Replace(value)
	return strings.Join(strings.Fields(html.UnescapeString(value)), " ")
}
