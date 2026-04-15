package cmd

import (
	"errors"
	"fmt"

	"docksmith/store"
)

func HandleRMI(args []string) error {
	if len(args) != 1 {
		return errors.New("rmi requires exactly one image <name:tag>")
	}

	Remove(args[0])
	return nil
}

func Remove(image string) {
	removedLayers, err := store.DeleteImage(image)
	if err != nil {
		fmt.Println("Error removing image:", err)
		return
	}

	fmt.Printf("Removed image %s\n", image)
	if len(removedLayers) > 0 {
		fmt.Printf("Pruned %d unreferenced layer(s)\n", len(removedLayers))
	}
}
