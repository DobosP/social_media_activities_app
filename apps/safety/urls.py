from django.urls import path

from .views import BlockView, ReportView

urlpatterns = [
    path("reports/", ReportView.as_view(), name="safety-report"),
    path("blocks/", BlockView.as_view(), name="safety-block"),
]
