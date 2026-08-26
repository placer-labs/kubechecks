package diff

import "github.com/prometheus/client_golang/prometheus"

var (
	diffLabels = []string{"application"}

	serverSideDiffSuccess = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Name:      "server_side_diff_success",
			Help:      "Count of applications diffed via the ArgoCD server-side diff API",
		},
		diffLabels,
	)
	// Fallbacks are silent from the caller's point of view -- the check still
	// succeeds using the local diff -- so this counter is the only way to see
	// that results are being computed the less accurate way.
	serverSideDiffFallback = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "kubechecks",
			Name:      "server_side_diff_fallback",
			Help:      "Count of applications that fell back to the local diff after a server-side diff failure",
		},
		diffLabels,
	)
)

func init() {
	r := prometheus.DefaultRegisterer
	r.MustRegister(serverSideDiffSuccess)
	r.MustRegister(serverSideDiffFallback)
}
