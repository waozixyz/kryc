// parser.go
package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"strings"
	"unicode"
)




// parseKrySource parses the preprocessed KRY source string.
func (state *CompilerState) parseKrySource(sourceBuffer string) error {
	scanner := bufio.NewScanner(strings.NewReader(sourceBuffer))
	currentLineNum := 0
	blockStack := make([]BlockStackEntry, 0, MaxBlockDepth) // Stack for tracking nested blocks

	// Helper to get current context safely
	getCurrentContext := func() (indent int, context interface{}, ctxType BlockContextType) {
		if len(blockStack) > 0 {
			top := blockStack[len(blockStack)-1]
			return top.Indent, top.Context, top.Type
		}
		return -1, nil, CtxNone // Base level
	}

	for scanner.Scan() {
		currentLineNum++
		state.CurrentLineNum = currentLineNum
		line := scanner.Text()

		// --- Prepare Line ---
		originalLine := line // Keep original for potential error messages if needed
		trimmed := line
		indent := 0
		for _, r := range trimmed { if r == ' ' { indent++ } else if r == '\t' { indent += 4 } else { break } }
		trimmed = strings.TrimLeftFunc(trimmed, unicode.IsSpace) // Remove leading whitespace only for processing

		// Handle comments starting at the beginning of the effective line
		if strings.HasPrefix(trimmed, "#") {
			trimmed = "" // Treat the whole line as a comment if # is first non-space char
		}
		// Trim trailing whitespace *after* checking for full-line comment
		trimmed = strings.TrimRightFunc(trimmed, unicode.IsSpace)

		// Skip empty lines (or lines that became empty after comment removal)
		if trimmed == "" {
			continue
		}

		// Basic line length check
		if len(originalLine) > MaxLineLength {
			log.Printf("L%d: Warning: Line exceeds MaxLineLength (%d)\n", currentLineNum, MaxLineLength)
		}

		currentIndent, currentContext, currentCtxType := getCurrentContext()

		// --- 1. Handle Block End ---
		if trimmed == "}" {
			if len(blockStack) == 0 {
				return fmt.Errorf("L%d: mismatched '}'", currentLineNum)
			}
			blockStack = blockStack[:len(blockStack)-1] // Pop the stack
			continue
		}

		// --- 2. Check for Block Start ---
		// Check if '{' is the last non-whitespace character on the line
		blockOpened := strings.HasSuffix(trimmed, "{")
		blockContent := trimmed // Content before potential '{'
		if blockOpened {
			// Find the '{' and take content before it, trimming space
			braceIndex := strings.LastIndex(trimmed, "{")
			if braceIndex != -1 {
				blockContent = strings.TrimSpace(trimmed[:braceIndex])
			} else {
				// Fallback shouldn't be needed with HasSuffix but handle defensively
				blockContent = strings.TrimSpace(trimmed[:len(trimmed)-1])
			}
		}

		// Identify potential block types based on the content *before* '{'
		isDefine := strings.HasPrefix(blockContent, "Define ") && blockOpened
		isStyle := strings.HasPrefix(blockContent, "style ") && blockOpened
		isProperties := blockContent == "Properties" && blockOpened
		// Element check: Starts with an uppercase letter convention?
		isElement := len(blockContent) > 0 && unicode.IsUpper(rune(blockContent[0])) && blockOpened

		// --- 3. Process Potential Block Start Lines ---
		if blockOpened {
			// --- 3a. Define Block ---
			if isDefine {
				if currentCtxType != CtxNone { return fmt.Errorf("L%d: 'Define' must be at the top level", currentLineNum) }
				parts := strings.Fields(blockContent); if len(parts) == 2 {
					name := parts[1]; if state.findComponentDef(name) != nil { return fmt.Errorf("L%d: component '%s' redefined", currentLineNum, name) }
					if len(state.ComponentDefs) >= MaxComponentDefs { return fmt.Errorf("L%d: maximum component definitions (%d) exceeded", currentLineNum, MaxComponentDefs) }
					def := ComponentDefinition{ Name: name, DefinitionStartLine: currentLineNum, Properties: make([]ComponentPropertyDef, 0, 4), DefinitionRootProperties: make([]SourceProperty, 0, 4), }
					state.ComponentDefs = append(state.ComponentDefs, def)
					currentComponentDef := &state.ComponentDefs[len(state.ComponentDefs)-1]
					if len(blockStack) >= MaxBlockDepth { return fmt.Errorf("L%d: max block depth exceeded", currentLineNum) }
					blockStack = append(blockStack, BlockStackEntry{indent, currentComponentDef, CtxComponentDef}); log.Printf("   Def: %s\n", name)
				} else { return fmt.Errorf("L%d: invalid Define syntax: '%s'", currentLineNum, blockContent) }
				continue // Handled Define
			}

			// --- 3b. Style Block ---
			if isStyle {
				if currentCtxType != CtxNone { return fmt.Errorf("L%d: 'style' must be at the top level", currentLineNum) }
				parts := strings.SplitN(blockContent, "\"", 3); if len(parts) == 3 && strings.TrimSpace(parts[0]) == "style" && strings.TrimSpace(parts[2]) == "" {
					name := parts[1]; if name == "" { return fmt.Errorf("L%d: style name cannot be empty", currentLineNum) }
					if state.findStyleByName(name) != nil { return fmt.Errorf("L%d: style '%s' redefined", currentLineNum, name) }
					if len(state.Styles) >= MaxStyles { return fmt.Errorf("L%d: maximum styles (%d) exceeded", currentLineNum, MaxStyles) }
					styleID := uint8(len(state.Styles) + 1); nameIdx, err := state.addString(name); if err != nil { return fmt.Errorf("L%d: failed adding style name '%s': %w", currentLineNum, name, err) }
					styleEntry := StyleEntry{ ID: styleID, SourceName: name, NameIndex: nameIdx, Properties: make([]KrbProperty, 0, 4), SourceProperties: make([]SourceProperty, 0, 8), CalculatedSize: 3, }
					state.Styles = append(state.Styles, styleEntry)
					currentStyle := &state.Styles[len(state.Styles)-1]
					if len(blockStack) >= MaxBlockDepth { return fmt.Errorf("L%d: max block depth exceeded", currentLineNum) }
					blockStack = append(blockStack, BlockStackEntry{indent, currentStyle, CtxStyle}); state.HeaderFlags |= FlagHasStyles
				} else { return fmt.Errorf("L%d: invalid style syntax: '%s', use 'style \"name\" {'", currentLineNum, blockContent) }
				continue // Handled Style
			}

			// --- 3c. Properties Block (inside Define) ---
			if isProperties {
				if currentCtxType != CtxComponentDef { return fmt.Errorf("L%d: 'Properties' block must be directly inside a 'Define' block", currentLineNum) }
				foundProperties := false; for i := len(blockStack) - 1; i >= 0; i-- { if blockStack[i].Type == CtxComponentDef { break }; if blockStack[i].Type == CtxProperties { foundProperties = true; break } }; if foundProperties { return fmt.Errorf("L%d: multiple 'Properties' blocks are invalid", currentLineNum) }
				if len(blockStack) >= MaxBlockDepth { return fmt.Errorf("L%d: max block depth exceeded", currentLineNum) }
				blockStack = append(blockStack, BlockStackEntry{indent, nil, CtxProperties}); continue // Handled Properties
			}

			// --- 3d. Element/Component Block Start ---
			if isElement {
				elementName := strings.Fields(blockContent)[0]
				parentIndex := -1; var parentElement *Element; validNestingContext := false

				// Determine if nesting is valid based on current context
				switch currentCtxType {
				case CtxNone: // Root element (App or Component usage)
					validNestingContext = true
				case CtxElement: // Standard nesting inside another element
					parentElement = currentContext.(*Element); parentIndex = parentElement.SelfIndex; validNestingContext = true
				case CtxComponentDef: // Potential root element *inside* Define { }
					def := currentContext.(*ComponentDefinition)
					if def.DefinitionRootType == "" { // Capturing the root type of the definition
						def.DefinitionRootType = elementName; log.Printf("      Def root: %s\n", elementName)
						if len(blockStack) >= MaxBlockDepth { return fmt.Errorf("L%d: max block depth exceeded", currentLineNum) }
						blockStack = append(blockStack, BlockStackEntry{indent, nil, CtxComponentDefBody}); continue // Switch context to Body
					} else { // Element start after root but directly under Define - invalid place
						log.Printf("L%d: Warning: Element '%s' inside Define '%s' block but *after* root type definition. Expected 'Properties' or end of block. Block ignored.", currentLineNum, elementName, def.Name)
						if len(blockStack) >= MaxBlockDepth { return fmt.Errorf("L%d: max block depth exceeded", currentLineNum) }
						blockStack = append(blockStack, BlockStackEntry{indent, nil, CtxNone}); continue // Ignore block
					}
				case CtxComponentDefBody: // *** Nested element inside the Define body - IGNORE ***
					defName := "unknown"; parentDefCtx := findParentContext(blockStack, CtxComponentDef); if parentDefCtx != nil { defName = parentDefCtx.(*ComponentDefinition).Name }
					log.Printf("L%d: Warning: Nested element '%s' inside Define '%s' body is not supported by expansion. Block ignored.", currentLineNum, elementName, defName)
					if len(blockStack) >= MaxBlockDepth { return fmt.Errorf("L%d: max block depth exceeded", currentLineNum) }
					blockStack = append(blockStack, BlockStackEntry{indent, nil, CtxNone}); continue // Push dummy context and SKIP processing this line further
				default:
					validNestingContext = false // Cannot nest inside Properties, Style, etc.
				}

				if !validNestingContext { return fmt.Errorf("L%d: cannot define element '%s' inside context %v", currentLineNum, elementName, currentCtxType) }
				if parentElement != nil && indent <= currentIndent { return fmt.Errorf("L%d: child element '%s' must be indented further than parent '%s'", currentLineNum, elementName, parentElement.SourceElementName) }
				if len(state.Elements) >= MaxElements { return fmt.Errorf("L%d: maximum elements (%d) exceeded", currentLineNum, MaxElements) }

				// Create Element struct
				elementIndex := len(state.Elements)
				el := Element{ SelfIndex: elementIndex, ParentIndex: parentIndex, SourceElementName: elementName, SourceLineNum: currentLineNum, CalculatedSize: KRBElementHeaderSize, SourceProperties: make([]SourceProperty, 0, 8), KrbProperties: make([]KrbProperty, 0, 8), KrbEvents: make([]KrbEvent, 0, 2), SourceChildrenIndices: make([]int, 0, 4), Children: make([]*Element, 0, 4), }

				// Check if it's a defined component usage or a standard element
				compDef := state.findComponentDef(elementName)
				if compDef != nil { // Component Usage
					el.Type = ElemTypeInternalComponentUsage; el.ComponentDef = compDef; el.IsComponentInstance = true
				} else { // Standard Element
					el.Type = getElementTypeFromName(elementName); el.IsComponentInstance = false
					if el.Type == ElemTypeUnknown {
						el.Type = ElemTypeCustomBase; nameIdx, err := state.addString(elementName); if err != nil { return fmt.Errorf("L%d: failed adding custom element name '%s': %w", currentLineNum, elementName, err) }; el.IDStringIndex = nameIdx
						log.Printf("L%d: Warn: Unknown element type '%s', using custom type 0x%X with name index %d\n", currentLineNum, elementName, el.Type, nameIdx)
					}
					// Root Checks
					if el.Type == ElemTypeApp { if state.HasApp || parentElement != nil { return fmt.Errorf("L%d: 'App' element must be the single root element", currentLineNum) }; state.HasApp = true; state.HeaderFlags |= FlagHasApp } else { if parentElement == nil && !state.HasApp { return fmt.Errorf("L%d: root element must be 'App', found '%s'", currentLineNum, elementName) } }
				}

				// Add element to state, update parent's children, push to stack
				state.Elements = append(state.Elements, el)
				currentElement := &state.Elements[elementIndex]
				if parentElement != nil { if len(parentElement.SourceChildrenIndices) >= MaxChildren { return fmt.Errorf("L%d: maximum children (%d) exceeded for parent '%s'", currentLineNum, MaxChildren, parentElement.SourceElementName) }; parentElement.SourceChildrenIndices = append(parentElement.SourceChildrenIndices, currentElement.SelfIndex) }
				if len(blockStack) >= MaxBlockDepth { return fmt.Errorf("L%d: max block depth exceeded", currentLineNum) }
				blockStack = append(blockStack, BlockStackEntry{indent, currentElement, CtxElement}); continue // Handled element start
			}

			// If block opened but wasn't Define, Style, Properties, or Element
			return fmt.Errorf("L%d: invalid block start syntax: '%s'", currentLineNum, trimmed) // Use trimmed which includes '{' here

		} // --- End of `if blockOpened` ---

		// --- 4. Process Non-Block-Starting Lines (Properties / Ignored Content) ---
		// This section only runs if the line did NOT start a block
		if len(blockStack) > 0 && indent > currentIndent {
			// Line is indented relative to current block context.

			// Check if it looks like 'key: value', ensuring key doesn't contain '{' or '}'
			parts := strings.SplitN(trimmed, ":", 2)
			keyPart := ""; if len(parts) > 0 { keyPart = strings.TrimSpace(parts[0]) }
			isPropertyLike := len(parts) == 2 && len(keyPart) > 0 && !strings.ContainsAny(keyPart, "{}")

			// Check if it looks like an element start (even without blockOpened being true)
			// This helps catch ignored element lines more reliably in Step 4 contexts
			firstWord := ""; if fw := strings.Fields(trimmed); len(fw) > 0 { firstWord = fw[0] }
			startsWithElement := len(firstWord) > 0 && unicode.IsUpper(rune(firstWord[0])) && strings.Contains(trimmed, "{")

			// Get current context details
			entry := blockStack[len(blockStack)-1]
			contextType := entry.Type
			contextObject := entry.Context // Can be *Element, *StyleEntry, *ComponentDefinition, or nil

			// Process based on the context
			switch contextType {
			case CtxProperties: // Inside Define -> Properties { }
				if startsWithElement { log.Printf("L%d: Warning: Ignoring line starting like element inside Properties block: '%s'", currentLineNum, trimmed); continue }
				if !isPropertyLike { return fmt.Errorf("L%d: invalid syntax inside Properties block (expected 'key: Type [= DefaultValue]'): '%s'", currentLineNum, trimmed) }
				key := keyPart; valueStr := strings.TrimSpace(parts[1])
				parentDef := findParentContext(blockStack, CtxComponentDef); if parentDef == nil { return fmt.Errorf("L%d: internal error: CtxProperties without parent CtxComponentDef", currentLineNum) }
				parentComponentDef := parentDef.(*ComponentDefinition)
				valParts := strings.SplitN(valueStr, "=", 2); propType := strings.TrimSpace(valParts[0]); propDefault := ""; if len(valParts) == 2 { propDefault = strings.TrimSpace(valParts[1]) /* keep quotes */ }
				if len(parentComponentDef.Properties) >= MaxProperties { return fmt.Errorf("L%d: max properties defined", currentLineNum) }
				pd := ComponentPropertyDef{Name: key, DefaultValueStr: propDefault}; switch propType { case "String": pd.ValueTypeHint = ValTypeString; case "Int": pd.ValueTypeHint = ValTypeShort; case "Bool": pd.ValueTypeHint = ValTypeByte; case "Color": pd.ValueTypeHint = ValTypeColor; case "StyleID": pd.ValueTypeHint = ValTypeStyleID; case "Resource": pd.ValueTypeHint = ValTypeResource; case "Float": pd.ValueTypeHint = ValTypeFloat; default: log.Printf("L%d: Warn: Unknown prop type hint '%s' for '%s'", currentLineNum, propType, key); pd.ValueTypeHint = ValTypeCustom }; parentComponentDef.Properties = append(parentComponentDef.Properties, pd)


			case CtxStyle: // Inside style "name" { }
				if !isPropertyLike { return fmt.Errorf("L%d: invalid property syntax inside Style block (expected 'key: value'): '%s'", currentLineNum, trimmed) }
				key := keyPart; valueStrRaw := strings.TrimSpace(parts[1]) // Keep raw value for addSourceProperty if needed
				if contextObject == nil { return fmt.Errorf("L%d: internal error: nil context for CtxStyle", currentLineNum) }
				parentStyle := contextObject.(*StyleEntry)

				// *** Process 'extends' specifically ***
				if key == "extends" {
					// Clean the value to get the base name *without* comments or quotes
					baseName, _ := cleanAndQuoteValue(valueStrRaw) // Use helper
					if baseName == "" { return fmt.Errorf("L%d: 'extends' requires non-empty base style name in style '%s'", currentLineNum, parentStyle.SourceName) }
					if baseName == parentStyle.SourceName { return fmt.Errorf("L%d: style '%s' cannot extend itself", currentLineNum, parentStyle.SourceName) }
					if parentStyle.ExtendsStyleName != "" { return fmt.Errorf("L%d: style '%s' specifies 'extends' multiple times", currentLineNum, parentStyle.SourceName) }

					// Store the CLEANED base name
					parentStyle.ExtendsStyleName = baseName
					log.Printf("   Parsed Style Extends: %s -> %s\n", parentStyle.SourceName, baseName)
					// NOTE: We DO NOT call addSourceProperty for 'extends' itself.
				} else {
					// Call the method directly on the style entry
					err := parentStyle.addSourceProperty(key, valueStrRaw, currentLineNum) // Calls method defined in constants.go
					if err != nil {
						return fmt.Errorf("L%d: %w", currentLineNum, err)
					}
				}
			
			case CtxElement: // Inside Element { }
				if startsWithElement { log.Printf("L%d: Warning: Ignoring line starting like element inside Element block '%s': '%s'", currentLineNum, contextObject.(*Element).SourceElementName, trimmed); continue }
				if !isPropertyLike { return fmt.Errorf("L%d: invalid property syntax inside Element '%s' (expected 'key: value'): '%s'", currentLineNum, contextObject.(*Element).SourceElementName, trimmed) }
				key := keyPart; valueStr := strings.TrimSpace(parts[1]) // Keep quotes/spacing
				parentElement := contextObject.(*Element)
				err := parentElement.addSourceProperty(key, valueStr, currentLineNum); if err != nil { return err }

			case CtxComponentDefBody: // Inside Define -> RootType { }
				parentDef := findParentContext(blockStack, CtxComponentDef); if parentDef == nil { return fmt.Errorf("L%d: internal error: CtxComponentDefBody without parent CtxComponentDef", currentLineNum) }
				parentComponentDef := parentDef.(*ComponentDefinition)
				// *** Explicitly ignore lines that start like elements ***
				if startsWithElement {
					log.Printf("L%d: Warning: Ignoring nested element-like line inside Define '%s' body: '%s'", currentLineNum, parentComponentDef.Name, trimmed)
				} else if !isPropertyLike { // Not element-like, not property-like
					log.Printf("L%d: Warning: Ignoring unexpected line inside Define '%s' body (not 'key: value'): '%s'", currentLineNum, parentComponentDef.Name, trimmed)
				} else { // Looks like a property for the root element definition
					key := keyPart; valueStr := strings.TrimSpace(parts[1]) // Keep quotes/spacing
					if parentComponentDef.DefinitionRootType == "" { return fmt.Errorf("L%d: internal error: property '%s' found in component definition body before root element type was defined", currentLineNum, key) }
					err := parentComponentDef.addDefinitionRootProperty(key, valueStr, currentLineNum); if err != nil { return err }
				}

			case CtxComponentDef: // Directly under Define { } - Warn and ignore
				if startsWithElement { log.Printf("L%d: Warning: Ignoring line starting like element directly under Define block: '%s'", currentLineNum, trimmed); continue }
				if isPropertyLike { log.Printf("L%d: Warning: Property '%s' found directly under Define block, expected inside 'Properties' or root element body. Ignoring.", currentLineNum, keyPart) } else { log.Printf("L%d: Warning: Ignoring unexpected line directly under Define block: '%s'", currentLineNum, trimmed) }

			default: // Includes CtxNone (inside ignored blocks) - Ignore silently
				if startsWithElement || isPropertyLike { log.Printf("L%d: Debug: Ignoring indented line in context %v: '%s'\n", currentLineNum, contextType, trimmed) }
			}

			continue // Handled (or ignored) indented line

		} // End indented line handling

		// --- 5. Handle Errors for Lines That Don't Match Above ---
		// Line wasn't block end, block start, or indented property/ignored content
		if indent <= currentIndent && len(blockStack) > 0 { return fmt.Errorf("L%d: unexpected syntax or indentation decrease: '%s'", currentLineNum, trimmed) }
		if indent > currentIndent && len(blockStack) == 0 { return fmt.Errorf("L%d: unexpected indentation at top level: '%s'", currentLineNum, trimmed) }
		if !blockOpened && (isDefine || isStyle || isProperties || isElement) { return fmt.Errorf("L%d: missing '{' to start block: '%s'", currentLineNum, trimmed) }
		return fmt.Errorf("L%d: unrecognized syntax: '%s'", currentLineNum, trimmed)

	} // End scanner loop

	if err := scanner.Err(); err != nil { return fmt.Errorf("error reading source buffer: %w", err) }

	// --- Final Checks ---
	if len(blockStack) != 0 { return fmt.Errorf("unclosed block at end of file (last context: %v)", blockStack[len(blockStack)-1].Type) }
	if !state.HasApp {
		isRootComponent := len(state.Elements) > 0 && state.Elements[0].IsComponentInstance
		if !isRootComponent && (len(state.Elements) == 0 || state.Elements[0].Type != ElemTypeApp) {
			appFound := false; for _, el := range state.Elements { if el.Type == ElemTypeApp { appFound = true; break } }
			if !appFound { return errors.New("no 'App' element defined") }
			return errors.New("'App' element found but it's not the root element")
		}
		if isRootComponent { log.Println("Info: Root element is a component. Assuming 'App' behavior is provided."); state.HasApp = true }
	}
	return nil // Success
}


