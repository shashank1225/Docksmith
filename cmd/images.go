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

	fmt.Printf("%-24s %-8s %s\n", "REPOSITORY", "TAG", "LAYERS")
	for _, img := range images {
		fmt.Printf("%-24s %-8s %d\n", img.Name, img.Tag, len(img.Layers))
	}
}
