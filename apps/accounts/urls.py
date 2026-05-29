from django.urls import path

from .views import (
    EUDIVerifyStartView,
    EUDIVerifyView,
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
    # EUDI Wallet age verification (OpenID4VP)
    path("verify-age/start/", EUDIVerifyStartView.as_view(), name="verify-age-start"),
    path("verify-age/", EUDIVerifyView.as_view(), name="verify-age"),
]
