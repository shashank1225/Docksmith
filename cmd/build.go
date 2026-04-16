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
	var noCache bool
	fs.StringVar(&tag, "t", "", "image name and tag")
	fs.BoolVar(&noCache, "no-cache", false, "disable build cache")

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

	return Build(tag, remaining[0], noCache)
}

func Build(tag string, context string, noCache bool) error {
	return engine.Build(tag, context, engine.BuildOptions{NoCache: noCache})
}
