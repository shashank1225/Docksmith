package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
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

	Build(tag, remaining[0])
	return nil
}

func Build(tag string, context string) {
	fmt.Println("Step 1/3 : FROM base")
	fmt.Println("Step 2/3 : COPY . /app")
	fmt.Println("Step 3/3 : RUN build")
	fmt.Printf("BUILD called with tag=%s, context=%s\n", tag, context)
}
