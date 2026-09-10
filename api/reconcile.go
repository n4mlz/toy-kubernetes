package api

import "context"

// 観測した状態を desired state に近づける 共通の interface
type Reconciler interface {
	Reconcile(context.Context) error
}

type WatchFunc func(context.Context) (<-chan error, error)

// Reconcile を一度実行した後、watch event を契機に再実行する
func Run(ctx context.Context, reconciler Reconciler, watch WatchFunc) error {
	for {
		if err := reconciler.Reconcile(ctx); err != nil {
			return err
		}

		events, err := watch(ctx)
		if err != nil {
			return err
		}
		select {
		case err, ok := <-events:
			if ok && err != nil {
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// 複数 resource の watch を一つの再 reconcile 通知にまとめる
func CombineWatches(ctx context.Context, watches ...WatchFunc) (<-chan error, error) {
	watchContext, cancel := context.WithCancel(ctx)
	events := make(chan error, 1)

	for _, watch := range watches {
		watchEvents, err := watch(watchContext)
		if err != nil {
			cancel()
			return nil, err
		}
		go func() {
			err, ok := <-watchEvents
			if !ok {
				err = nil
			}
			select {
			case events <- err:
				cancel()
			case <-watchContext.Done():
			}
		}()
	}

	return events, nil
}
