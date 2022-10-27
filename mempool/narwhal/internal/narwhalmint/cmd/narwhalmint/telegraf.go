package main

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

func (b *builder) cmdTelegrafConfig() *cobra.Command {
	cmd := cobra.Command{
		Use: "telegraf-config",
		RunE: func(cmd *cobra.Command, args []string) error {
			ipAddrs, err := getIPsFromStdIn(cmd.InOrStdin())
			if err != nil {
				return err
			}
			for i := range ipAddrs {
				ipAddrs[i] = fmt.Sprintf(`"http://%s:%d"`, ipAddrs[i], b.metricsPort)
			}
			return telegrafTmpl.Execute(cmd.OutOrStdout(), map[string]any{
				"IPs":       strings.Join(ipAddrs, ","),
				"TestnetID": b.testnetID,
			})
		},
	}
	cmd.Flags().StringVar(&b.testnetID, "testnet-id", "", "testnet id")
	b.registerTMMetricsPort(&cmd)
	cobra.MarkFlagRequired(cmd.Flags(), "metrics-port")

	return &cmd
}

var telegrafTmpl = template.Must(template.New("telegraf").
	Parse(`[[inputs.prometheus]]
urls = [{{ .IPs }}]
metric_version = 2

[[processors.override]]
  [processors.override.tags]
    testnet_id = "{{ .TestnetID }}"

## Write Prometheus formatted metrics to InfluxDB
[[outputs.influxdb_v2]]
urls = ["http://localhost:8086"]
token = "$INFLUXDB_TOKEN"
organization = "peggy"
bucket = "tm"
`))
