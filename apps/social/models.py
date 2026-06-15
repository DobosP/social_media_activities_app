from django.conf import settings
from django.contrib.postgres.indexes import GinIndex, OpClass
from django.db import models
from django.db.models import Q, UniqueConstraint
from django.db.models.functions import Upper
from django.utils.translation import gettext_lazy as _

from apps.accounts.models import Cohort

# Default join-by-vote threshold: a join request passes when at least this fraction
# of current members approve. Two-thirds per the product spec (configurable per activity).
DEFAULT_JOIN_THRESHOLD = 2 / 3

# Independent confirmations required before a user-submitted place is published.
DEFAULT_PLACE_QUORUM = 3


class Activity(models.Model):
    """A meetup: a Place (D1) + ActivityType (D1) + a time window + an owner.

    Cohort-scoped for safety: the activity is pinned to its owner's cohort at
    creation, and visibility/joining are restricted to that same cohort so children
    only meet similar-age peers (see docs/SAFETY.md).
    """

    class Status(models.TextChoices):
        OPEN = "open", "Open"
        CANCELLED = "cancelled", "Cancelled"
        COMPLETED = "completed", "Completed"

    class CostBand(models.TextChoices):
        UNSPECIFIED = "unspecified", "Not specified"
        FREE = "free", "Free"
        LOW = "low", "Low cost"
        PAID = "paid", "Paid"

    class Difficulty(models.TextChoices):
        UNSPECIFIED = "unspecified", "Not specified"
        EASY = "easy", "Easy"
        MODERATE = "moderate", "Moderate"
        CHALLENGING = "challenging", "Challenging"

    owner = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="owned_activities"
    )
    place = models.ForeignKey(
        "places.Place", on_delete=models.PROTECT, related_name="social_activities"
    )
    activity_type = models.ForeignKey(
        "taxonomy.ActivityType", on_delete=models.PROTECT, related_name="social_activities"
    )

    title = models.CharField(max_length=200)
    description = models.TextField(blank=True)

    # F9: owner-curated logistics shown to members above the thread (where exactly to meet,
    # what to bring, a short organiser note). Text-first; length-capped at the form/serializer
    # edge (LOGISTICS_FIELD_MAX_LENGTH), like description.
    meeting_point = models.TextField(blank=True, default="")
    what_to_bring = models.TextField(blank=True, default="")
    organizer_note = models.TextField(blank=True, default="")
    # F18: a short "getting home" note (e.g. nearest bus stop, pickup point). Owner-curated,
    # same edit path + cap as the other logistics; mirrored onto a CHILD ward's guardian manifest.
    getting_home_note = models.TextField(blank=True, default="")
    # F41: a "what to expect when you arrive" note for a nervous first-timer (how to recognise the
    # group, what happens first). Owner-curated, same edit path + cap. MEMBER-ONLY like
    # getting_home_note (deliberately NOT on the cohort-visible ActivitySerializer) — it lowers the
    # social drop-at-the-door barrier without becoming a public discovery surface.
    first_time_note = models.TextField(blank=True, default="")

    # F8: optional "what to expect" fields so newcomers, disabled, and anxious users can
    # judge fit at a glance. Owner-curated; cost/difficulty are constrained choices,
    # accessibility_notes is capped at the form/serializer edge (like description).
    cost_band = models.CharField(
        max_length=16, choices=CostBand.choices, default=CostBand.UNSPECIFIED
    )
    difficulty = models.CharField(
        max_length=16, choices=Difficulty.choices, default=Difficulty.UNSPECIFIED
    )
    accessibility_notes = models.TextField(blank=True, default="")

    starts_at = models.DateTimeField()
    ends_at = models.DateTimeField(null=True, blank=True)

    # Pinned from the owner's cohort at creation; the isolation boundary.
    cohort = models.CharField(max_length=16, choices=Cohort.choices)

    join_threshold = models.FloatField(default=DEFAULT_JOIN_THRESHOLD)
    owner_can_override = models.BooleanField(default=True)
    capacity = models.PositiveIntegerField(null=True, blank=True)
    # F1 "Quorum-go": owner-set minimum number of GOING RSVPs for the meetup to be "on".
    # null = no threshold. A property of THIS meetup, never a per-user reliability signal.
    min_to_go = models.PositiveIntegerField(null=True, blank=True)
    # One-shot latch: set the FIRST time the live GOING count reaches min_to_go, so the
    # MEETUP_CONFIRMED notice fires at most once. It does NOT drive the displayed state — the
    # "it's on / needs N more" chip is always derived LIVE from the current count, so it can never
    # lie after the count drops back below the threshold.
    go_confirmed_at = models.DateTimeField(null=True, blank=True)
    # Children's activities may allow a parent/guardian to accompany (supervised,
    # group-only). Only meaningful for the CHILD cohort. See docs/SAFETY.md.
    guardian_accompanied = models.BooleanField(default=False)
    # F29: when True (CHILD only, implies guardian_accompanied), a join cannot SETTLE until the
    # owner's own verified guardian is seated as a read-only GUARDIAN supervisor. Set at create or
    # the guarded set_activity_supervision service — deliberately NOT in ACTIVITY_EDITABLE_FIELDS
    # (a structural, cohort-isolation-adjacent pin). "Is a supervisor present *now*" is derived LIVE
    # from memberships, never stored — so the chip can't lie after a guardian leaves.
    supervised = models.BooleanField(default=False)
    # F17: an owner-set "beginners welcome" flag — a property of the MEETUP, never a skill
    # judgement of any person. Used only as a per-activity discovery filter/tag.
    beginners_welcome = models.BooleanField(default=False)

    status = models.CharField(max_length=16, choices=Status.choices, default=Status.OPEN)
    # Set by a moderator REMOVE action; hidden content is excluded from every member-facing
    # query (discovery, recommendations) but retained for audit/appeal. See apps/safety.
    is_hidden = models.BooleanField(default=False)

    # F4: the recurring series that auto-spawned this instance (null for one-off activities).
    # SET_NULL — ending/deleting a series leaves already-spawned, joined meetups standing as
    # plain one-offs. Used only for idempotency + tracing; NEVER to copy a roster between instances.
    series = models.ForeignKey(
        "ActivitySeries",
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="instances",
    )

    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        verbose_name_plural = "activities"
        constraints = [
            models.CheckConstraint(
                condition=Q(join_threshold__gt=0) & Q(join_threshold__lte=1),
                name="activity_threshold_fraction",
            ),
        ]
        indexes = [
            models.Index(fields=["cohort", "status"]),
            models.Index(fields=["starts_at"]),
            # Communities map activities by (cohort, type) and (cohort, place); index both so the
            # community read predicate + the nightly generator are index scans, not seq scans.
            models.Index(fields=["cohort", "activity_type"]),
            models.Index(fields=["cohort", "place"]),
            # W1 search uses icontains over title/description. On Postgres, Django compiles
            # icontains to UPPER(col) LIKE UPPER(%s) — so the trigram GIN index must be an
            # EXPRESSION index on Upper(col) to ever be used (a plain column index never
            # matches; review finding W1-14). pg_trgm is created in migration 0016.
            GinIndex(OpClass(Upper("title"), name="gin_trgm_ops"), name="activity_title_trgm"),
            GinIndex(OpClass(Upper("description"), name="gin_trgm_ops"), name="activity_desc_trgm"),
        ]

    def __str__(self):
        return self.title


