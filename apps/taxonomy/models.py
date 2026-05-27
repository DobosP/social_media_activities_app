from django.db import models
from django.db.models import CheckConstraint, F, Q, UniqueConstraint


class ActivityCategory(models.Model):
    """Broad grouping (Sport, Tabletop, Reading, ...).

    Self-referential `parent` expresses the is-a hierarchy among categories
    (e.g. 'Team Sport' is-a 'Sport').
    """

    slug = models.SlugField(max_length=64, unique=True)
    name = models.CharField(max_length=128)
    parent = models.ForeignKey(
        "self",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="children",
    )
    description = models.TextField(blank=True)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        verbose_name_plural = "activity categories"
        constraints = [
            CheckConstraint(condition=~Q(parent=F("id")), name="category_not_self_parent"),
        ]

    def __str__(self):
        return self.name


class ActivityType(models.Model):
    """A concrete activity a place can support: basketball, table_tennis, ..."""

    slug = models.SlugField(max_length=64, unique=True)
    name = models.CharField(max_length=128)
    category = models.ForeignKey(
        ActivityCategory,
        on_delete=models.PROTECT,
        related_name="activity_types",
    )
    parent = models.ForeignKey(
        "self",
        null=True,
        blank=True,
        on_delete=models.PROTECT,
        related_name="children",
    )
    aliases = models.JSONField(default=list, blank=True)
    is_active = models.BooleanField(default=True)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        constraints = [
            CheckConstraint(condition=~Q(parent=F("id")), name="type_not_self_parent"),
        ]
        indexes = [models.Index(fields=["category"])]

    def __str__(self):
        return self.name


class ActivityRelation(models.Model):
    """A typed edge between two ActivityTypes — the lateral 'knowledge graph'
    links that the is-a adjacency cannot express (related/synonym/variant)."""

    class Kind(models.TextChoices):
        RELATED = "related", "Related to"
        SYNONYM = "synonym", "Synonym of"
        VARIANT = "variant", "Variant of"
        REQUIRES = "requires", "Requires"

    source = models.ForeignKey(ActivityType, on_delete=models.CASCADE, related_name="relations_out")
    target = models.ForeignKey(ActivityType, on_delete=models.CASCADE, related_name="relations_in")
    kind = models.CharField(max_length=16, choices=Kind.choices, default=Kind.RELATED)
    symmetric = models.BooleanField(default=True)
    note = models.CharField(max_length=255, blank=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["source", "target", "kind"], name="uq_activity_relation"),
            CheckConstraint(condition=~Q(source=F("target")), name="relation_not_self"),
        ]

    def __str__(self):
        return f"{self.source.slug} -{self.kind}-> {self.target.slug}"
