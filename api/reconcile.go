package api

import "context"

// 観測した状態を desired state に近づける 共通の interface
type Reconciler interface {
	Reconcile(context.Context) error
}
