package main

import (
	"os"

	ceremonyapp "github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/app"
)

func main() {
	ceremonyapp.ExitWithError(ceremonyapp.RunCeremonyCLI(os.Args[1:]))
}
