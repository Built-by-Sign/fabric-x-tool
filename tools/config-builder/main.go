package main

import (
	"os"

	"github.com/ethsign/cbdc-chain/cbdc-network/config-builder/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
