package main

import (
	"github.com/tendermint/tendermint/cmd/tendermint/commands"
	nm "github.com/tendermint/tendermint/node"
)

func main() {
	cmd := commands.Cmd(commands.WithNodeFunc(nm.DefaultNewNode))
	if err := cmd.Execute(); err != nil {
		panic(err)
	}
}
