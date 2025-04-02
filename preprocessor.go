package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// readAndProcessIncludes recursively reads a file, processes @include directives, and returns the combined content.
func readAndProcessIncludes(filePath string, depth int, totalLinesProcessed *int, stateForErrors *CompilerState) (string, error) {
	if depth > MaxIncludeDepth {
		return "", fmt.Errorf("maximum include depth (%d) exceeded: processing '%s'", MaxIncludeDepth, filePath)
	}

	file, err := os.Open(filePath)
	if err != nil {
		// Try to provide context line if possible (difficult here, maybe pass calling line?)
		return "", fmt.Errorf("cannot open include file '%s': %w", filePath, err)
	}
	defer file.Close()

	basePath := filepath.Dir(filePath)
	var resultBuffer bytes.Buffer
	scanner := bufio.NewScanner(file)
	lineInThisFile := 0

	for scanner.Scan() {
		lineInThisFile++
		line := scanner.Text()

		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "@include ") {
			parts := strings.SplitN(trimmed, "\"", 3)
			if len(parts) == 3 && strings.TrimSpace(parts[2]) == "" {
				includePathRaw := parts[1]
				fullIncludePath := includePathRaw

				// Handle relative paths
				if !filepath.IsAbs(includePathRaw) && !(len(includePathRaw) > 1 && includePathRaw[1] == ':') { // Basic check for windows drive letter
					fullIncludePath = filepath.Join(basePath, includePathRaw)
				}

				// Normalize the path
				fullIncludePath = filepath.Clean(fullIncludePath)

				includedContent, err := readAndProcessIncludes(fullIncludePath, depth+1, totalLinesProcessed, stateForErrors)
				if err != nil {
					// Add context to the error
					return "", fmt.Errorf("error in included file '%s' (from %s L%d): %w", fullIncludePath, filePath, lineInThisFile, err)
				}
				resultBuffer.WriteString(includedContent) // Append content
				// Ensure newline if included content didn't end with one (scanner strips it)
				if !strings.HasSuffix(includedContent, "\n") && len(includedContent) > 0 {
					resultBuffer.WriteString("\n")
				}

			} else {
				log.Printf("Warning (%s L%d): Invalid @include syntax. Use '@include \"path\"'.\n", filePath, lineInThisFile)
				// Optionally include the invalid line itself: resultBuffer.WriteString(line + "\n")
			}
		} else {
			resultBuffer.WriteString(line) // Write the original line
			resultBuffer.WriteString("\n") // Add back the newline stripped by scanner
			*totalLinesProcessed++         // Count non-include lines
		}
		// Basic check for line length during read
		if len(line) > MaxLineLength {
			log.Printf("Warning (%s L%d): Line exceeds MaxLineLength (%d).\n", filePath, lineInThisFile, MaxLineLength)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading file '%s': %w", filePath, err)
	}

	return resultBuffer.String(), nil
}

// preprocessIncludes is the entry point for include processing.
func preprocessIncludes(mainFilePath string) (string, int, error) {
	totalLines := 0
	// Pass dummy state, real state not needed until parsing
	dummyState := &CompilerState{}
	content, err := readAndProcessIncludes(mainFilePath, 0, &totalLines, dummyState)
	if err != nil {
		return "", 0, err
	}
	return content, totalLines, nil
}
