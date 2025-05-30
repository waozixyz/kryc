// includes.go (Corrected Include Parsing Logic)
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode" // Need unicode for IsSpace
)

// readAndProcessIncludes recursively reads a file, processes @include directives, and returns the combined content.
func readAndProcessIncludes(filePath string, depth int, totalLinesProcessed *int, stateForErrors *CompilerState) (string, error) {
	if depth > MaxIncludeDepth {
		return "", fmt.Errorf("maximum include depth (%d) exceeded: processing '%s'", MaxIncludeDepth, filePath)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("cannot open include file '%s': %w", filePath, err)
	}
	defer file.Close()

	basePath := filepath.Dir(filePath)
	var resultBuffer bytes.Buffer
	scanner := bufio.NewScanner(file)
	lineInThisFile := 0

	log.Printf("DEBUG Include: Reading file: %s (Depth: %d)\n", filePath, depth)

	for scanner.Scan() {
		lineInThisFile++
		line := scanner.Text()
		originalLineForLog := line

		// 1. Trim leading whitespace first
		trimmedLeading := strings.TrimLeftFunc(line, unicode.IsSpace)

		// 2. Check for @include prefix
		if strings.HasPrefix(trimmedLeading, "@include") {
			// 3. Extract the part after "@include" and trim space
			includeDirectivePart := strings.TrimSpace(trimmedLeading[len("@include"):])

			// 4. Check if it starts and ends with quotes
			if len(includeDirectivePart) >= 2 && includeDirectivePart[0] == '"' {
				// Find the closing quote
				closingQuoteIndex := strings.Index(includeDirectivePart[1:], "\"")
				if closingQuoteIndex != -1 {
					// Extract path
					includePathRaw := includeDirectivePart[1 : 1+closingQuoteIndex]

					// Check if anything comes AFTER the closing quote (should be whitespace or comment)
					restOfLine := strings.TrimSpace(includeDirectivePart[1+closingQuoteIndex+1:])
					isComment := strings.HasPrefix(restOfLine, "#")

					if restOfLine == "" || isComment {
						// Valid Include Found!
						log.Printf("DEBUG Include (%s L%d): Parsed valid include. Path: '%s'\n", filepath.Base(filePath), lineInThisFile, includePathRaw)

						fullIncludePath := includePathRaw
						if !filepath.IsAbs(includePathRaw) && !(len(includePathRaw) > 1 && includePathRaw[1] == ':') {
							fullIncludePath = filepath.Join(basePath, includePathRaw)
						}
						fullIncludePath = filepath.Clean(fullIncludePath)

						log.Printf("DEBUG Include (%s L%d): Processing include for path: '%s' -> '%s'\n", filepath.Base(filePath), lineInThisFile, includePathRaw, fullIncludePath)

						includedContent, errInc := readAndProcessIncludes(fullIncludePath, depth+1, totalLinesProcessed, stateForErrors)
						if errInc != nil {
							return "", fmt.Errorf("error in included file '%s' (from %s L%d): %w", fullIncludePath, filepath.Base(filePath), lineInThisFile, errInc)
						}
						resultBuffer.WriteString(includedContent)
						if !strings.HasSuffix(includedContent, "\n") && len(includedContent) > 0 {
							resultBuffer.WriteString("\n")
						}
						continue // Skip writing the original @include line
					} else {
						log.Printf("Warning (%s L%d): Invalid @include syntax. Found extra characters after closing quote: '%s'. Line ignored.\n", filepath.Base(filePath), lineInThisFile, restOfLine)
					}
				} else {
					log.Printf("Warning (%s L%d): Invalid @include syntax. Missing closing quote. Line ignored.\n", filepath.Base(filePath), lineInThisFile)
				}
			} else {
				log.Printf("Warning (%s L%d): Invalid @include syntax. Path not enclosed in quotes. Line ignored.\n", filepath.Base(filePath), lineInThisFile)
			}
			// If any check above failed, we fall through and treat the line as normal content (effectively ignoring the faulty include)
		}

		// --- Process non-include lines (or lines that failed include parsing) ---
		// Write the original line to preserve comments and indentation for the parser
		resultBuffer.WriteString(originalLineForLog)
		resultBuffer.WriteString("\n")
		*totalLinesProcessed++

		if len(line) > MaxLineLength {
			log.Printf("Warning (%s L%d): Line exceeds MaxLineLength (%d).\n", filepath.Base(filePath), lineInThisFile, MaxLineLength)
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
