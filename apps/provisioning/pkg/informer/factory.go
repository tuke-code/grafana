package informer

import (
	"time"

	versioned "github.com/grafana/grafana/apps/provisioning/pkg/generated/clientset/versioned"
	informers "github.com/grafana/grafana/apps/provisioning/pkg/generated/informers/externalversions"
)

// NewInformerFactory builds the standard provisioning SharedInformerFactory,
// backed by the apiserver LIST+WATCH. It is exactly
// informers.NewSharedInformerFactory and exists so callers can pick between the
// apiserver and NATS variants (the latter lives with the NATS consumer, which
// depends on NATS and therefore cannot live in this module).
func NewInformerFactory(client versioned.Interface, resync time.Duration) informers.SharedInformerFactory {
	return informers.NewSharedInformerFactory(client, resync)
}
