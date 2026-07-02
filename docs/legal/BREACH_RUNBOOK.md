# Personal-Data Breach & Illegal-Content Escalation Runbook

> **STATUS: DRAFT — to be finalized by a qualified DPO / Romanian counsel. NOT legal advice.**
> Confirm contact points, the exact ANSPDCP notification form, the law-enforcement route, and
> severity thresholds before this is operational. Statutory timelines below **must not be exceeded**.

Covers two incident types (which may overlap):
1. **Personal-data breach** — GDPR **Art. 33** (notify supervisory authority **ANSPDCP** within
   **72h** of becoming aware) / **Art. 34** (notify affected data subjects without undue delay when
   high risk).
2. **CSAM / child-endangerment found via a report** — escalate to **IGPR** (Romanian Police) and the
   **INHOPE / esc_ABUZ (Ora de Net — Salvați Copiii)** hotline.

Because the data subjects are **largely minors**, treat every incident as higher severity and lean
toward notification.

## Roles (assign real names — TBD)
| Role | Owner |
|---|---|
| Incident lead | _<TBD>_ |
| DPO (ANSPDCP liaison) | _<TBD>_ |
| Engineering on-call | _<TBD>_ |
| Moderation/safety lead (CSAM) | _<TBD>_ |
| Legal counsel (RO) | _<TBD>_ |

## A. Personal-data breach
1. **Detect & contain (hour 0):** rotate `DJANGO_SECRET_KEY` (invalidates sessions/signed media
   URLs), revoke leaked credentials, isolate affected components. Start a timestamped incident log.
2. **Assess (hours 0–24):** what data, how many subjects, **how many minors**, likelihood & severity
   of harm. Use the hash-chained audit log (`verify_audit_chain()`) for a tamper-evident timeline.
3. **Notify ANSPDCP (within 72h)** if a risk to rights/freedoms is likely: nature of breach,
   categories & approximate number of subjects/records, DPO contact, likely consequences, measures
   taken/proposed. Phased notification is allowed if facts are still emerging.
4. **Notify data subjects (Art. 34)** without undue delay if **high risk** — clear language; for
   minors, **notify guardians**. Provide DPO contact + mitigation advice.
5. **Remediate & record:** root-cause fix, regression test, and a permanent record (Art. 33(5)) even
   when ANSPDCP isn't notified, with the justification.

## B. CSAM / child-endangerment (via report-with-decryption)
> CSAM can arrive even with uploads OFF (e.g. a recipient reports an E2EE message they decrypted).
1. **Preserve, do not redistribute:** never re-download/forward; preserve hashes + metadata + the
   audit trail. Restrict access to the named safety lead.
2. **Escalate immediately** to IGPR + INHOPE/esc_ABUZ per the agreed route (TBD). Follow their
   evidence-handling instructions; maintain chain-of-custody.
3. **Account action:** suspend/ban via `take_action` (audited); preserve evidence before any deletion.
4. **GDPR overlap:** if personal data was also exposed, run track A in parallel.

## Templates (fill in)
- *ANSPDCP notification:* _<incident summary, categories, counts, minors count, consequences, measures, DPO>_
- *Data-subject / guardian notice:* _<plain-language what happened, what data, what we did, what you should do, DPO contact>_

## Rehearsal
Tabletop-rehearse this runbook before onboarding minors; record the date and gaps found. (Audit:
L-CSAM-SOP, breach-runbook are launch-blockers.)

*Uncertainties requiring counsel: exact ANSPDCP form, IGPR/INHOPE route, DSA Art.18 applicability —
see `docs/archive/AUDIT_STRESS_2026-05-29.md` §4.*
