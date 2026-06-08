package contracts

// Counter is a monotonically increasing metric.
type Counter interface {
	Inc()
	Add(delta float64)
}

// Gauge is a metric that can increase and decrease.
type Gauge interface {
	Set(value float64)
	Inc()
	Dec()
	Add(delta float64)
}

// Histogram observes values and tracks distribution.
type Histogram interface {
	Observe(value float64)
}

// MetricsProvider exposes application metrics.
// Modules implement this to provide Prometheus, Datadog, or other
// metrics backends. Modules that want to expose metrics call these
// methods to register and update metrics. If no MetricsProvider is
// registered, metric calls are no-ops.
type MetricsProvider interface {
	// Counter creates or returns a counter with the given name and labels.
	Counter(name string, labels map[string]string) Counter
	// Gauge creates or returns a gauge with the given name and labels.
	Gauge(name string, labels map[string]string) Gauge
	// Histogram creates or returns a histogram with the given name, labels, and buckets.
	Histogram(name string, labels map[string]string, buckets []float64) Histogram
}