class ActivitySeries(models.Model):
    """F4: a recurring meetup template. An organiser defines it once; the nightly
    ``spawn_due_series`` job materialises ONLY the next single Activity through the normal
    ``create_activity`` path, so every cohort/consent/blocking gate re-runs per instance.

    A series is NOT a roster and NOT an attendance record: each spawned instance requires a
    fresh per-member join (fresh parental consent for under-16), and memberships are never
    copied between instances. ``place``/``activity_type``/``cohort`` are immutable — the
    identity + cohort-isolation boundary, re-asserted against the owner's cohort at spawn.
    See docs/SAFETY.md.
    """

    class Cadence(models.TextChoices):
        WEEKLY = "weekly", "Weekly"
        BIWEEKLY = "biweekly", "Every two weeks"
        MONTHLY = "monthly", "Monthly"

    class Status(models.TextChoices):
        ACTIVE = "active", "Active"
        PAUSED = "paused", "Paused"
        ENDED = "ended", "Ended"

    owner = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="owned_series"
    )
    place = models.ForeignKey(
        "places.Place", on_delete=models.PROTECT, related_name="activity_series"
    )
    activity_type = models.ForeignKey(
        "taxonomy.ActivityType", on_delete=models.PROTECT, related_name="activity_series"
    )
    # Pinned from the owner's cohort at creation; the isolation boundary. Immutable, and
    # re-asserted against the owner's current cohort at every spawn.
    cohort = models.CharField(max_length=16, choices=Cohort.choices)

    title = models.CharField(max_length=200)
    description = models.TextField(blank=True, default="")

    # Logistics template copied onto each spawned instance (text-first; length-capped at the edge).
    meeting_point = models.TextField(blank=True, default="")
    what_to_bring = models.TextField(blank=True, default="")
    organizer_note = models.TextField(blank=True, default="")
    getting_home_note = models.TextField(blank=True, default="")  # F18
    accessibility_notes = models.TextField(blank=True, default="")
    cost_band = models.CharField(
        max_length=16, choices=Activity.CostBand.choices, default=Activity.CostBand.UNSPECIFIED
    )
    difficulty = models.CharField(
        max_length=16, choices=Activity.Difficulty.choices, default=Activity.Difficulty.UNSPECIFIED
    )
    capacity = models.PositiveIntegerField(null=True, blank=True)
    min_to_go = models.PositiveIntegerField(null=True, blank=True)
    join_threshold = models.FloatField(default=DEFAULT_JOIN_THRESHOLD)
    guardian_accompanied = models.BooleanField(default=False)
    # F29: each spawned instance is supervised — the owner's verified guardian must be seated
    # afresh on every instance before joins settle (supervision is re-established per meetup).
    supervised = models.BooleanField(default=False)
    beginners_welcome = models.BooleanField(default=False)

    cadence = models.CharField(max_length=16, choices=Cadence.choices)
    # The start time of the NEXT instance to spawn; advanced by one cadence step after each spawn.
    next_starts_at = models.DateTimeField()
    # The intended local day-of-month for MONTHLY series, captured at create time. Monthly advance
    # clamps from THIS anchor (e.g. 31 -> Feb 28 -> Mar 31), so a "last day" series doesn't decay
    # to the 28th forever after one short month. Unused for weekly/biweekly.
    anchor_day = models.PositiveSmallIntegerField(default=1)
    # Length of each instance in minutes, so every spawn gets a FRESH ends_at = starts + duration
    # (never a stale absolute end time). null = open-ended (no ends_at on the instance).
    duration_minutes = models.PositiveIntegerField(null=True, blank=True)

    status = models.CharField(max_length=16, choices=Status.choices, default=Status.ACTIVE)

    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        verbose_name_plural = "activity series"
        constraints = [
            models.CheckConstraint(
                condition=Q(join_threshold__gt=0) & Q(join_threshold__lte=1),
                name="series_threshold_fraction",
            ),
        ]
        indexes = [
            models.Index(fields=["status", "next_starts_at"]),  # the nightly due-scan
            models.Index(fields=["owner"]),  # "my series"
            models.Index(fields=["cohort", "status"]),
        ]

    def __str__(self):
        return f"{self.title} ({self.cadence})"


