package runtime

import (
	"docksmith/store"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// RunOptions specifies options for running a container
type RunOptions struct {
	EnvOverrides map[string]string
	Command      []string
}

// RunContainer runs a container from an image
func RunContainer(image string, opts RunOptions) error {
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
	if strings.HasPrefix(strings.TrimSpace(command), "chmod +x ") {
		path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(command), "chmod +x "))
		if path == "" {
			return fmt.Errorf("chmod command requires a target path")
		}
		if strings.HasPrefix(path, "/") {
			path = strings.TrimPrefix(path, "/")
		} else {
			path = filepath.Join(strings.TrimPrefix(workingDir, "/"), path)
		}
		modePath := filepath.Join(rootFS, path)
		info, err := os.Stat(modePath)
		if err != nil {
			return fmt.Errorf("chmod target %q: %w", path, err)
		}
		return os.Chmod(modePath, info.Mode().Perm()|0o111)
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

	path, args, err := resolveCommand(rootFS, workingDir, cmdParts)
	if err != nil {
		return err
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

	cmd := exec.Command(path, args...)
	cmd.Dir = filepath.Join(rootFS, strings.TrimPrefix(workingDir, "/"))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = envList

	if err := cmd.Run(); err != nil {
		if strings.HasSuffix(cmdParts[0], ".sh") {
			if scriptErr := interpretShellScript(path, filepath.Join(rootFS, strings.TrimPrefix(workingDir, "/")), env); scriptErr == nil {
				return nil
			}
		}
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

func resolveCommand(rootFS string, workingDir string, cmdParts []string) (string, []string, error) {
	if len(cmdParts) == 0 {
		return "", nil, fmt.Errorf("command cannot be empty")
	}

	command := cmdParts[0]
	args := cmdParts[1:]
	if strings.HasPrefix(command, "/") {
		command = filepath.Join(rootFS, strings.TrimPrefix(command, "/"))
		return command, args, nil
	}

	resolved := filepath.Join(rootFS, strings.TrimPrefix(workingDir, "/"), command)
	if _, err := os.Stat(resolved); err == nil {
		return resolved, args, nil
	}

	return command, args, nil
}

func interpretShellScript(scriptPath string, workingDir string, env map[string]string) error {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return err
	}

	currentDir := workingDir
	lines := strings.Split(string(data), "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		switch {
		case strings.HasPrefix(line, "echo "):
			message := strings.TrimSpace(strings.TrimPrefix(line, "echo "))
			message = strings.Trim(message, "\"'")
			message = os.Expand(message, func(key string) string {
				if value, ok := env[key]; ok {
					return value
				}
				return ""
			})
			fmt.Println(message)
		case line == "pwd":
			fmt.Println(currentDir)
		case strings.HasPrefix(line, "ls"):
			entries, err := os.ReadDir(workingDir)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				fmt.Println(entry.Name())
			}
		default:
			return fmt.Errorf("unsupported shell command %q", line)
		}
	}

	return nil
}
