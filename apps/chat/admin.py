from django.contrib import admin

from .models import ChatMessage


@admin.register(ChatMessage)
class ChatMessageAdmin(admin.ModelAdmin):
    list_display = ("thread", "author", "redacted", "created_at")
    list_filter = ("redacted",)
    search_fields = ("author__username", "body")
    raw_id_fields = ("thread", "author")
    readonly_fields = ("created_at",)