class Membership(models.Model):
    """A user's relationship to an activity, with a role and a lifecycle state.

    A `requested` membership is the pending join request that members vote on.
    """

    class Role(models.TextChoices):
        OWNER = "owner", "Owner"
        CO_ORGANIZER = "co_organizer", "Co-organizer"  # F22: owner-granted, adult activities only
        MEMBER = "member", "Member"
        GUARDIAN = "guardian", "Guardian"  # accompanying parent/guardian (supervisory)

    class State(models.TextChoices):
        REQUESTED = "requested", "Requested"
        MEMBER = "member", "Member"
        REMOVED = "removed", "Removed"

    class AttendanceIntent(models.TextChoices):
        # F20: a transient, per-activity go/no-go toggle shown only as an in-activity count
        # to members. Deliberately NOT aggregated into any per-user reliability/attendance
        # history (that would be behavioural tracking). Reset to UNKNOWN when the member leaves.
        UNKNOWN = "unknown", "Not said"
        GOING = "going", "Coming"
        NOT_GOING = "not_going", "Can't make it"

    class TransitStatus(models.TextChoices):
        # W2-F9: an ephemeral, self-declared "I'm en route" cue alongside arrived_at — a fixed
        # enum (no free text, no location, no member-authored string reaches an adult). The
        # late bucket is a server constant ("~10 min"), never a member-entered figure. Cleared
        # by expire_arrivals + reset on leave, so it is a moment-of-meetup nudge, NEVER a
        # punctuality/reliability rollup. Set only forward (NONE→ON_MY_WAY→RUNNING_LATE) so a
        # member emits at most two pings — see services.set_transit_status.
        NONE = "none", "Not said"
        ON_MY_WAY = "on_my_way", "On my way"
        RUNNING_LATE = "running_late", "Running late"

    activity = models.ForeignKey(Activity, on_delete=models.CASCADE, related_name="memberships")
    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="memberships"
    )
    role = models.CharField(max_length=16, choices=Role.choices, default=Role.MEMBER)
    state = models.CharField(max_length=16, choices=State.choices, default=State.REQUESTED)
    attendance_intent = models.CharField(
        max_length=16, choices=AttendanceIntent.choices, default=AttendanceIntent.UNKNOWN
    )
    # F3: ephemeral self-declared arrival ping. Set by mark_arrived within a start-relative
    # window and cleared a few hours after start by expire_arrivals, so it never becomes a
    # standing presence record. NOT geolocation — a tap, not a position.
    arrived_at = models.DateTimeField(null=True, blank=True)
    # W2-F9: ephemeral self-declared "on my way / running late" cue (sibling of arrived_at).
    # Same window + clearing lifecycle, so it is never a standing punctuality record.
    transit_status = models.CharField(
        max_length=16, choices=TransitStatus.choices, default=TransitStatus.NONE
    )
    # F22: a member's private "yes, we met up" tap, allowed ONLY once activity.status ==
    # COMPLETED. A single per-member boolean (null = unset) about whether THE MEETUP happened —
    # never a rating/judgement of any person, NEVER aggregated per-user, NEVER read
    # cross-activity (the aggregate only ever counts IS NOT NULL within ONE activity). Cleared
    # on leave so a removed row carries no signal.
    met_confirmed_at = models.DateTimeField(null=True, blank=True)
    # F39: one-shot marker set inside _admit when this is a genuinely-new joiner's FIRST
    # admitted membership, so the first-timer welcome (a self-dismissing banner + a line on
    # the join notification) fires at most once. null = not (yet) welcomed; never aggregated.
    welcomed_at = models.DateTimeField(null=True, blank=True)

    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)
    decided_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["activity", "user"], name="uq_membership_activity_user"),
        ]
        indexes = [models.Index(fields=["activity", "state"])]

    def __str__(self):
        return f"{self.user} @ {self.activity} ({self.state})"


