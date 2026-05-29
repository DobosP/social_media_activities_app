"""Admin surfaces messaging METADATA only — message bodies are end-to-end
encrypted and unreadable here (and to the server). Use it for abuse triage of
who-talked-to-whom; content evidence comes via report-with-decryption."""

from django.contrib import admin

from .models import Conversation, Message, MessageKey, Participant, PublicKey


@admin.register(PublicKey)
class PublicKeyAdmin(admin.ModelAdmin):
    list_display = ("user", "algorithm", "active", "created_at")
    list_filter = ("algorithm", "active")
    search_fields = ("user__username",)
    raw_id_fields = ("user",)
    readonly_fields = ("key_id", "created_at")


class ParticipantInline(admin.TabularInline):
    model = Participant
    extra = 0
    raw_id_fields = ("user", "invited_by")
    readonly_fields = ("created_at", "joined_at")


@admin.register(Conversation)
class ConversationAdmin(admin.ModelAdmin):
    list_display = ("id", "kind", "cohort", "title", "creator", "created_at", "updated_at")
    list_filter = ("kind", "cohort")
    search_fields = ("title", "creator__username")
    raw_id_fields = ("creator",)
    inlines = [ParticipantInline]
    readonly_fields = ("created_at", "updated_at")


@admin.register(Message)
class MessageAdmin(admin.ModelAdmin):
    # No content column: ciphertext is unreadable. Metadata only.
    list_display = ("id", "conversation", "sender", "algorithm", "created_at")
    list_filter = ("algorithm",)
    search_fields = ("sender__username",)
    raw_id_fields = ("conversation", "sender")
    readonly_fields = ("created_at",)


@admin.register(MessageKey)
class MessageKeyAdmin(admin.ModelAdmin):
    list_display = ("id", "message", "recipient", "created_at")
    raw_id_fields = ("message", "recipient")
    readonly_fields = ("created_at",)
