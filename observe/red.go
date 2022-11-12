package observe

import (
	"fmt"
	"time"

	"github.com/go-kit/kit/metrics"
	"github.com/go-kit/kit/metrics/prometheus"
	stdprometheus "github.com/prometheus/client_golang/prometheus"
)

func NewRED(namespace, subsystem, opName string, labelsAndVals ...string) RED {
	labels := []string{}
	for i := 0; i < len(labelsAndVals); i += 2 {
		labels = append(labels, labelsAndVals[i])
	}
	return RED{
		Reqs: prometheus.NewCounterFrom(stdprometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      fmt.Sprintf("%s_attempts_total", opName),
			Help:      fmt.Sprintf("Number of times %s was called", opName),
		}, labels).With(labelsAndVals...),
		Errs: prometheus.NewCounterFrom(stdprometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      fmt.Sprintf("%s_errors_total", opName),
			Help:      fmt.Sprintf("Number of times %s errored", opName),
		}, labels).With(labelsAndVals...),
		Duration: prometheus.NewHistogramFrom(stdprometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      fmt.Sprintf("%s_dur_seconds", opName),
		}, labels).With(labelsAndVals...),
	}
}

type RED struct {
	Reqs     metrics.Counter
	Errs     metrics.Counter
	Duration metrics.Histogram
}

func (m RED) Incr() func(err error) error {
	start := time.Now()
	m.Reqs.Add(1)
	return func(err error) error {
		if err != nil {
			m.Errs.Add(1)
		}
		m.Duration.Observe(time.Since(start).Seconds())
		return err
	}
}
