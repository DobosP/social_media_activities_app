"""Role-based DRF permissions. Roles: USER (default), MODERATOR, ADMIN — plus the
GUARDIAN *relationship* (a USER with active wards). See apps.accounts.models.Role."""

from rest_framework.permissions import BasePermission


class IsModerator(BasePermission):
    """Moderators and admins (used for the safety/moderation surfaces)."""

    def has_permission(self, request, view):
        user = request.user
        return bool(user and user.is_authenticated and user.is_moderator)


class IsAdmin(BasePermission):
    def has_permission(self, request, view):
        user = request.user
        return bool(user and user.is_authenticated and user.is_admin)