class JoinVote(models.Model):
    """A current member's vote on a pending join request (the requested Membership)."""

    membership = models.ForeignKey(Membership, on_delete=models.CASCADE, related_name="votes")
    voter = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="join_votes"
    )
    approve = models.BooleanField()
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["membership", "voter"], name="uq_joinvote_membership_voter"),
        ]

    def __str__(self):
        return f"{self.voter} {'+' if self.approve else '-'} {self.membership_id}"


class Group(models.Model):
    """A persistent, cohort-pinned STANDING GROUP — a joinable space on the same
    (Area x taxonomy) coordinate system as a ``communities.Community``, but explicitly
    created with a stored membership and its own thread.

    Activity-SHAPED on purpose: it exposes the same duck-typed attributes the hardened thread
    gates read off an Activity (``owner``/``owner_id``, ``cohort``, ``status``, ``is_hidden``,
    ``memberships``, ``thread``), so ``post_to_thread`` / ``can_read_thread`` work on a Group
    VERBATIM (the single-gate reuse that the One-Thread collapse exists for). The cohort is
    pinned from the creator at creation and is IMMUTABLE — the isolation boundary, never in any
    editable whitelist. Membership counts/rosters are NEVER stored here (see services.group_roster
    for the per-cohort visibility rule); a Group has no per-user reliability/attendance history."""

    class Tier(models.TextChoices):
        TYPE = "type", "Activity type"
        CATEGORY = "category", "Category"

    class Status(models.TextChoices):
        ACTIVE = "active", "Active"
        ARCHIVED = "archived", "Archived"

    # PROTECT-vs-CASCADE: CASCADE, mirroring Activity.owner. A Group is content like an Activity
    # (thread + memberships), and CASCADE keeps GDPR erase_user() working without a system-curator
    # account to reassign to. The destruction is NOT silent: erase_user() writes a
    # `group.owner_erased` audit row per owned group BEFORE deletion, so the hash-chained log keeps
    # a permanent, traceable record even of a moderation-hidden group (target_ref is a string, not
    # an FK, so it survives).
    # Reassign a departing staff curator's groups before offboarding if continuity is required.
    owner = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="owned_groups"
    )
    area = models.ForeignKey("communities.Area", on_delete=models.PROTECT, related_name="groups")
    # The rollup category (always set, = the type's category or the category itself).
    category = models.ForeignKey(
        "taxonomy.ActivityCategory", on_delete=models.PROTECT, related_name="groups"
    )
    # Set ONLY for a TYPE-tier group; NULL marks a CATEGORY-tier rollup group.
    activity_type = models.ForeignKey(
        "taxonomy.ActivityType",
        on_delete=models.PROTECT,
        null=True,
        blank=True,
        related_name="groups",
    )
    tier = models.CharField(max_length=8, choices=Tier.choices)

    # Pinned from the owner's cohort at creation; the isolation boundary. IMMUTABLE.
    cohort = models.CharField(max_length=16, choices=Cohort.choices)

    title = models.CharField(max_length=200)
    description = models.TextField(blank=True)

    status = models.CharField(max_length=16, choices=Status.choices, default=Status.ACTIVE)
    # Set by a moderator REMOVE action (safety.take_action); a hidden group is excluded from
    # every read surface (visible_groups, group_detail, thread gates) but retained for audit.
    is_hidden = models.BooleanField(default=False)
    # True for any CHILD/TEEN group (staff-curated only) and for staff-created adult groups.
    is_staff_curated = models.BooleanField(default=False)

    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        constraints = [
            # At most ONE live group per coordinate per cohort (anti-spam-clone), mirroring the
            # Community one-per-coordinate rule but scoped to ACTIVE groups (an archived group
            # frees the coordinate). Partial unique on the live set.
            models.UniqueConstraint(
                fields=["cohort", "area", "activity_type"],
                condition=Q(status="active", activity_type__isnull=False),
                name="uq_group_type",
            ),
            models.UniqueConstraint(
                fields=["cohort", "area", "category"],
                condition=Q(status="active", activity_type__isnull=True),
                name="uq_group_category",
            ),
            # Tier must agree with whether activity_type is set (mirrors Community).
            models.CheckConstraint(
                condition=(
                    Q(tier="type", activity_type__isnull=False)
                    | Q(tier="category", activity_type__isnull=True)
                ),
                name="group_tier_matches_type",
            ),
        ]
        indexes = [
            models.Index(fields=["cohort", "status"]),  # visible_groups discovery scan
            models.Index(fields=["cohort", "area", "activity_type"]),  # coordinate / linkage lookup
            models.Index(fields=["cohort", "category"]),
        ]

    def __str__(self):
        return f"group({self.title}, {self.cohort})"


