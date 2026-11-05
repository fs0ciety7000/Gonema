// Command gonema is a self-hosted movie collection manager.
package main

import (
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("gonema", version)
		return
	}
	fmt.Fprintln(os.Stderr, "gonema: server not implemented yet (phase 3+)")
	os.Exit(1)
}
