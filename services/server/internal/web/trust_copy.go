package web

// Exact existing account-deletion plural copy. Policy remains in AccountView.

var trustDestroyedCopy = [][3]string{
	{"memberships", "%(n)s activity membership", "%(n)s activity memberships"},
	{"owned_activities", "%(n)s activity you organise (and its thread)", "%(n)s activities you organise (and their threads)"},
	{"owned_groups", "%(n)s group you run (and its thread)", "%(n)s groups you run (and their threads)"},
	{"group_memberships", "%(n)s group membership", "%(n)s group memberships"},
	{"thread_posts", "%(n)s message you wrote in activity threads", "%(n)s messages you wrote in activity threads"},
	{"messages_sent", "%(n)s private (end-to-end-encrypted) message you sent", "%(n)s private (end-to-end-encrypted) messages you sent"},
	{"photos", "%(n)s photo you uploaded", "%(n)s photos you uploaded"},
	{"attachments", "%(n)s file you shared in a thread", "%(n)s files you shared in threads"},
	{"age_assurance_records", "%(n)s age-verification record", "%(n)s age-verification records"},
	{"parental_consents", "%(n)s parental-consent record", "%(n)s parental-consent records"},
	{"guardian_links", "%(n)s guardian link", "%(n)s guardian links"},
}
var trustRetainedCopy = [][3]string{
	{"donations_anonymised", "%(n)s donation stays as an anonymous financial record — the amount and status are kept for accounting, but the link to you is removed.", "%(n)s donations stay as anonymous financial records — the amounts and statuses are kept for accounting, but the link to you is removed."},
	{"audit_entries_retained", "%(n)s entry in our tamper-evident audit log is kept (the law requires this log can't be edited or deleted), but de-identified: the link to your account is removed, leaving only an internal reference number — never your username, name, or the content of your activities or messages. Deleting your account adds a final entry recording that it happened.", "%(n)s entries in our tamper-evident audit log are kept (the law requires this log can't be edited or deleted), but de-identified: the link to your account is removed, leaving only an internal reference number — never your username, name, or the content of your activities or messages. Deleting your account adds a final entry recording that it happened."},
}
