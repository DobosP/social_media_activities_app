# Status — social_media_activities_app

Durable status for agents. Update this file when project direction, verification commands, or operational assumptions change.

## Role in the fleet
Django/social-activity app for RO-EDU-adjacent workflows, with privacy, moderation, and child-safety responsibilities.

## Current operational focus
- Keep GDPR/privacy, moderation, and child-safety gates explicit and test-covered.
- Use deferred/off-request task foundations for work that should not block requests.
- Verify through Docker Compose using `python -m pytest` in the web container.

## Standard verification
```bash
docker compose -p socialfix -f docker-compose.local.yml exec -T web sh -lc 'python -m pytest apps/ops/tests/test_deferred_tasks.py -q'
git diff --check
```

## Agent notes
- Require human review for privacy, moderation, child-safety, or auth changes.
- Never read or print secret values.
