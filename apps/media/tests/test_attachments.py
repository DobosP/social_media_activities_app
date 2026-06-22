"""Thread attachments (images + PDF in the activity conversation): the scan/EXIF gate, the
cohort policy (PDF adults-only, images any cohort, none in DMs), membership-scoped signed
serving (PDF forced to download), and the web compose flow. Reuses the Photo scan pipeline."""

from datetime import timedelta
from io import BytesIO

import pytest
from django.contrib.gis.geos import Point
from django.test import Client, override_settings
from django.utils import timezone
from PIL import Image

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.media import services as media
from apps.media.models import Attachment
from apps.places.models import Place
from apps.safety.services import block_user
from apps.social import services as social
from apps.social.models import Membership
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "pw-123-secret"


def _png(color=(10, 120, 200), size=(8, 8)) -> bytes:
    out = BytesIO()
    Image.new("RGB", size, color).save(out, format="PNG")
    return out.getvalue()


def _pdf() -> bytes:
    return b"%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"


def _adult(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _type(slug="att-bball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="att-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": "Basketball", "category": cat}
    )
    return t


def _activity(owner, slug="att-bball"):
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return social.create_activity(
        owner,
        place=place,
        activity_type=_type(slug),
        title="Game",
        starts_at=timezone.now() + timedelta(days=1),
    )


def _join(activity, user):
    return Membership.objects.create(
        activity=activity, user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


# Fake document scanners, wired by dotted path via override_settings(MEDIA_DOCUMENT_SCANNER=...).
# (The default is the no-op scanner, so a wired+effective scanner needs a test double.)
class _EffectiveCleanDocScanner:
    def is_effective(self):
        return True

    def scan(self, data):
        from apps.media.scanning import ScanResult

        return ScanResult(clean=True)


class _EffectiveMatchDocScanner:
    def is_effective(self):
        return True

    def scan(self, data):
        from apps.media.scanning import ScanResult

        return ScanResult(clean=False, matched="Eicar-Test-Signature")


# --- the attach gate -----------------------------------------------------------------------


def test_attach_image_to_own_post():
    owner = _adult("att_o1")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "here's the court")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png())
    assert att.kind == Attachment.Kind.IMAGE
    assert att.exif_stripped is True
    assert att.content_type.startswith("image/")


def test_attach_pdf_allowed_for_adults():
    owner = _adult("att_o2")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "the rules")
    att = media.attach_to_post(owner, post, filename="rules v2.pdf", data=_pdf())
    assert att.kind == Attachment.Kind.FILE
    assert att.content_type == "application/pdf"
    assert att.original_filename.endswith(".pdf")


def test_pdf_blocked_in_child_thread():
    # FILE (PDF) is a new media type → never for minors. (Images are still allowed.)
    child = _child("att_c1")
    activity = _activity(child, slug="att-kids")
    post = social.post_to_thread(child, activity, "hi")
    with pytest.raises(media.NotAuthorized):
        media.attach_to_post(child, post, filename="x.pdf", data=_pdf())
    # ...but an image is fine in a (supervised, scanned) child thread.
    assert media.attach_to_post(child, post, filename="x.png", data=_png()).kind == "image"


def test_cannot_attach_to_someone_elses_post():
    owner = _adult("att_o3")
    other = _adult("att_x3")
    activity = _activity(owner)
    _join(activity, other)
    post = social.post_to_thread(owner, activity, "mine")
    with pytest.raises(media.NotAuthorized):
        media.attach_to_post(other, post, filename="x.png", data=_png())


def test_attach_blocked_for_cohort_drifted_member():
    # The write gate mirrors the read gate (can_read_thread): a member whose cohort drifted away
    # from the activity (while their stale MEMBER row persists) can no longer attach — the
    # child-safety gate lives in the service, not the caller.
    from apps.accounts.models import Cohort

    owner = _adult("att_cd1")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "mine")
    owner.cohort = Cohort.TEEN
    owner.save(update_fields=["cohort"])
    with pytest.raises(media.NotAuthorized):
        media.attach_to_post(owner, post, filename="x.png", data=_png())


