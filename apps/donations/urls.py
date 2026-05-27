from django.urls import path

from .views import (
    DonationTotalView,
    DonationWebhookView,
    MyDonationsView,
    StartDonationView,
)

urlpatterns = [
    path("", StartDonationView.as_view(), name="donation-start"),
    path("mine/", MyDonationsView.as_view(), name="donation-mine"),
    path("total/", DonationTotalView.as_view(), name="donation-total"),
    path("webhook/", DonationWebhookView.as_view(), name="donation-webhook"),
]
