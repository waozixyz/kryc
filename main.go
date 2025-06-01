// main.go
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
	log.SetFlags(0) // Remove timestamp prefixes

	// --- Argument Handling ---
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <input.kry> <output.krb>\n", filepath.Base(os.Args[0]))
		os.Exit(1)
	}
	inputFile := os.Args[1]
	outputFile := os.Args[2]

	// --- State Initialization ---
	state := CompilerState{
		Elements:      make([]Element, 0, 64),
		Strings:       make([]StringEntry, 0, 128),
		Styles:        make([]StyleEntry, 0, 32),
		Resources:     make([]ResourceEntry, 0, 16),
		ComponentDefs: make([]ComponentDefinition, 0, 16),
		Variables:     make(map[string]VariableDef), // Initialize Variables map
	}

	log.Printf("Compiling '%s' to '%s' (KRB v%d.%d)...\n", inputFile, outputFile, KRBVersionMajor, KRBVersionMinor)

	// --- Pass 0.1: Process Includes ---
	log.Println("Pass 0.1: Processing includes...")
	sourceAfterIncludes, totalLines, err := preprocessIncludes(inputFile)
	if err != nil {
		log.Fatalf("Failed: Preprocessing Includes - %v\n", err)
	}
	log.Printf("   Preprocessed includes: approx %d lines.\n", totalLines)

	// --- Pass 0.2: Process Variables ---
	log.Println("Pass 0.2: Processing variables...")
	sourceAfterVariables, err := state.ProcessAndSubstituteVariables(sourceAfterIncludes)
	if err != nil {
		log.Fatalf("Failed: Processing Variables - %v\n", err)
	}
	// For debugging source after variable substitution:
	// fmt.Printf("--- Source After Variables ---\n%s\n--------------------------\n", sourceAfterVariables)

	// --- Pass 1: Parse Source ---
	log.Println("Pass 1: Parsing source...")
	state.CurrentFilePath = inputFile // Set context for parser errors
	if err := state.parseKrySource(sourceAfterVariables); err != nil {
		log.Fatalf("Failed: Parsing - %v\n", err)
	}
	log.Printf("   Parsed %d items, %d styles, %d strings, %d res, %d defs.\n",
		len(state.Elements), len(state.Styles), len(state.Strings), len(state.Resources), len(state.ComponentDefs))

	// --- Pass 1.2: Resolve Style Inheritance ---
	log.Println("Pass 1.2: Resolving style inheritance...")
	if err := state.resolveStyleInheritance(); err != nil {
		log.Fatalf("Failed: Style Resolution - %v\n", err)
	}

	// --- Pass 1.5: Resolve Components and Element Properties ---
	log.Println("Pass 1.5: Expanding components and resolving element properties...")
	if err := state.resolveComponentsAndProperties(); err != nil {
		log.Fatalf("Failed: Expansion/Resolution - %v\n", err)
	}

	// --- Pass 2: Calculate Offsets and Final Sizes ---
	log.Println("Pass 2: Calculating final offsets and sizes...")
	if err := state.calculateOffsetsAndSizes(); err != nil {
		log.Fatalf("Failed: Offset Calculation - %v\n", err)
	}

	// --- Pass 3: Write Binary KRB File ---
	log.Println("Pass 3: Writing KRB binary...")
	if err := state.writeKrbFile(outputFile); err != nil {
		log.Printf("Failed: Writing Binary - %v\n", err)
		_ = os.Remove(outputFile)
		os.Exit(1)
	}

	// --- Success Message ---
	info, statErr := os.Stat(outputFile)
	finalSize := int64(-1)
	if statErr == nil {
		finalSize = info.Size()
	} else {
		log.Printf("Warning: Could not stat output file '%s' after writing: %v\n", outputFile, statErr)
	}

	log.Printf("Success. Output size: %d bytes.\n", finalSize)
}
