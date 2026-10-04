package platform

import (
	"context"
	"strings"
	"testing"
)

func TestMultipartMemoryBudgetIsAggregateAndRequestLocal(t *testing.T) {
	ctx := WithDataUploadLimit(context.Background(), 6)
	b := NewUploadBudget(ctx)
	if _, err := b.ReadField(strings.NewReader("four"), 10); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReadField(strings.NewReader("two"), 10); err == nil {
		t.Fatal("individually small fields exceeded aggregate cap")
	}
	if _, err := NewUploadBudget(ctx).ReadField(strings.NewReader("sixxxx"), 10); err != nil {
		t.Fatal("budget crossed requests")
	}
	if _, err := NewUploadBudget(WithDataUploadLimit(ctx, -1)).ReadField(strings.NewReader("x"), 10); err == nil {
		t.Fatal("invalid cap did not fail closed")
	}
}