class GroupMembership(models.Model):
    """A user's stored membership of a Group. Deliberately SIMPLER than the Activity Membership:

    - Role is OWNER/MEMBER only — **no GUARDIAN role** (a standing group thread is peer-only,
      same-cohort; there is no adult guardian-observer seat, so the leak-prone observer/prune
      machinery the activity surface needs simply does not exist here).
    - State is MEMBER/LEFT/REMOVED — **no REQUESTED/INVITED** (groups are openly joinable; join
      admits straight to MEMBER).
    - **No** ``attendance_intent`` / ``arrived_at`` / ``met_confirmed_at`` / ``last_read_at`` — a
      standing group must never accrue per-user reliability or read-tracking history (inv.2)."""

    class Role(models.TextChoices):
        OWNER = "owner", "Owner"
        MEMBER = "member", "Member"

    class State(models.TextChoices):
        MEMBER = "member", "Member"
        LEFT = "left", "Left"
        REMOVED = "removed", "Removed"

    group = models.ForeignKey(Group, on_delete=models.CASCADE, related_name="memberships")
    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="group_memberships"
    )
    role = models.CharField(max_length=16, choices=Role.choices, default=Role.MEMBER)
    state = models.CharField(max_length=16, choices=State.choices, default=State.MEMBER)
    joined_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["group", "user"], name="uq_group_membership_group_user"),
        ]
        indexes = [
            models.Index(fields=["group", "state"]),  # group_roster / count
            models.Index(fields=["user", "state"]),  # "my groups" + cohort-change eviction sweep
        ]

    def __str__(self):
        return f"{self.user} @ {self.group} ({self.state})"


