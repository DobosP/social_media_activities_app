from django.urls import path

from .views import (
    AuthorityReferralView,
    BlockView,
    ModerationReportListView,
    ReportView,
    ResolveReportView,
)

urlpatterns = [
    path("reports/", ReportView.as_view(), name="safety-report"),
    path("blocks/", BlockView.as_view(), name="safety-block"),
    path("moderation/reports/", ModerationReportListView.as_view(), name="safety-mod-reports"),
    path(
        "moderation/reports/<int:pk>/resolve/",
        ResolveReportView.as_view(),
        name="safety-mod-resolve",
    ),
    path(
        "moderation/referrals/",
        AuthorityReferralView.as_view(),
        name="safety-mod-referral",
    ),
    path(
        "moderation/referrals/<int:pk>/proof/",
        AuthorityReferralView.as_view(),
        name="safety-mod-referral-proof",
    ),
]
