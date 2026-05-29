from django.urls import path

from .views import (
    EUDIVerifyStartView,
    EUDIVerifyView,
    GuardianLinkAcceptView,
    GuardianLinkDeclineView,
    GuardianLinkView,
    MeView,
    WardConsentView,
    WardDetailView,
    WardListView,
)

urlpatterns = [
    path("me/", MeView.as_view(), name="me"),
    path("wards/", WardListView.as_view(), name="wards"),
    path("wards/<uuid:public_id>/", WardDetailView.as_view(), name="ward-detail"),
    path("wards/<uuid:public_id>/consent/", WardConsentView.as_view(), name="ward-consent"),
    # Guardianship link establishment (mutually-confirmed invite/accept)
    path("guardian-links/", GuardianLinkView.as_view(), name="guardian-links"),
    path(
        "guardian-links/<str:token>/accept/",
        GuardianLinkAcceptView.as_view(),
        name="guardian-link-accept",
    ),
    path(
        "guardian-links/<str:token>/decline/",
        GuardianLinkDeclineView.as_view(),
        name="guardian-link-decline",
    ),
    # EUDI Wallet age verification (OpenID4VP)
    path("verify-age/start/", EUDIVerifyStartView.as_view(), name="verify-age-start"),
    path("verify-age/", EUDIVerifyView.as_view(), name="verify-age"),
]