def test_non_image_non_pdf_rejected_cleanly():
    owner = _adult("att_o4")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "junk")
    with pytest.raises(media.MediaRejected):
        media.attach_to_post(owner, post, filename="x.bin", data=b"not an image or pdf")


def test_oversize_rejected():
    owner = _adult("att_o5")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "big")
    with override_settings(MEDIA_ATTACHMENT_MAX_BYTES=10):
        with pytest.raises(media.MediaRejected):
            media.attach_to_post(owner, post, filename="x.png", data=_png())


def test_scan_match_blocks_and_stores_nothing():
    import hashlib

    owner = _adult("att_o6")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "bad")
    data = _png(color=(1, 2, 3))
    digest = hashlib.sha256(data).hexdigest()
    with override_settings(MEDIA_CSAM_HASH_BLOCKLIST=[digest], MEDIA_REQUIRE_SCANNER=True):
        with pytest.raises(media.MediaRejected):
            media.attach_to_post(owner, post, filename="x.png", data=data)
    assert not Attachment.objects.filter(post=post).exists()


def test_fail_closed_without_scanner():
    owner = _adult("att_o7")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "noscan")
    with override_settings(MEDIA_REQUIRE_SCANNER=True, MEDIA_CSAM_HASH_BLOCKLIST=[]):
        with pytest.raises(media.MediaRejected):
            media.attach_to_post(owner, post, filename="x.png", data=_png())


# --- PDF document-scan (antivirus) branch in attach_to_post --------------------------------


def test_pdf_fail_closed_without_document_scanner():
    # The gap: a PDF must be REFUSED when document scanning is required but the default no-op
    # scanner is wired (is_effective False). The image scan passes first (it's not required in
    # test settings), so this isolates the document-scanner fail-closed gate.
    owner = _adult("att_doc1")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "doc")
    with override_settings(MEDIA_REQUIRE_DOCUMENT_SCANNER=True):  # default scanner is the no-op
        with pytest.raises(media.MediaRejected, match="document scanner"):
            media.attach_to_post(owner, post, filename="x.pdf", data=_pdf())
    assert not Attachment.objects.filter(post=post).exists()


def test_pdf_blocked_by_effective_document_scanner_match():
    # An effective doc scanner that reports a signature hit blocks the PDF (no require flag needed).
    owner = _adult("att_doc2")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "doc")
    with override_settings(
        MEDIA_DOCUMENT_SCANNER="apps.media.tests.test_attachments._EffectiveMatchDocScanner"
    ):
        with pytest.raises(media.MediaRejected, match="failed safety screening"):
            media.attach_to_post(owner, post, filename="x.pdf", data=_pdf())
    assert not Attachment.objects.filter(post=post).exists()


def test_pdf_allowed_when_effective_document_scanner_is_clean():
    # The fail-closed gate OPENS once a real, effective scanner clears the file — even with the
    # require flag on. Proves the gate isn't just "always reject PDFs".
    owner = _adult("att_doc3")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "doc")
    with override_settings(
        MEDIA_REQUIRE_DOCUMENT_SCANNER=True,
        MEDIA_DOCUMENT_SCANNER="apps.media.tests.test_attachments._EffectiveCleanDocScanner",
    ):
        att = media.attach_to_post(owner, post, filename="ok.pdf", data=_pdf())
    assert att.kind == Attachment.Kind.FILE
    assert att.content_type == "application/pdf"


def test_image_skips_the_document_scanner_gate():
    # The document-scan gate is PDF-only — an image in the same thread is unaffected by a
    # required-but-unwired document scanner.
    owner = _adult("att_doc4")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "img")
    with override_settings(MEDIA_REQUIRE_DOCUMENT_SCANNER=True):  # no-op doc scanner
        att = media.attach_to_post(owner, post, filename="x.png", data=_png())
    assert att.kind == Attachment.Kind.IMAGE


