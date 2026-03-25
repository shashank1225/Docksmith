package main

import (
	"fmt"
	"os"

	"docksmith/cmd"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "build":
		if err := cmd.HandleBuild(args); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			printBuildUsage()
			os.Exit(1)
		}
	case "run":
		if err := cmd.HandleRun(args); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			printRunUsage()
			os.Exit(1)
		}
	case "images":
		if err := cmd.HandleImages(args); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			printImagesUsage()
			os.Exit(1)
		}
	case "rmi":
		if err := cmd.HandleRMI(args); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			printRMIUsage()
			os.Exit(1)
		}
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command %q\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("docksmith - a lightweight container CLI")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  docksmith build -t <name:tag> <context>")
	fmt.Println("  docksmith run <name:tag>")
	fmt.Println("  docksmith images")
	fmt.Println("  docksmith rmi <name:tag>")
	fmt.Println()
	fmt.Println("Use 'docksmith help' to show this message.")
}

func printBuildUsage() {
	fmt.Println("Usage: docksmith build -t <name:tag> <context>")
}

func printRunUsage() {
	fmt.Println("Usage: docksmith run <name:tag>")
}

func printImagesUsage() {
	fmt.Println("Usage: docksmith images")
}

func printRMIUsage() {
	fmt.Println("Usage: docksmith rmi <name:tag>")
}
