// Command qcs switches coding agents between their official setup and
// custom providers.
package main

import (
	"os"

	"github.com/zzf-857/quickerconfigswitch/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
