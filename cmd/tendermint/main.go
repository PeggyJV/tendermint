package main

import (
	"github.com/tendermint/tendermint/cmd/tendermint/commands"
)

func main() {
	cmd := commands.Cmd()
	if err := cmd.Execute(); err != nil {
		panic(err)
	}
}
