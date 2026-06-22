from django.urls import path

from .views import CSPReportView, StatsView

urlpatterns = [
    path("stats/", StatsView.as_view(), name="ops-stats"),
    path("csp-report/", CSPReportView.as_view(), name="csp-report"),
]
