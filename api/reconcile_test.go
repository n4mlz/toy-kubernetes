package api

import (
	"context"
	"testing"
)

type testReconciler struct {
	reconciled chan struct{}
}

func (reconciler *testReconciler) Reconcile(context.Context) error {
	select {
	case reconciler.reconciled <- struct{}{}:
	default:
	}
	return nil
}

func TestRunReconcilesAfterWatchEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reconciler := &testReconciler{reconciled: make(chan struct{}, 2)}
	watchCount := 0

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, reconciler, func(context.Context) (<-chan error, error) {
			watchCount++
			events := make(chan error, 1)
			if watchCount == 1 {
				events <- nil
			}
			return events, nil
		})
	}()

	for range 2 {
		select {
		case <-reconciler.reconciled:
		case <-ctx.Done():
			t.Fatal("watch event should trigger a second reconcile")
		}
	}
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run should stop cleanly: %v", err)
	}
}