// --- State Management Helpers (addString, addResource, find*, etc.) ---
// --- Property Handling Helpers (addKrbProperty, addSourceProperty, etc.) ---
// (Paste the helper functions from previous correct versions here)

// addString adds a string to the state's string table if unique, returning its 0-based index.
func (state *CompilerState) addString(text string) (uint8, error) {
	if text == "" {
		if len(state.Strings) == 0 {
			state.Strings = append(state.Strings, StringEntry{Text: "", Length: 0, Index: 0})
		}
		return 0, nil
	}
	cleaned := trimQuotes(strings.TrimSpace(text))
	if cleaned == "" {
		if len(state.Strings) == 0 {
			state.Strings = append(state.Strings, StringEntry{Text: "", Length: 0, Index: 0})
		}
		return 0, nil
	}
	for i := 1; i < len(state.Strings); i++ {
		if state.Strings[i].Text == cleaned {
			return state.Strings[i].Index, nil
		}
	}
	if len(state.Strings) >= MaxStrings {
		return 0, fmt.Errorf("maximum string limit (%d) exceeded", MaxStrings)
	}
	if len(state.Strings) == 0 {
		state.Strings = append(state.Strings, StringEntry{Text: "", Length: 0, Index: 0})
	}
	idx := uint8(len(state.Strings))
	entry := StringEntry{Text: cleaned, Length: len(cleaned), Index: idx}
	state.Strings = append(state.Strings, entry)
	return idx, nil
}