class GroupQuestionPrompt(models.TextChoices):
    """F30 — the FIXED, closed set of questions a minor-group member may send its staff
    organiser. There is deliberately NO free-text option: a closed enum removes the
    grooming / PII-disclosure vector entirely (a child can never type a name, address, or
    anything else into this channel). It is intentionally tiny and logistics/belonging
    only — anything safety-related goes through the report / "I feel unsafe" path, never
    here. Not a model field: the chosen prompt lives only in the resulting notification +
    the audit row, never in a stored Post."""

    NEXT_MEETUP = "next_meetup", _("When is the next meetup?")
    WHERE = "where", _("Where exactly do we meet?")
    WHAT_TO_BRING = "what_to_bring", _("What should I bring?")
    HOW_IT_WORKS = "how_it_works", _("I'm new here — how does this group work?")
    MORE_INFO = "more_info", _("Could you post more about what's coming up?")


class Thread(models.Model):
    """The text-only discussion thread for an activity OR a group (exactly one owner).

    A Thread is owned by EITHER an ``activity`` or a ``group`` (the XOR constraint), bridged by the
    ``owner_object`` property so the single hardened ``post_to_thread`` / ``can_read_thread`` gates
    read it duck-typed. ``group`` is a OneToOneField (NOT a plain FK) with the same
    ``related_name="thread"`` as ``activity``, so ``group.thread`` resolves to the singular Thread
    object exactly like ``activity.thread`` (a plain FK would make it a RelatedManager and break the
    bridge)."""

    activity = models.OneToOneField(
        Activity, on_delete=models.CASCADE, related_name="thread", null=True, blank=True
    )
    group = models.OneToOneField(
        "Group", on_delete=models.CASCADE, related_name="thread", null=True, blank=True
    )
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            # Exactly one owner: an activity thread XOR a group thread, never both, never neither.
            models.CheckConstraint(
                condition=(
                    Q(activity__isnull=False, group__isnull=True)
                    | Q(activity__isnull=True, group__isnull=False)
                ),
                name="thread_exactly_one_owner",
            ),
        ]

    def __str__(self):
        return f"thread({self.owner_object})"

    @property
    def owner_object(self):
        """The Activity or Group that owns this thread (exactly one is non-null per the XOR
        constraint). The single duck-type bridge the thread gates dispatch on."""
        return self.activity or self.group


