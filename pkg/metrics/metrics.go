package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "gitlab_token_rotate"

var Secrets = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "secrets",
		Help:      "A gauge for the total number of secrets",
	},
	[]string{"secret_namespace", "secret"},
)

var LastUpdated = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "secrets_last_updated",
		Help:      "A gauge for the last updated time of secrets",
	},
	[]string{"secret_namespace", "secret"},
)

var Errors = promauto.NewCounter(
	prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "errors_total",
		Help:      "A counter for the total number of errors",
	},
)

func GetHandler() http.Handler {
	return promhttp.Handler()
}
