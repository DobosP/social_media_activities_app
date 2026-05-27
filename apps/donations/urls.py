from django.urls import path

from .views import MyDonationsView, StartDonationView

urlpatterns = [
    path("", StartDonationView.as_view(), name="donation-start"),
    path("mine/", MyDonationsView.as_view(), name="donation-mine"),
]
