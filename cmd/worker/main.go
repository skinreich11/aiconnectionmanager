package main

import (
	"os"

	"aiconnector/internal/app"
)

func main() {
	if exitCode := app.Run(os.Args[1:], os.Stdout, os.Stderr); exitCode != 0 {
		os.Exit(exitCode)
	}
}
