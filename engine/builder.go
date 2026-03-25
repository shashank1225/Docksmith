package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"docksmith/layers"
	"docksmith/store"
)

/*
BuildState stores the current build session information
*/
type BuildState struct {
	BaseImage     string
	WorkDir       string
	Env           map[string]string
	Cmd           []string
	Layers        []string
	PreviousLayer string
}

/*
Main entry point called from cmd/build.go
*/
func BuildImage(tag string, contextPath string) error {

	fmt.Println("Starting build:", tag)

	docksmithfilePath := filepath.Join(contextPath, "Docksmithfile")

	instructions, err := ParseDocksmithfile(docksmithfilePath)
	if err != nil {
		return err
	}

	state := BuildState{
		Env:    make(map[string]string),
		Layers: []string{},
	}

	for _, inst := range instructions {

		err := executeInstruction(inst, &state, contextPath)
		if err != nil {
			return err
		}
	}

	err = saveImageManifest(tag, state)
	if err != nil {
		return err
	}

	fmt.Println("Build successful:", tag)

	return nil
}

/*
Handles execution of each instruction
*/
func executeInstruction(inst Instruction, state *BuildState, context string) error {

	fmt.Println("Executing:", inst.Raw)

	switch inst.Type {

	case "FROM":

		state.BaseImage = inst.Args[0]

	case "WORKDIR":

		state.WorkDir = inst.Args[0]

	case "ENV":

		key, value := parseEnv(inst.Args[0])
		state.Env[key] = value

	case "CMD":

		state.Cmd = inst.Args

	case "COPY":

		layerDigest, err := layers.CreateCopyLayer(inst.Args, context, state.WorkDir)
		if err != nil {
			return err
		}

		state.Layers = append(state.Layers, layerDigest)
		state.PreviousLayer = layerDigest

	case "RUN":

		layerDigest, err := layers.CreateRunLayer(inst.Args, state.WorkDir, state.Env)
		if err != nil {
			return err
		}

		state.Layers = append(state.Layers, layerDigest)
		state.PreviousLayer = layerDigest

	default:

		return fmt.Errorf("unknown instruction: %s", inst.Type)
	}

	return nil
}

/*
Parses ENV KEY=value
*/
func parseEnv(input string) (string, string) {

	for i := range input {

		if input[i] == '=' {

			return input[:i], input[i+1:]
		}
	}

	return "", ""
}

/*
Creates image manifest JSON
Saved into ~/.docksmith/images/
*/
func saveImageManifest(tag string, state BuildState) error {

	parts := strings.Split(tag, ":")

	if len(parts) != 2 {
		return fmt.Errorf("invalid tag format")
	}

	imageName := parts[0]
	imageTag := parts[1]

	manifest := map[string]interface{}{
		"name":   imageName,
		"tag":    imageTag,
		"layers": state.Layers,
		"config": map[string]interface{}{
			"Env":        state.Env,
			"Cmd":        state.Cmd,
			"WorkingDir": state.WorkDir,
			"BaseImage":  state.BaseImage,
		},
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	imageDir := filepath.Join(homeDir, ".docksmith", "images")

	err = os.MkdirAll(imageDir, os.ModePerm)
	if err != nil {
		return err
	}

	filePath := filepath.Join(imageDir, imageName+"_"+imageTag+".json")

	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	err = encoder.Encode(manifest)
	if err != nil {
		return err
	}

	return store.RegisterImage(imageName, imageTag, state.Layers)
}