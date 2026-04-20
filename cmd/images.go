package cmd

import (
	"errors"
	"fmt"
	"strings"

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

	fmt.Printf("%-24s %-10s %-14s %s\n", "NAME", "TAG", "IMAGE ID", "CREATED")
	for _, img := range images {
		id := strings.TrimPrefix(img.Digest, "sha256:")
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Printf("%-24s %-10s %-14s %s\n", img.Name, img.Tag, id, img.Created)
	}
}