def test_web_pdf_doc_scan_fail_closed_rolls_back_post():
    # End-to-end: the web compose flow refusing a PDF on the document-scan gate must roll the
    # Post back too (one transaction), so there is never a post without its (rejected) file.
    owner = _adult("att_doc5")
    activity = _activity(owner)
    # Pin MEDIA_REQUIRE_SCANNER=False so the rejection is unambiguously the DOCUMENT gate (not the
    # image gate) even if test settings change later.
    with override_settings(MEDIA_REQUIRE_DOCUMENT_SCANNER=True, MEDIA_REQUIRE_SCANNER=False):
        _client(owner).post(
            f"/activities/{activity.id}/post/",
            {"body": "with pdf", "attachment": BytesIO(_pdf())},
        )
    assert not activity.thread.posts.filter(body="with pdf").exists()
    assert Attachment.objects.filter(post__thread=activity.thread).count() == 0


# --- visibility + serving ------------------------------------------------------------------


def test_attachment_visibility_membership_scoped():
    owner = _adult("att_v1")
    member = _adult("att_v2")
    outsider = _adult("att_v3")
    activity = _activity(owner)
    _join(activity, member)
    post = social.post_to_thread(owner, activity, "pic")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png())
    assert media.can_view_attachment(member, att) is True
    assert media.can_view_attachment(outsider, att) is False  # not a member
    block_user(member, owner)
    assert media.can_view_attachment(member, att) is False  # blocked-vs-uploader


def test_hidden_post_hides_attachment():
    owner = _adult("att_h1")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "secret")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png())
    social.delete_own_post(owner, post)  # soft-hide
    att.refresh_from_db()
    assert media.can_view_attachment(owner, att) is False


def test_signed_serving_pdf_forces_download():
    owner = _adult("att_s1")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "doc")
    att = media.attach_to_post(owner, post, filename="plan.pdf", data=_pdf())
    url = media.attachment_signed_url(att, owner)
    resp = _client(owner).get(url)
    assert resp.status_code == 200
    assert resp["Content-Type"] == "application/pdf"
    assert resp["X-Content-Type-Options"] == "nosniff"
    assert "attachment;" in resp["Content-Disposition"]  # forced download (no inline PDF JS)


def test_signed_serving_rejects_other_viewer():
    owner = _adult("att_s2")
    outsider = _adult("att_s3")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "pic")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png())
    url = media.attachment_signed_url(att, owner)  # token bound to owner
    assert _client(outsider).get(url).status_code == 403


# --- web compose flow ----------------------------------------------------------------------


def test_web_post_with_image_renders_inline():
    owner = _adult("att_w1")
    activity = _activity(owner)
    resp = _client(owner).post(
        f"/activities/{activity.id}/post/",
        {"body": "check this", "attachment": BytesIO(_png())},
    )
    assert resp.status_code == 302
    post = activity.thread.posts.get(body="check this")
    assert Attachment.objects.filter(post=post).count() == 1
    page = _client(owner).get(f"/activities/{activity.id}/").content.decode()
    assert "/api/media/attachment/" in page  # signed inline image URL present


def test_web_image_only_message_allowed():
    owner = _adult("att_w2")
    activity = _activity(owner)
    resp = _client(owner).post(
        f"/activities/{activity.id}/post/", {"body": "", "attachment": BytesIO(_png())}
    )
    assert resp.status_code == 302
    assert Attachment.objects.filter(post__thread=activity.thread).count() == 1


def test_web_empty_message_with_no_file_rejected():
    owner = _adult("att_w3")
    activity = _activity(owner)
    _client(owner).post(f"/activities/{activity.id}/post/", {"body": "   "})
    assert activity.thread.posts.count() == 0  # nothing created


def test_web_bad_file_rolls_back_post():
    owner = _adult("att_w4")
    activity = _activity(owner)
    _client(owner).post(
        f"/activities/{activity.id}/post/",
        {"body": "with junk", "attachment": BytesIO(b"not an image")},
    )
    # The scan/validation rejected the file, so the post was rolled back too (atomic).
    assert not activity.thread.posts.filter(body="with junk").exists()
