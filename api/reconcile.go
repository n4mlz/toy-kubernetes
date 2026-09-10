package api

import (
	"context"
	"log"
	"time"
)

// 観測した状態を desired state に近づける 共通の interface
type Reconciler interface {
	Reconcile(context.Context) error
}

type WatchFunc func(context.Context) (<-chan error, error)

// watch を確立してから Reconcile を実行し、watch event を契機に再実行する
// 先に Reconcile すると、一覧取得と watch 開始の間に発生した変更を取り逃がす
func Run(ctx context.Context, reconciler Reconciler, watch WatchFunc) error {
	for {
		events, err := watch(ctx)
		if err != nil {
			if !retry(ctx, "open watch", err) {
				return ctx.Err()
			}
			continue
		}
		if err := reconciler.Reconcile(ctx); err != nil {
			if !retry(ctx, "reconcile", err) {
				return ctx.Err()
			}
			continue
		}
		select {
		case err, ok := <-events:
			if ok && err != nil {
				if !retry(ctx, "watch", err) {
					return ctx.Err()
				}
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func retry(ctx context.Context, operation string, err error) bool {
	log.Printf("%s: %v; retrying", operation, err)
	select {
	case <-time.After(100 * time.Millisecond):
		return true
	case <-ctx.Done():
		return false
	}
}

// 複数 resource の watch を一つの再 reconcile 通知にまとめる
func CombineWatches(ctx context.Context, watches ...WatchFunc) (<-chan error, error) {
	watchContext, cancel := context.WithCancel(ctx)
	// watch event または親 context の終了時に goroutine から cancel する
	// 成功時の戻り値からも cancel が使われるため、静的解析にもその関係を示す
	_ = cancel
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
