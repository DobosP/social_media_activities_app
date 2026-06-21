from django.urls import path

from .views import (
    AppealView,
    AuthorityReferralView,
    BlockView,
    ModerationAppealListView,
    ModerationReportListView,
    ReportView,
    ResolveAppealView,
    ResolveReportView,
)

urlpatterns = [
    path("reports/", ReportView.as_view(), name="safety-report"),
    path("blocks/", BlockView.as_view(), name="safety-block"),
    path("appeals/", AppealView.as_view(), name="safety-appeal"),
    path("moderation/reports/", ModerationReportListView.as_view(), name="safety-mod-reports"),
    path(
        "moderation/reports/<int:pk>/resolve/",
        ResolveReportView.as_view(),
        name="safety-mod-resolve",
    ),
    path("moderation/appeals/", ModerationAppealListView.as_view(), name="safety-mod-appeals"),
    path(
        "moderation/appeals/<int:pk>/resolve/",
        ResolveAppealView.as_view(),
        name="safety-mod-appeal-resolve",
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
