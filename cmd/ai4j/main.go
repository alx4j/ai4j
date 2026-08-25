package main

import (
	"io"
	"os"

	"github.com/alx4j/ai4j/internal/app"
)

type applicationRunner func([]string, io.Reader, io.Writer, io.Writer) int

func main() {
	os.Exit(run(app.Run, os.Args, os.Stdin, os.Stdout, os.Stderr))
}

func run(runner applicationRunner, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runner(args, stdin, stdout, stderr)
}
