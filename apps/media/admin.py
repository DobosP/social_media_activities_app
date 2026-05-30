from django.contrib import admin

from .models import Attachment, Photo


@admin.register(Photo)
class PhotoAdmin(admin.ModelAdmin):
    list_display = ("id", "kind", "uploader", "thread", "scan_status", "byte_size", "created_at")
    list_filter = ("kind", "scan_status")
    search_fields = ("uploader__username", "sha256")
    readonly_fields = ("sha256", "storage_key", "byte_size", "width", "height", "created_at")


@admin.register(Attachment)
class AttachmentAdmin(admin.ModelAdmin):
    list_display = ("id", "kind", "uploader", "post", "content_type", "byte_size", "created_at")
    list_filter = ("kind",)
    search_fields = ("uploader__username", "sha256", "original_filename")
    readonly_fields = (
        "sha256",
        "storage_key",
        "byte_size",
        "width",
        "height",
        "created_at",
    )
