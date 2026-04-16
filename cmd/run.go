package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	dockruntime "docksmith/runtime"
)

func HandleRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var envFlags multiEnvFlag
	fs.Var(&envFlags, "e", "override environment variable (KEY=value)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) < 1 {
		return errors.New("run requires at least one image <name:tag>")
	}

	image := remaining[0]
	command := []string{}
	if len(remaining) > 1 {
		command = remaining[1:]
	}

	return Run(image, envFlags.toMap(), command)
}

func Run(image string, envOverrides map[string]string, command []string) error {
	if err := dockruntime.RunContainer(image, dockruntime.RunOptions{EnvOverrides: envOverrides, Command: command}); err != nil {
		return fmt.Errorf("run image %q: %w", image, err)
	}

	return nil
}

type multiEnvFlag []string

func (m *multiEnvFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiEnvFlag) Set(value string) error {
	if !strings.Contains(value, "=") {
		return errors.New("environment override must be in KEY=value format")
	}

	parts := strings.SplitN(value, "=", 2)
	if strings.TrimSpace(parts[0]) == "" {
		return errors.New("environment override key cannot be empty")
	}

	*m = append(*m, value)
	return nil
}

func (m multiEnvFlag) toMap() map[string]string {
	overrides := make(map[string]string, len(m))

	for _, item := range m {
		parts := strings.SplitN(item, "=", 2)
		overrides[parts[0]] = parts[1]
	}

	return overrides
}
