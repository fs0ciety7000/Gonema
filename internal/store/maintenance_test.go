package store_test

import (
	"context"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/store"
	"github.com/fs0ciety7000/gonema/internal/store/storetest"
)

func TestQuickCheckAndWALSize(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	problems, err := st.QuickCheck(ctx)
	if err != nil || len(problems) != 0 {
		t.Fatalf("quick_check = %v, %v", problems, err)
	}
	if err := store.SetSetting(ctx, st, "maintenance.probe", 1); err != nil {
		t.Fatal(err)
	}
	n, err := st.WALSize()
	if err != nil || n <= 0 {
		t.Errorf("WAL size after a write = %d, %v", n, err)
	}
	if st.Path() == "" {
		t.Error("empty path")
	}
}
