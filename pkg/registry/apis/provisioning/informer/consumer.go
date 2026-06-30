package informer

import (
	"context"
	"fmt"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"

	k8sinformer "github.com/grafana/grafana/pkg/apimachinery/informer"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/infra/nats"
	"github.com/grafana/grafana/pkg/storage/unified/resourcewatch"
)

// notification is a raw message bridged from the NATS subscriber to the decoder.
type notification struct {
	subject string
	data    []byte
}

// watchChanBuffer bounds how many notifications can queue per watch before the
// NATS subscriber drops the slowest. The informer's event handler drains
// promptly, so a small buffer absorbs bursts without unbounded memory growth.
const watchChanBuffer = 256

// defaultRelistInterval is how often a NATS watch ends itself so the reflector
// re-LISTs. The relist is the only thing that reconciles changes NATS does not
// deliver live — chiefly hard deletes — and it heals any notifications dropped
// by best-effort delivery. A clean watch close would merely reconnect, and the
// informer's resyncPeriod only replays the existing store, so neither updates
// the cache for deletions; only a relist does.
const defaultRelistInterval = 5 * time.Minute

// Consumer is a prototype NATS-based watch transport for the provisioning
// informers. It plugs into the watch-swap seam in this package: Watch subscribes
// to the per-resource NATS subject defined by the resourcewatch contract and
// turns each notification into a watch event, so the informers' delta source
// comes from NATS while their caches keep being seeded by the apiserver LIST.
//
// NATS carries only metadata (see resourcewatch.Event), so the Consumer
// materializes every notification by issuing a GET at the informer's own
// version — this is what makes the transport version-agnostic and keeps the
// object faithful (full metadata, finalizers, spec). A notification whose object
// can no longer be fetched is dropped rather than fabricated.
//
// Delivery is best-effort (no JetStream): only notifications published while a
// watch is open are observed, and hard deletes are not delivered at all. The
// cache is kept correct by ending each watch with a Gone error every
// relistInterval, which makes the reflector re-LIST and reconcile everything
// (deletions included) against a fresh snapshot.
type Consumer struct {
	subscriber     nats.Subscriber
	log            log.Logger
	relistInterval time.Duration
}

// NewConsumer builds a Consumer over the shared NATS subscriber. The subscriber
// owns the connection lifecycle; the Consumer only opens per-watch
// subscriptions through it.
func NewConsumer(subscriber nats.Subscriber) *Consumer {
	return &Consumer{
		subscriber:     subscriber,
		log:            log.New("provisioning.informer.nats"),
		relistInterval: defaultRelistInterval,
	}
}

// Watch implements WatchFunc. It subscribes to the resource's NATS subject and
// returns a watch.Interface that materializes each metadata notification into a
// watch event by fetching the current object with get. The watch ends itself
// with a Gone error after relistInterval so the reflector re-LISTs. opts is
// ignored — NATS carries only live notifications, so there is no resourceVersion
// to resume from.
func (c *Consumer) Watch(ctx context.Context, gvr schema.GroupVersionResource, namespace string, get k8sinformer.GetFunc, _ metav1.ListOptions) (watch.Interface, error) {
	if get == nil {
		return nil, fmt.Errorf("nats consumer: missing Get for %s", gvr.String())
	}

	subject := resourcewatch.Subject(gvr, namespace)
	watchCtx, cancel := context.WithCancel(ctx)

	// The subscriber invokes handler on its own delivery goroutine. Hand each
	// raw notification to the decoder over a buffered channel; if the decoder is
	// not draining fast enough, drop rather than block delivery for other watches
	// — the periodic relist heals the gap. The data slice is owned by the
	// subscriber once handler returns, so copy it before queueing.
	msgs := make(chan notification, watchChanBuffer)
	handler := func(subject string, data []byte) {
		buf := make([]byte, len(data))
		copy(buf, data)
		select {
		case msgs <- notification{subject: subject, data: buf}:
		case <-watchCtx.Done():
		default:
			c.log.Warn("dropping nats notification; watch buffer full", "subject", subject)
		}
	}

	sub, err := c.subscriber.Subscribe(watchCtx, subject, handler)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("nats consumer: subscribe %q: %w", subject, err)
	}
	c.log.Debug("opened nats watch", "subject", subject, "gvr", gvr.String())

	decoder := &decoder{
		ctx:    watchCtx,
		cancel: cancel,
		msgs:   msgs,
		sub:    sub,
		get:    get,
		log:    c.log,
		timer:  time.NewTimer(c.relistInterval),
	}
	// A Gone (410) status makes the reflector treat the watch as expired and
	// re-LIST, rather than merely reconnecting it.
	reporter := apierrors.NewClientErrorReporter(http.StatusGone, "WATCH", "Expired")
	return watch.NewStreamWatcher(decoder, reporter), nil
}
