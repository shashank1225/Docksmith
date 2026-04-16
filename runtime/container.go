package runtime

import (
	"docksmith/store"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"syscall"
)

// RunOptions specifies options for running a container
type RunOptions struct {
	EnvOverrides map[string]string
	Command      []string
}

// RunContainer runs a container from an image
func RunContainer(image string, opts RunOptions) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("container runtime requires Linux")
	}

	bundleDir, rootFS, manifest, err := PrepareContainerFilesystem(image)
	if err != nil {
		return err
	}
	defer CleanupContainerFilesystem(bundleDir)

	env := store.EnvListToMap(manifest.Config.Env)
	for k, v := range opts.EnvOverrides {
		env[k] = v
	}

	command := manifest.Config.Cmd
	if len(opts.Command) > 0 {
		command = opts.Command
	}

	if len(command) == 0 {
		return fmt.Errorf("image %q has no configured command", image)
	}
	if manifest.Config.WorkingDir == "" {
		manifest.Config.WorkingDir = "/"
	}

	if err := executeInContainer(rootFS, manifest.Config.WorkingDir, command, env); err != nil {
		return fmt.Errorf("run image %q: %w", image, err)
	}

	return nil
}

func ExecuteShellInRootFS(rootFS string, workingDir string, env map[string]string, command string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("RUN requires Linux isolation")
	}

	cmdParts := []string{"/bin/sh", "-c", command}

	return executeInContainer(rootFS, workingDir, cmdParts, env)
}

// ExecuteInternal is called for internal container execution
func ExecuteInternal() error {
	rootFS := os.Getenv("DOCKSMITH_ROOTFS")
	if rootFS == "" {
		return fmt.Errorf("missing DOCKSMITH_ROOTFS")
	}

	workingDir := os.Getenv("DOCKSMITH_WORKDIR")
	if workingDir == "" {
		workingDir = "/"
	}

	rawCmd := os.Getenv("DOCKSMITH_CMD")
	if rawCmd == "" {
		return fmt.Errorf("missing DOCKSMITH_CMD")
	}

	var cmdParts []string
	if err := json.Unmarshal([]byte(rawCmd), &cmdParts); err != nil {
		return fmt.Errorf("decode DOCKSMITH_CMD: %w", err)
	}
	if len(cmdParts) == 0 {
		return fmt.Errorf("DOCKSMITH_CMD cannot be empty")
	}

	rawEnv := os.Getenv("DOCKSMITH_ENV")
	env := map[string]string{}
	if rawEnv != "" {
		if err := json.Unmarshal([]byte(rawEnv), &env); err != nil {
			return fmt.Errorf("decode DOCKSMITH_ENV: %w", err)
		}
	}

	if _, ok := env["PATH"]; !ok {
		env["PATH"] = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}

	if err := chrootRootFS(rootFS); err != nil {
		return err
	}

	if err := os.Chdir(workingDir); err != nil {
		return fmt.Errorf("change to working directory %q: %w", workingDir, err)
	}

	path, err := exec.LookPath(cmdParts[0])
	if err != nil {
		return fmt.Errorf("resolve command %q: %w", cmdParts[0], err)
	}

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	envList := make([]string, 0, len(keys))
	for _, key := range keys {
		envList = append(envList, key+"="+env[key])
	}

	if err := syscall.Exec(path, cmdParts, envList); err != nil {
		return fmt.Errorf("exec command %q: %w", cmdParts[0], err)
	}

	return nil
}

func executeInContainer(rootFS string, workingDir string, cmdParts []string, env map[string]string) error {
	rawCmd, err := json.Marshal(cmdParts)
	if err != nil {
		return fmt.Errorf("encode command: %w", err)
	}

	rawEnv, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encode environment: %w", err)
	}

	cmd := exec.Command(os.Args[0], "__docksmith_internal_exec")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(),
		"DOCKSMITH_ROOTFS="+rootFS,
		"DOCKSMITH_WORKDIR="+workingDir,
		"DOCKSMITH_CMD="+string(rawCmd),
		"DOCKSMITH_ENV="+string(rawEnv),
	)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("internal execution failed: %w", err)
	}

	return nil
}
