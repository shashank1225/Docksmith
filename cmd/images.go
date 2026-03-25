package cmd

import (
	"errors"
	"fmt"
)

func HandleImages(args []string) error {
	if len(args) != 0 {
		return errors.New("images does not accept arguments")
	}

	Images()
	return nil
}

func Images() {
	fmt.Println("IMAGES called")
}
