package cmd

import (
	"errors"
	"fmt"
)

func HandleRun(args []string) error {
	if len(args) != 1 {
		return errors.New("run requires exactly one image <name:tag>")
	}

	Run(args[0])
	return nil
}

func Run(image string) {
	fmt.Printf("RUN called with image=%s\n", image)
}
