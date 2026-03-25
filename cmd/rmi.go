package cmd

import (
	"errors"
	"fmt"
)

func HandleRMI(args []string) error {
	if len(args) != 1 {
		return errors.New("rmi requires exactly one image <name:tag>")
	}

	Remove(args[0])
	return nil
}

func Remove(image string) {
	fmt.Printf("RMI called with image=%s\n", image)
}
