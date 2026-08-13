package main

import "github.com/W0rkingChr1s/dredge/cmd"

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cmd.Execute(version)
}
