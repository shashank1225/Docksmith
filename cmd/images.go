package cmd

import (
	"errors"
	"fmt"

	"docksmith/store"
)

func HandleImages(args []string) error {
	if len(args) != 0 {
		return errors.New("images does not accept arguments")
	}

	Images()
	return nil
}

func Images() {
	images, err := store.ListImages()
	if err != nil {
		fmt.Println("Error listing images:", err)
		return
	}

	if len(images) == 0 {
		fmt.Println("No images found")
		return
	}

	fmt.Printf("%-24s %-10s %-14s %s\n", "NAME", "TAG", "ID", "CREATED")
	for _, img := range images {
		id := img.Digest
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Printf("%-24s %-10s %-14s %s\n", img.Name, img.Tag, id, img.Created)
	}
}