class Post(models.Model):
    """A text post in an activity thread. Text-first: no media here (photos are D6).

    The SINGLE durable record for an activity's conversation: realtime delivery (Channels)
    is a transport over committed Posts, never a parallel store. Every write goes through
    ``social.services.post_to_thread`` / ``post_announcement`` (the only two creators)."""

    thread = models.ForeignKey(Thread, on_delete=models.CASCADE, related_name="posts")
    author = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="posts"
    )
    body = models.TextField()
    # An owner-only pinned broadcast ("meet at the north gate", "time changed"): surfaced
    # above the ordinary thread and accompanied by a one-off notification to every member.
    is_announcement = models.BooleanField(default=False)
    # Set by a moderator REMOVE action (or an author self-delete); hidden posts are excluded
    # from thread reads but retained for audit/appeal.
    is_hidden = models.BooleanField(default=False)
    # WhatsApp-style one-level quote-reply. SET_NULL (not CASCADE) so moderator-hiding or
    # GDPR-erasing a parent never destroys a child's coordination text — an orphaned reply
    # renders as a plain top-level post. Depth is capped at ONE LEVEL IN THE SERVICE
    # (post_to_thread re-parents a reply-to-a-reply onto the top-level ancestor), so this
    # column only ever points at a top-level Post — a 2-level render with bounded index
    # scans, never a recursive CTE. The quoted snippet is NEVER stored here; it is derived
    # live from the current parent.body at render/serialize time, so editing or hiding a
    # parent immediately changes what its replies show.
    reply_to = models.ForeignKey(
        "self", on_delete=models.SET_NULL, null=True, blank=True, related_name="replies"
    )
    # W6 "share into the conversation": an optional structured reference to ONE in-app
    # object (an activity / a venue / an event), rendered as a card. SET_NULL — deleting
    # the target degrades the post to its plain text, never destroys conversation. The
    # card is RE-GATED AT RENDER TIME (hidden activity / unpublished place → an
    # "unavailable" stub), so a share can never outlive its target's visibility.
    # Sharing a venue is the privacy-safe "send a location": a public Place card, never
    # anyone's coordinates (inv.4 — user location is never stored).
    shared_activity = models.ForeignKey(
        Activity,
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="shared_in_posts",
    )
    shared_place = models.ForeignKey(
        "places.Place",
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="shared_in_posts",
    )
    shared_event = models.ForeignKey(
        "events.Event",
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="shared_in_posts",
    )
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        ordering = ["created_at"]
        indexes = [
            # Chronological/digest read (top-level page = reply_to IS NULL, ordered by time).
            models.Index(fields=["thread", "reply_to", "created_at"]),
            # Retained covering scan for the digest's whole-thread chronological pass.
            models.Index(fields=["thread", "created_at"]),
        ]

    def __str__(self):
        return f"post({self.author} @ {self.thread_id})"


class PostReaction(models.Model):
    """An emoji acknowledgement of a thread Post. Deliberately ANONYMOUS + COUNTLESS at the
    read surface: the thread renders only the DISTINCT set of emojis present on a post (never how
    many, never who). The rows exist so a user can toggle their OWN reaction and so a moderator
    can inspect, but `post_reaction_emojis` exposes neither a count nor a who-list — that keeps a
    reaction a neutral ack, not a per-user popularity/affinity signal among minors (inv.2). The
    encrypted-DM surface, by contrast, shows who-reacted-with-what, but that lives entirely
    client-side inside the ciphertext the server relays — there is no PostReaction there."""

    post = models.ForeignKey(Post, on_delete=models.CASCADE, related_name="reactions")
    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="post_reactions"
    )
    emoji = models.CharField(max_length=8)  # one of a fixed, non-extensible set (no custom emoji)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["post", "user", "emoji"], name="uq_post_reaction"),
        ]
        indexes = [models.Index(fields=["post"])]

    def __str__(self):
        return f"reaction({self.emoji} on {self.post_id})"


