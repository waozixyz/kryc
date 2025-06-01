// variables.go
package main

import (
	"bufio"
	"fmt"
	"log"
	"regexp"
	"strings"
	"unicode"
)

var varUsageRegex = regexp.MustCompile(`\$([a-zA-Z_][a-zA-Z0-9_]*)`)

// ProcessAndSubstituteVariables is the main entry point for the variable processing pass.
// It collects, resolves, and substitutes variables, then removes @variables blocks.
func (state *CompilerState) ProcessAndSubstituteVariables(source string) (string, error) {
	state.Variables = make(map[string]VariableDef)

	if err := state.collectRawVariables(source); err != nil {
		return source, fmt.Errorf("error collecting variables: %w", err)
	}

	if err := state.resolveAllVariables(); err != nil {
		return source, fmt.Errorf("error resolving variables: %w", err)
	}

	substitutedSource, err := state.performSubstitutionAndRemoveBlocks(source)
	if err != nil {
		return source, fmt.Errorf("error substituting variables: %w", err)
	}
	return substitutedSource, nil
}

// collectRawVariables scans the source for @variables blocks and populates state.Variables.
// Handles redefinition (later wins, with a warning).
func (state *CompilerState) collectRawVariables(source string) error {
	scanner := bufio.NewScanner(strings.NewReader(source))
	inVariablesBlock := false
	currentLineNum := 0

	for scanner.Scan() {
		currentLineNum++
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)

		if strings.HasPrefix(trimmedLine, "#") { // Skip full-line comments
			continue
		}

		// Strip trailing comments from the line
		commentIndex := -1
		inQuotes := false
		for i, r := range trimmedLine {
			if r == '"' {
				inQuotes = !inQuotes
			}
			if r == '#' && !inQuotes {
				commentIndex = i
				break
			}
		}
		if commentIndex != -1 {
			trimmedLine = strings.TrimSpace(trimmedLine[:commentIndex])
		}

		if trimmedLine == "@variables {" {
			if inVariablesBlock {
				return fmt.Errorf("L%d: nested @variables blocks are not allowed", currentLineNum)
			}
			inVariablesBlock = true
			continue
		}

		if trimmedLine == "}" {
			if inVariablesBlock {
				inVariablesBlock = false
				continue
			}
			// If not inVariablesBlock, this '}' is for other KRY syntax, ignore here.
		}

		if inVariablesBlock {
			if trimmedLine == "" {
				continue
			}
			parts := strings.SplitN(trimmedLine, ":", 2)
			if len(parts) != 2 {
				return fmt.Errorf("L%d: invalid variable definition syntax in @variables block: '%s'. Expected 'name: value'", currentLineNum, trimmedLine)
			}
			varName := strings.TrimSpace(parts[0])
			rawValue := strings.TrimSpace(parts[1])

			if !isValidIdentifier(varName) {
				return fmt.Errorf("L%d: invalid variable name '%s'", currentLineNum, varName)
			}

			if existing, exists := state.Variables[varName]; exists {
				log.Printf("L%d: Warn: Variable '%s' redefined. Previous definition at L%d.", currentLineNum, varName, existing.DefLine)
			}
			state.Variables[varName] = VariableDef{
				RawValue: rawValue,
				DefLine:  currentLineNum,
			}
		}
	}
	return scanner.Err()
}

// resolveAllVariables resolves inter-variable dependencies and detects cycles.
// Updates VariableDef.Value with the final literal string.
func (state *CompilerState) resolveAllVariables() error {
	for name := range state.Variables {
		if !state.Variables[name].IsResolved {
			_, err := state.resolveVariable(name, make(map[string]struct{}))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveVariable recursively resolves a single variable.
func (state *CompilerState) resolveVariable(name string, visited map[string]struct{}) (string, error) {
	varDef, exists := state.Variables[name]
	if !exists {
		// This error should be caught later during substitution if a $var is used but not defined.
		// Here, it implies an internal issue if resolveVariable is called for a non-existent key.
		return "", fmt.Errorf("internal error: trying to resolve undefined variable '%s'", name)
	}

	if varDef.IsResolved {
		return varDef.Value, nil
	}
	if varDef.IsResolving { // Also check visited for path-specific cycle
		return "", fmt.Errorf("L%d: cyclic variable definition detected for '%s'", varDef.DefLine, name)
	}
	if _, alreadyVisited := visited[name]; alreadyVisited {
		return "", fmt.Errorf("L%d: cyclic variable definition detected involving '%s' (path: %v)", varDef.DefLine, name, getPath(visited, name))
	}

	varDef.IsResolving = true
	visited[name] = struct{}{}
	state.Variables[name] = varDef // Update state

	// Recursively resolve references in RawValue
	currentValue := varDef.RawValue
	matches := varUsageRegex.FindAllStringSubmatch(currentValue, -1)
	for _, match := range matches {
		refVarName := match[1]
		resolvedRefValue, err := state.resolveVariable(refVarName, visited)
		if err != nil {
			// Prepend current variable's context to the error
			return "", fmt.Errorf("L%d: in variable '%s': %w", varDef.DefLine, name, err)
		}
		// Substitute resolved value textually
		currentValue = strings.ReplaceAll(currentValue, "$"+refVarName, resolvedRefValue)
	}

	varDef.Value = currentValue
	varDef.IsResolved = true
	varDef.IsResolving = false
	delete(visited, name) // Backtrack
	state.Variables[name] = varDef

	return varDef.Value, nil
}

// performSubstitutionAndRemoveBlocks removes @variables blocks and substitutes $varName elsewhere.
func (state *CompilerState) performSubstitutionAndRemoveBlocks(source string) (string, error) {
	var result strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(source))
	inVariablesBlock := false
	currentLineNum := 0
	var substitutionErrors []string

	for scanner.Scan() {
		currentLineNum++
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)

		if strings.HasPrefix(trimmedLine, "@variables {") {
			inVariablesBlock = true
			continue // Remove this line
		}
		if inVariablesBlock && trimmedLine == "}" {
			inVariablesBlock = false
			continue // Remove this line
		}
		if inVariablesBlock {
			continue // Remove lines inside @variables block
		}

		// Perform substitution for lines not in @variables block
		substitutedLine := varUsageRegex.ReplaceAllStringFunc(line, func(match string) string {
			varName := match[1:] // Remove leading '$'
			varDef, exists := state.Variables[varName]
			if !exists {
				errStr := fmt.Sprintf("L%d: undefined variable '$%s' used", currentLineNum, varName)
				substitutionErrors = append(substitutionErrors, errStr)
				return match // Return original if undefined, error will be reported
			}
			if !varDef.IsResolved { // Should not happen if resolveAllVariables was successful
				errStr := fmt.Sprintf("L%d: internal error: variable '$%s' used but not resolved", currentLineNum, varName)
				substitutionErrors = append(substitutionErrors, errStr)
				return match
			}
			return varDef.Value
		})
		result.WriteString(substitutedLine)
		result.WriteString("\n")
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}
	if len(substitutionErrors) > 0 {
		return "", fmt.Errorf(strings.Join(substitutionErrors, "\n"))
	}

	return result.String(), nil
}

func isValidIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
		} else {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				return false
			}
		}
	}
	return true
}

func getPath(visited map[string]struct{}, current string) []string {
	var path []string
	for v := range visited {
		path = append(path, v)
	}
	path = append(path, current+" (cycle)")
	return path
}
