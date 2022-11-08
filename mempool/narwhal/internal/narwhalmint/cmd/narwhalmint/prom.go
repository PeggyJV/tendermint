package main

import (
	"net"
	"strconv"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

func (b *builder) cmdPromConfig() *cobra.Command {
	cmd := cobra.Command{
		Use: "prom-config",
		RunE: func(cmd *cobra.Command, args []string) error {
			ipAddrs, err := getIPsFromStdIn(cmd.InOrStdin())
			if err != nil {
				return err
			}

			var tm, primaries, workers []string
			for _, ip := range ipAddrs {
				if b.tmMetricsPort > -1 {
					tm = append(tm, hostport(ip, b.tmMetricsPort))
				}
				if b.narwhalPrimaryMetricsPort > -1 {
					primaries = append(primaries, hostport(ip, b.narwhalPrimaryMetricsPort))
				}
				if b.narwhalWorkerMetricsPort > -1 {
					workers = append(workers, hostport(ip, b.narwhalWorkerMetricsPort))
				}
			}
			return promTmpl.Execute(cmd.OutOrStdout(), map[string]any{
				"TMIPs":      strings.Join(tm, ", "),
				"PrimaryIPs": strings.Join(primaries, ", "),
				"WorkerIPs":  strings.Join(workers, ", "),
			})
		},
	}
	b.registerNarwhalMetricsPort(&cmd)
	b.registerTMMetricsPort(&cmd)
	cobra.MarkFlagRequired(cmd.Flags(), "tm-metrics-port")

	return &cmd
}

func hostport(host string, port int) string {
	return `"` + net.JoinHostPort(host, strconv.Itoa(port)) + `"`
}

var promTmpl = template.Must(template.New("prom").
	Parse(`global:
  scrape_interval: 10s
  scrape_timeout: 10s
  evaluation_interval: 15s

scrape_configs:
  - job_name: prom
    honor_timestamps: true
    scrape_interval: 15s
    metrics_path: /metrics
    scheme: http
    static_configs:
      - targets:
          - localhost:9090
  - job_name: prompush
    honor_timestamps: true
    scrape_interval: 1s
    metrics_path: /metrics
    scheme: http
    static_configs:
      - targets:
          - pushgateway:9091
  - job_name: tendermint
    honor_timestamps: true
    scrape_interval: 5s
    metrics_path: /metrics
    scheme: http
    static_configs:
      - targets: [ {{ .TMIPs }} ]
  - job_name: narwhal_primary
    honor_timestamps: true
    scrape_interval: 5s
    metrics_path: /metrics
    scheme: http
    static_configs:
      - targets: [ {{ .PrimaryIPs }} ]
  - job_name: narwhal_worker
    honor_timestamps: true
    scrape_interval: 5s
    metrics_path: /metrics
    scheme: http
    static_configs:
      - targets: [ {{ .WorkerIPs }} ]
`))
