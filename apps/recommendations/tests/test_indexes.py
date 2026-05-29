from pgvector.django import HnswIndex

from apps.recommendations.models import ActivityEmbedding


def test_activity_embedding_has_hnsw_ann_index():
    indexes = {idx.name: idx for idx in ActivityEmbedding._meta.indexes}
    assert "actemb_vector_hnsw" in indexes

    idx = indexes["actemb_vector_hnsw"]
    assert isinstance(idx, HnswIndex)
    assert idx.fields == ["vector"]
    assert idx.m == 16
    assert idx.ef_construction == 64
    assert idx.opclasses == ["vector_cosine_ops"]
