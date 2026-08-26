package main

import (
	"os"

	"github.com/alx4j/ai4j/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}
