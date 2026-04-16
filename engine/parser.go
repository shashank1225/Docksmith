package engine

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Instruction struct {
	Op   string
	Args []string
	Raw  string
	Line int
}

type BuildSpec struct {
	Instructions []Instruction
}

func ParseBuildFile(contextDir string) (*BuildSpec, error) {
	buildFile, err := resolveBuildFile(contextDir)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(buildFile)
	if err != nil {
		return nil, fmt.Errorf("open build file %q: %w", buildFile, err)
	}
	defer file.Close()

	instructions := make([]Instruction, 0)
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		inst, err := parseInstructionLine(line, lineNo)
		if err != nil {
			return nil, err
		}
		instructions = append(instructions, inst)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read build file %q: %w", buildFile, err)
	}

	if len(instructions) == 0 {
		return nil, fmt.Errorf("build file %q has no instructions", buildFile)
	}

	return &BuildSpec{Instructions: instructions}, nil
}

func resolveBuildFile(contextDir string) (string, error) {
	path := filepath.Join(contextDir, "Docksmithfile")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path, nil
	}

	return "", fmt.Errorf("no Docksmithfile found in context %q", contextDir)
}

func parseInstructionLine(line string, lineNo int) (Instruction, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return Instruction{}, fmt.Errorf("line %d: empty instruction", lineNo)
	}

	op := strings.ToUpper(fields[0])
	rest := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))

	switch op {
	case "FROM", "WORKDIR":
		if strings.TrimSpace(rest) == "" {
			return Instruction{}, fmt.Errorf("line %d: %s requires one argument", lineNo, op)
		}
		return Instruction{Op: op, Args: []string{strings.TrimSpace(rest)}, Raw: line, Line: lineNo}, nil
	case "COPY":
		if len(fields) != 3 {
			return Instruction{}, fmt.Errorf("line %d: COPY requires <src> <dest>", lineNo)
		}
		return Instruction{Op: op, Args: []string{fields[1], fields[2]}, Raw: line, Line: lineNo}, nil
	case "RUN", "CMD", "ENV":
		if strings.TrimSpace(rest) == "" {
			return Instruction{}, fmt.Errorf("line %d: %s requires arguments", lineNo, op)
		}
		return Instruction{Op: op, Args: []string{strings.TrimSpace(rest)}, Raw: line, Line: lineNo}, nil
	default:
		return Instruction{}, fmt.Errorf("line %d: unsupported instruction %q", lineNo, op)
	}
}