// addResource adds a resource if unique, returns 0-based index.
func (state *CompilerState) addResource(resType uint8, pathStr string) (uint8, error) {
	pathIdx, err := state.addString(pathStr)
	if err != nil {
		return 0, fmt.Errorf("failed to add resource path '%s' to string table: %w", pathStr, err)
	}
	if pathIdx == 0 && len(strings.TrimSpace(pathStr)) > 0 {
		return 0, fmt.Errorf("failed to add non-empty resource path '%s' resulting in index 0", pathStr)
	}
	if pathIdx == 0 {
		return 0, fmt.Errorf("resource path cannot be empty")
	}
	format := ResFormatExternal
	for i := 0; i < len(state.Resources); i++ {
		if state.Resources[i].Type == resType && state.Resources[i].Format == format && state.Resources[i].DataStringIndex == pathIdx {
			return state.Resources[i].Index, nil
		}
	}
	if len(state.Resources) >= MaxResources {
		return 0, fmt.Errorf("maximum resource limit (%d) exceeded", MaxResources)
	}
	idx := uint8(len(state.Resources))
	entry := ResourceEntry{Type: resType, NameIndex: pathIdx, Format: format, DataStringIndex: pathIdx, Index: idx, CalculatedSize: 4}
	state.Resources = append(state.Resources, entry)
	state.HeaderFlags |= FlagHasResources
	return idx, nil
}

