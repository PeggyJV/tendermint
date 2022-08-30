package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	tmjson "github.com/tendermint/tendermint/libs/json"
	"github.com/tendermint/tendermint/p2p/upnp"
)

// probeUpnpCmd adds capabilities to test the UPnP functionality.
func (b *builderRoot) probeUpnpCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "probe-upnp",
		Aliases: []string{"probe_upnp"},
		Short:   "Test UPnP functionality",
		RunE:    b.probeUpnp,
		PreRun:  deprecateSnakeCase,
	}
}

func (b *builderRoot) probeUpnp(cmd *cobra.Command, args []string) error {
	capabilities, err := upnp.Probe(b.logger)
	if err != nil {
		fmt.Println("Probe failed: ", err)
	} else {
		fmt.Println("Probe success!")
		jsonBytes, err := tmjson.Marshal(capabilities)
		if err != nil {
			return err
		}
		fmt.Println(string(jsonBytes))
	}
	return nil
}
