package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func RegisterPrometheus(r *http.ServeMux) {
	r.Handle("/metrics", promhttp.Handler())
}