// findComponentDef finds a ComponentDefinition by name.
func (state *CompilerState) findComponentDef(name string) *ComponentDefinition {
	if name == "" {
		return nil
	}
	for i := range state.ComponentDefs {
		if state.ComponentDefs[i].Name == name {
			return &state.ComponentDefs[i]
		}
	}
	return nil
}

func findParentContext(stack []BlockStackEntry, targetType BlockContextType) interface{} {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].Type == targetType {
			return stack[i].Context
		}
	}
	return nil
}

// addKrbProperty adds a resolved KRB property to an element.
func (el *Element) addKrbProperty(propID, valType uint8, data []byte) error {
	if len(el.KrbProperties) >= MaxProperties {
		return fmt.Errorf("L%d: maximum KRB properties (%d) exceeded for element '%s'", el.SourceLineNum, MaxProperties, el.SourceElementName)
	}
	if len(data) > 255 {
		return fmt.Errorf("L%d: property data size (%d) exceeds maximum (255) for element '%s', prop ID %d", el.SourceLineNum, len(data), el.SourceElementName, propID)
	}
	prop := KrbProperty{PropertyID: propID, ValueType: valType, Size: uint8(len(data)), Value: data}
	el.KrbProperties = append(el.KrbProperties, prop)
	el.PropertyCount = uint8(len(el.KrbProperties))
	return nil
}