class UserPlaceProposal(models.Model):
    """A user-submitted place awaiting a multi-user quorum before it goes public.

    Co-creation: N independent users (not the proposer) must confirm before the
    place is published, plugging into D1's `Place.source="user"` seam.
    """

    class Status(models.TextChoices):
        PENDING = "pending", "Pending"
        PUBLISHED = "published", "Published"
        REJECTED = "rejected", "Rejected"

    place = models.OneToOneField("places.Place", on_delete=models.CASCADE, related_name="proposal")
    proposer = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="place_proposals"
    )
    required_confirmations = models.PositiveIntegerField(default=DEFAULT_PLACE_QUORUM)
    status = models.CharField(max_length=16, choices=Status.choices, default=Status.PENDING)
    created_at = models.DateTimeField(auto_now_add=True)
    published_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        constraints = [
            models.CheckConstraint(
                condition=Q(required_confirmations__gte=1),
                name="proposal_quorum_positive",
            ),
        ]

    def __str__(self):
        return f"proposal({self.place_id}, {self.status})"


class PlaceConfirmation(models.Model):
    """An independent user's confirmation of a proposed place."""

    proposal = models.ForeignKey(
        UserPlaceProposal, on_delete=models.CASCADE, related_name="confirmations"
    )
    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="place_confirmations"
    )
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["proposal", "user"], name="uq_confirmation_proposal_user"),
        ]

    def __str__(self):
        return f"confirm({self.proposal_id} by {self.user})"


class ActivityInterest(models.Model):
    """F27 — an EPHEMERAL "I'd come" gauge for a place + type + coarse time. The throwaway
    proto-meetup sibling of the persistent ``Group``: a proposer floats it, same-cohort peers
    signal interest, and once a few do the proposer converts it into a real ``Activity`` (the
    normal ``create_activity`` path). A failed gauge simply expires — silent, no dead room.

    Deliberately MINIMAL and isolated from the membership graph:
    - ``interested_users`` is a plain M2M that is NEVER ``Membership`` — so it can NOT establish
      a "shared activity" and can NEVER feed ``connections.can_connect`` (pinned by a test).
    - the interest is a COUNT only (no roster is ever exposed — who signalled stays private).
    - ``cohort`` is pinned from the proposer (the isolation boundary); ``coarse_window`` is a
      fixed choice (no free text / no precise time / no PII); ``expires_at`` caps its lifetime
      and ``expire_interest`` deletes stale rows so it never accretes into a standing surface.
    """

    class CoarseWindow(models.TextChoices):
        WEEKDAY_DAYTIME = "weekday_daytime", "Weekday daytime"
        WEEKDAY_EVENING = "weekday_evening", "Weekday evening"
        WEEKEND_DAYTIME = "weekend_daytime", "Weekend daytime"
        WEEKEND_EVENING = "weekend_evening", "Weekend evening"

    proposer = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="proposed_interests"
    )
    place = models.ForeignKey("places.Place", on_delete=models.PROTECT, related_name="interests")
    activity_type = models.ForeignKey(
        "taxonomy.ActivityType", on_delete=models.PROTECT, related_name="interests"
    )
    # Pinned from the proposer's cohort at creation; the isolation boundary. IMMUTABLE.
    cohort = models.CharField(max_length=16, choices=Cohort.choices)
    coarse_window = models.CharField(max_length=16, choices=CoarseWindow.choices)
    # The ephemeral signal set. NOT a Membership — invisible to connections.shares_activity.
    interested_users = models.ManyToManyField(
        settings.AUTH_USER_MODEL, related_name="gauge_interests", blank=True
    )
    # Set when the proposer converts the gauge into a real meetup; SET_NULL so deleting the
    # spawned Activity never deletes the (soon-expiring) gauge row.
    converted_activity = models.ForeignKey(
        "social.Activity",
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="converted_from_interest",
    )
    expires_at = models.DateTimeField()
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        indexes = [
            # visible_gauges discovery scan: active gauges in a cohort, soonest-expiring last.
            models.Index(fields=["cohort", "expires_at"]),
        ]

    def __str__(self):
        return f"interest({self.activity_type_id} @ {self.place_id}, {self.cohort})"
