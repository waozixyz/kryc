package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// --- Main Function ---
func main() {
	// Use log package for consistent output formatting
	log.SetFlags(0) // Remove timestamp prefixes if desired

	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <input.kry> <output.krb>\n", filepath.Base(os.Args[0]))
		os.Exit(1)
	}
	inputFile := os.Args[1]
	outputFile := os.Args[2]

	state := CompilerState{
		// Initialize slices with some capacity to potentially reduce reallocations
		Elements:      make([]Element, 0, 64),
		Strings:       make([]StringEntry, 0, 128),
		Styles:        make([]StyleEntry, 0, 32),
		Resources:     make([]ResourceEntry, 0, 16),
		ComponentDefs: make([]ComponentDefinition, 0, 16),
	}

	log.Printf("Compiling '%s' to '%s' (KRB v%d.%d)...\n", inputFile, outputFile, KRBVersionMajor, KRBVersionMinor)

	// Pass 0: Includes
	log.Println("Pass 0: Processing includes...")
	sourceBuffer, totalLines, err := preprocessIncludes(inputFile)
	if err != nil {
		log.Fatalf("Failed: Preprocessing - %v\n", err)
	}
	log.Printf("   Preprocessed source: approx %d lines.\n", totalLines)
	// fmt.Printf("--- Preprocessed Source ---\n%s\n--------------------------\n", sourceBuffer) // Debug

	// Pass 1: Parse
	log.Println("Pass 1: Parsing source...")
	state.CurrentFilePath = inputFile // For error context
	if err := state.parseKrySource(sourceBuffer); err != nil {
		log.Fatalf("Failed: Parsing - %v\n", err)
	}
	log.Printf("   Parsed %d items, %d styles, %d strings, %d res, %d defs.\n",
		len(state.Elements), len(state.Styles), len(state.Strings), len(state.Resources), len(state.ComponentDefs))

	// Pass 1.5: Resolve/Expand
	if err := state.resolveComponentsAndProperties(); err != nil {
		log.Fatalf("Failed: Expansion/Resolution - %v\n", err)
	}

	// Pass 1.7: Layout Adjust (No-op)
	if err := state.adjustLayoutForPosition(); err != nil {
		log.Fatalf("Failed: Layout Adjustment - %v\n", err)
	}

	// Pass 2: Calculate Offsets
	if err := state.calculateOffsetsAndSizes(); err != nil {
		log.Fatalf("Failed: Offset Calculation - %v\n", err)
	}

	// Pass 3: Write Binary
	if err := state.writeKrbFile(outputFile); err != nil {
		log.Printf("Failed: Writing Binary - %v\n", err)
		// Attempt to remove partially written file
		_ = os.Remove(outputFile)
		os.Exit(1)
	}

	// Get final size for confirmation message
	info, err := os.Stat(outputFile)
	finalSize := int64(-1)
	if err == nil {
		finalSize = info.Size()
	}

	log.Printf("Success. Output size: %d bytes.\n", finalSize)
}
