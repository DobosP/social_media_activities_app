"""Bounded API pagination (P1). Caps an over-eager/abusive ``?limit`` so a single request can
never ask for an unbounded scan (the default LimitOffsetPagination has no ``max_limit``). The
default page size still comes from REST_FRAMEWORK['PAGE_SIZE']."""

from rest_framework.pagination import LimitOffsetPagination


class BoundedLimitOffsetPagination(LimitOffsetPagination):
    max_limit = 200
