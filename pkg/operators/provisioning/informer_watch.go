package provisioning

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	versioned "github.com/grafana/grafana/apps/provisioning/pkg/generated/clientset/versioned"
	informers "github.com/grafana/grafana/apps/provisioning/pkg/generated/informers/externalversions"
	provinformer "github.com/grafana/grafana/apps/provisioning/pkg/informer"
	"github.com/grafana/grafana/pkg/infra/nats"
	"github.com/grafana/grafana/pkg/registry/apis/provisioning/informer"
	"github.com/grafana/grafana/pkg/setting"
)

// newInformerFactory builds the provisioning informer factory for the operators.
// When a NATS subscriber is present and enabled, the informers keep their
// LIST-seeded caches but take their watch deltas from a NATS consumer instead of
// the apiserver watch.
func newInformerFactory(client versioned.Interface, resync time.Duration, subscriber nats.Subscriber) informers.SharedInformerFactory {
	if subscriber != nil && subscriber.Enabled() {
		watchFn := informer.NewConsumer(subscriber).Watch
		return informers.NewSharedInformerFactory(provinformer.WrapClient(client, watchFn), resync)
	}
	return provinformer.NewInformerFactory(client, resync)
}

// newNATSSubscriber builds the shared NATS subscriber from configuration. It
// connects lazily on first Subscribe and is a no-op transport when NATS is
// disabled (Enabled() reports false, so newInformerFactory falls back to the
// apiserver watch). Operators run against an external NATS, so no embedded
// server is started here.
func newNATSSubscriber(cfg *setting.Cfg, reg prometheus.Registerer) (nats.Subscriber, error) {
	server, err := nats.ProvideServer(cfg, nil, reg)
	if err != nil {
		return nil, err
	}
	return nats.ProvideSubscriber(cfg, nats.ProvideNATSConfig(cfg, server), reg), nil
}
