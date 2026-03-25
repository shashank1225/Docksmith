package cmd

import (
	"errors"
	"flag"
	"io"

	"docksmith/engine"
)

func HandleBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var tag string
	fs.StringVar(&tag, "t", "", "image name and tag")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if tag == "" {
		return errors.New("missing required -t <name:tag>")
	}

	remaining := fs.Args()
	if len(remaining) != 1 {
		return errors.New("build requires exactly one context path")
	}

	return Build(tag, remaining[0])
}

func Build(tag string, context string) error {
	return engine.Build(tag, context)
}
