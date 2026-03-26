package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const internalExecSpecEnv = "DOCKSMITH_INTERNAL_EXEC_SPEC"

type RunOptions struct {
	EnvOverrides map[string]string
}

type internalExecSpec struct {
	RootFS  string            `json:"rootfs"`
	WorkDir string            `json:"workDir"`
	Cmd     []string          `json:"cmd"`
	Env     map[string]string `json:"env"`
}

func RunContainer(image string, opts RunOptions) error {
	bundleDir, rootFS, manifest, err := PrepareContainerFilesystem(image)
	if err != nil {
		return err
	}
	defer func() {
		_ = CleanupContainerFilesystem(bundleDir)
	}()

	if len(manifest.Config.Cmd) == 0 {
		return errors.New("image has no CMD configured")
	}

	containerEnv := mergeEnvironment(manifest.Config.Env, opts.EnvOverrides)
	workDir := manifest.Config.WorkingDir
	if strings.TrimSpace(workDir) == "" {
		workDir = "/"
	}

	spec := internalExecSpec{
		RootFS:  rootFS,
		WorkDir: workDir,
		Cmd:     manifest.Config.Cmd,
		Env:     containerEnv,
	}

	return runInternalExec(spec)
}

func ExecuteInternal() error {
	raw := os.Getenv(internalExecSpecEnv)
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("missing %s", internalExecSpecEnv)
	}

	var spec internalExecSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return fmt.Errorf("decode internal exec spec: %w", err)
	}

	if len(spec.Cmd) == 0 {
		return errors.New("internal exec command is empty")
	}

	if err := ApplyIsolation(spec.RootFS); err != nil {
		return err
	}

	if err := os.Chdir(spec.WorkDir); err != nil {
		return fmt.Errorf("set working directory %q: %w", spec.WorkDir, err)
	}

	command := exec.Command(spec.Cmd[0], spec.Cmd[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = flattenEnvMap(spec.Env)

	if err := command.Run(); err != nil {
		return fmt.Errorf("execute command: %w", err)
	}

	return nil
}

func runInternalExec(spec internalExecSpec) error {
	encodedSpec, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("encode internal exec spec: %w", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve docksmith executable path: %w", err)
	}

	child := exec.Command(exePath, "__docksmith_internal_exec")
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.Env = append(os.Environ(), internalExecSpecEnv+"="+string(encodedSpec))

	if err := child.Run(); err != nil {
		return fmt.Errorf("run container: %w", err)
	}

	return nil
}

func mergeEnvironment(imageEnv map[string]string, overrides map[string]string) map[string]string {
	merged := envSliceToMap(os.Environ())

	for key, value := range imageEnv {
		merged[key] = value
	}

	for key, value := range overrides {
		merged[key] = value
	}

	return merged
}

func envSliceToMap(values []string) map[string]string {
	env := make(map[string]string, len(values))

	for _, item := range values {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		env[parts[0]] = parts[1]
	}

	return env
}

func flattenEnvMap(values map[string]string) []string {
	flat := make([]string, 0, len(values))

	for key, value := range values {
		flat = append(flat, key+"="+value)
	}

	return flat
}
