// Package informer defines the generic seam for swapping a Kubernetes
// informer's watch transport. It depends only on k8s apimachinery, so any typed
// informer can reuse it: WatchFunc is the injection point an informer's Watch is
// routed through, and GetFunc lets a transport materialize objects by name.
package informer

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

// GetFunc fetches the current typed object for a name in a namespace. A
// metadata-only transport (e.g. NATS) calls it to materialize the object after
// a change notification.
type GetFunc func(ctx context.Context, name string, opts metav1.GetOptions) (runtime.Object, error)

// WatchFunc produces the delta source (a watch.Interface) for a single resource
// type and namespace. It is the injection point used to replace an informer's
// watch transport without changing how its cache is seeded by LIST. get resolves
// the current object by name at the informer's own version, so a metadata-only
// transport can materialize notifications into the objects the SharedInformer
// expects.
type WatchFunc func(ctx context.Context, gvr schema.GroupVersionResource, namespace string, get GetFunc, opts metav1.ListOptions) (watch.Interface, error)