// addKrbStringProperty adds a string property (looks up/adds string, stores index).
func (state *CompilerState) addKrbStringProperty(el *Element, propID uint8, valueStr string) error {
	idx, err := state.addString(valueStr)
	if err != nil {
		return fmt.Errorf("L%d: failed adding string for property %d: %w", el.SourceLineNum, propID, err)
	}
	return el.addKrbProperty(propID, ValTypeString, []byte{idx})
}

// addKrbResourceProperty adds a resource property (looks up/adds resource, stores index).
func (state *CompilerState) addKrbResourceProperty(el *Element, propID, resType uint8, pathStr string) error {
	idx, err := state.addResource(resType, pathStr)
	if err != nil {
		return fmt.Errorf("L%d: failed adding resource for property %d: %w", el.SourceLineNum, propID, err)
	}
	return el.addKrbProperty(propID, ValTypeResource, []byte{idx})
}

// addStyleKrbProperty adds a resolved KRB property to a style definition.
func (style *StyleEntry) addStyleKrbProperty(propID, valType uint8, data []byte) error {
	if len(style.Properties) >= MaxProperties {
		return fmt.Errorf("maximum KRB properties (%d) exceeded for style '%s'", MaxProperties, style.SourceName)
	}
	if len(data) > 255 {
		return fmt.Errorf("property data size (%d) exceeds maximum (255) for style '%s', prop ID %d", len(data), style.SourceName, propID)
	}
	prop := KrbProperty{PropertyID: propID, ValueType: valType, Size: uint8(len(data)), Value: data}
	style.Properties = append(style.Properties, prop)
	return nil
}

