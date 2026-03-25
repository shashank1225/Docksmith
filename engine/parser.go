package engine

import (
	"bufio"
	"os"
	"strings"
)

type Instruction struct {
	Type string
	Args []string
	Raw  string
}

func ParseDocksmithfile(path string) ([]Instruction, error) {

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var instructions []Instruction

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {

		line := strings.TrimSpace(scanner.Text())

		// skip empty lines
		if line == "" {
			continue
		}

		// skip comments
		if strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Fields(line)

		inst := Instruction{
			Type: strings.ToUpper(parts[0]),
			Args: parts[1:],
			Raw:  line,
		}

		instructions = append(instructions, inst)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return instructions, nil
}
