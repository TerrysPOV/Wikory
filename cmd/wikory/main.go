// Command wikory is the Wikory memory engine for coding agents.
package main

import (
	"os"

	"github.com/TerrysPOV/Wikory/internal/app"
	_ "github.com/TerrysPOV/Wikory/internal/deps"
)

// version is set at release time with -ldflags "-X main.version=..."
var version = "0.0.0-dev"

func main() {
	os.Exit(app.Run(os.Args[1:], app.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, version))
}