// addStyleKrbStringProperty adds a string property to a style definition.
func (state *CompilerState) addStyleKrbStringProperty(style *StyleEntry, propID uint8, valueStr string) error {
	idx, err := state.addString(valueStr)
	if err != nil {
		return fmt.Errorf("failed adding string for style property %d in style '%s': %w", propID, style.SourceName, err)
	}
	return style.addStyleKrbProperty(propID, ValTypeString, []byte{idx})
}

// getSourcePropertyValue retrieves the last value string for a given key from an element's source properties.
func (el *Element) getSourcePropertyValue(key string) (string, bool) {
	// Search backwards to respect potential overrides during property merging
	for i := len(el.SourceProperties) - 1; i >= 0; i-- {
		if el.SourceProperties[i].Key == key {
			return el.SourceProperties[i].ValueStr, true
		}
	}
	return "", false
}

// addSourceProperty adds a raw key-value pair from the .kry source to an element. Overwrites if key exists.
func (el *Element) addSourceProperty(key, value string, lineNum int) error {
	for i := range el.SourceProperties {
		if el.SourceProperties[i].Key == key {
			el.SourceProperties[i].ValueStr = value
			el.SourceProperties[i].LineNum = lineNum
			return nil
		}
	}
	if len(el.SourceProperties) >= MaxProperties {
		return fmt.Errorf("L%d: maximum source properties (%d) exceeded for element '%s'", lineNum, MaxProperties, el.SourceElementName)
	}
	prop := SourceProperty{Key: key, ValueStr: value, LineNum: lineNum}
	el.SourceProperties = append(el.SourceProperties, prop)
	return nil
}

// addDefinitionRootProperty adds a raw key-value pair to a component definition's root properties. Overwrites if key exists.
func (def *ComponentDefinition) addDefinitionRootProperty(key, value string, lineNum int) error {
	for i := range def.DefinitionRootProperties {
		if def.DefinitionRootProperties[i].Key == key {
			def.DefinitionRootProperties[i].ValueStr = value
			def.DefinitionRootProperties[i].LineNum = lineNum
			return nil
		}
	}
	if len(def.DefinitionRootProperties) >= MaxProperties {
		return fmt.Errorf("L%d: maximum root properties (%d) exceeded for component definition '%s'", lineNum, MaxProperties, def.Name)
	}
	prop := SourceProperty{Key: key, ValueStr: value, LineNum: lineNum}
	def.DefinitionRootProperties = append(def.DefinitionRootProperties, prop)
	return nil
}
