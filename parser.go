// parser.go
package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"strconv"
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
		trimmed := line
		indent := 0
		for _, r := range trimmed {
			if r == ' ' {
				indent++
			} else if r == '\t' {
				indent += 4 // Or configurable tab width
			} else {
				break
			}
		}
		trimmed = strings.TrimLeftFunc(trimmed, unicode.IsSpace) // Remove leading whitespace

		// Handle comments starting at the beginning of the effective line
		if strings.HasPrefix(trimmed, "#") {
			trimmed = "" // Treat the whole line as a comment
		}
		trimmed = strings.TrimRightFunc(trimmed, unicode.IsSpace) // Trim trailing whitespace

		// Skip empty lines
		if trimmed == "" {
			continue
		}

		// Basic line length check
		if len(line) > MaxLineLength {
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
		blockOpened := strings.HasSuffix(trimmed, "{")
		blockContent := trimmed
		if blockOpened {
			blockContent = strings.TrimSpace(trimmed[:len(trimmed)-1])
		}

		// Check various block start keywords/patterns
		isDefine := strings.HasPrefix(blockContent, "Define ") && blockOpened
		isStyle := strings.HasPrefix(blockContent, "style ") && blockOpened
		isProperties := blockContent == "Properties" && blockOpened
		// Check if first char is a letter (simple check for element/component name start)
		isElement := len(blockContent) > 0 && unicode.IsLetter(rune(blockContent[0])) && blockOpened

		// --- 3. Process Potential Block Start Lines ---
		if blockOpened {
			// --- 3a. Define Block ---
			if isDefine {
				if currentCtxType != CtxNone {
					return fmt.Errorf("L%d: 'Define' must be at the top level", currentLineNum)
				}
				parts := strings.Fields(blockContent) // ["Define", "WidgetName"]
				if len(parts) == 2 {
					name := parts[1]
					if state.findComponentDef(name) != nil {
						return fmt.Errorf("L%d: component '%s' redefined", currentLineNum, name)
					}
					if len(state.ComponentDefs) >= MaxComponentDefs {
						return fmt.Errorf("L%d: maximum component definitions (%d) exceeded", currentLineNum, MaxComponentDefs)
					}
					def := ComponentDefinition{
						Name:                     name,
						DefinitionStartLine:      currentLineNum,
						Properties:               make([]ComponentPropertyDef, 0, 4),
						DefinitionRootProperties: make([]SourceProperty, 0, 4),
					}
					state.ComponentDefs = append(state.ComponentDefs, def)
					currentComponentDef := &state.ComponentDefs[len(state.ComponentDefs)-1]
					if len(blockStack) >= MaxBlockDepth {
						return fmt.Errorf("L%d: max block depth exceeded", currentLineNum)
					}
					blockStack = append(blockStack, BlockStackEntry{indent, currentComponentDef, CtxComponentDef})
					log.Printf("   Def: %s\n", name)
				} else {
					return fmt.Errorf("L%d: invalid Define syntax: '%s'", currentLineNum, blockContent)
				}
				continue // Handled
			}

			// --- 3b. Style Block ---
			if isStyle {
				if currentCtxType != CtxNone {
					return fmt.Errorf("L%d: 'style' must be at the top level", currentLineNum)
				}
				parts := strings.SplitN(blockContent, "\"", 3) // style "name"
				if len(parts) == 3 && strings.TrimSpace(parts[0]) == "style" && strings.TrimSpace(parts[2]) == "" {
					name := parts[1]
					if name == "" {
						return fmt.Errorf("L%d: style name cannot be empty", currentLineNum)
					}
					if state.findStyleByName(name) != nil {
						return fmt.Errorf("L%d: style '%s' redefined", currentLineNum, name)
					}
					if len(state.Styles) >= MaxStyles {
						return fmt.Errorf("L%d: maximum styles (%d) exceeded", currentLineNum, MaxStyles)
					}
					styleID := uint8(len(state.Styles) + 1)
					nameIdx, err := state.addString(name)
					if err != nil {
						return fmt.Errorf("L%d: failed adding style name '%s': %w", currentLineNum, name, err)
					}
					styleEntry := StyleEntry{
						ID:             styleID,
						SourceName:     name,
						NameIndex:      nameIdx,
						Properties:     make([]KrbProperty, 0, 4),
						CalculatedSize: 3,
					}
					state.Styles = append(state.Styles, styleEntry)
					currentStyle := &state.Styles[len(state.Styles)-1]
					if len(blockStack) >= MaxBlockDepth {
						return fmt.Errorf("L%d: max block depth exceeded", currentLineNum)
					}
					blockStack = append(blockStack, BlockStackEntry{indent, currentStyle, CtxStyle})
					state.HeaderFlags |= FlagHasStyles
				} else {
					return fmt.Errorf("L%d: invalid style syntax: '%s', use 'style \"name\" {'", currentLineNum, blockContent)
				}
				continue // Handled
			}

			// --- 3c. Properties Block (inside Define) ---
			if isProperties {
				if currentCtxType != CtxComponentDef {
					return fmt.Errorf("L%d: 'Properties' block must be directly inside a 'Define' block", currentLineNum)
				}
				// Check if already in properties block implicitly by searching stack
				foundProperties := false
				for i := len(blockStack) - 1; i >= 0; i-- {
					if blockStack[i].Type == CtxComponentDef {
						break
					} // Stop at parent Define
					if blockStack[i].Type == CtxProperties {
						foundProperties = true
						break
					}
				}
				if foundProperties {
					return fmt.Errorf("L%d: multiple 'Properties' blocks are invalid", currentLineNum)
				}

				if len(blockStack) >= MaxBlockDepth {
					return fmt.Errorf("L%d: max block depth exceeded", currentLineNum)
				}
				blockStack = append(blockStack, BlockStackEntry{indent, nil, CtxProperties})
				continue // Handled
			}

			// --- 3d. Element/Component Block ---
			if isElement {
				elementName := strings.Fields(blockContent)[0]

				// Check if valid context for nesting
				parentIndex := -1
				var parentElement *Element
				validNestingContext := false

				if currentCtxType == CtxNone { // Root element (must be App or Component)
					validNestingContext = true
				} else if currentCtxType == CtxElement { // Nested inside another element
					parentElement = currentContext.(*Element)
					parentIndex = parentElement.SelfIndex
					validNestingContext = true
				} else if currentCtxType == CtxComponentDef { // Root element *inside* a Define block
					def := currentContext.(*ComponentDefinition)
					if def.DefinitionRootType == "" {
						def.DefinitionRootType = elementName
						if len(blockStack) >= MaxBlockDepth {
							return fmt.Errorf("L%d: max block depth exceeded", currentLineNum)
						}
						blockStack = append(blockStack, BlockStackEntry{indent, nil, CtxComponentDefBody})
						log.Printf("      Def root: %s\n", elementName)
						continue // Handled special case: Define root type capture
					} else {
						log.Printf("L%d: Warning: Ignoring nested element '%s' inside Define '%s' (simple expansion only).\n", currentLineNum, elementName, def.Name)
						// Push dummy context to handle closing brace
						if len(blockStack) >= MaxBlockDepth {
							return fmt.Errorf("L%d: max block depth exceeded", currentLineNum)
						}
						blockStack = append(blockStack, BlockStackEntry{indent, nil, CtxNone})
						continue // Handled (ignored) nested element in define
					}
				}
				// Add other valid contexts if needed (e.g., CtxComponentDefBody if deep nesting allowed)

				if !validNestingContext {
					return fmt.Errorf("L%d: cannot define element '%s' inside context %v", currentLineNum, elementName, currentCtxType)
				}

				// Check indentation relative to parent
				if parentElement != nil && indent <= currentIndent {
					return fmt.Errorf("L%d: child element '%s' must be indented further than parent '%s'", currentLineNum, elementName, parentElement.SourceElementName)
				}

				// Proceed with creating the element
				if len(state.Elements) >= MaxElements {
					return fmt.Errorf("L%d: maximum elements (%d) exceeded", currentLineNum, MaxElements)
				}
				elementIndex := len(state.Elements)
				el := Element{
					SelfIndex:             elementIndex, ParentIndex: parentIndex, SourceElementName: elementName,
					SourceLineNum:         currentLineNum, CalculatedSize: KRBElementHeaderSize,
					SourceProperties:      make([]SourceProperty, 0, 8), KrbProperties: make([]KrbProperty, 0, 8),
					KrbEvents:             make([]KrbEvent, 0, 2), SourceChildrenIndices: make([]int, 0, 4),
					Children:              make([]*Element, 0, 4),
				}
				compDef := state.findComponentDef(elementName)
				if compDef != nil {
					el.Type = ElemTypeInternalComponentUsage
					el.ComponentDef = compDef
					el.IsComponentInstance = true
				} else { // It's not a component, handle standard types and root checks
					el.Type = getElementTypeFromName(elementName)
					if el.Type == ElemTypeUnknown {
						el.Type = ElemTypeCustomBase
						nameIdx, err := state.addString(elementName)
						if err != nil {
							return fmt.Errorf("L%d: failed adding custom element name '%s': %w", currentLineNum, elementName, err)
						}
						el.IDStringIndex = nameIdx
						log.Printf("L%d: Warn: Unknown element type '%s', using custom type 0x%X with name index %d\n", currentLineNum, elementName, el.Type, nameIdx)
					}
					el.IsComponentInstance = false

					// --- Root Checks (Corrected Structure) ---
					if el.Type == ElemTypeApp {
						if state.HasApp || parentElement != nil { // App must be root and only one
							return fmt.Errorf("L%d: 'App' element must be the single root element", currentLineNum)
						}
						state.HasApp = true
						state.HeaderFlags |= FlagHasApp
					} else { // It's not App
						if parentElement == nil && !state.HasApp { // If at root and App not seen yet
							return fmt.Errorf("L%d: root element must be 'App', found '%s'", currentLineNum, elementName)
						}
					}
					// --- End Root Checks ---

				} // End `else` for `if compDef != nil`

				state.Elements = append(state.Elements, el)
				currentElement := &state.Elements[elementIndex]
				if parentElement != nil {
					if len(parentElement.SourceChildrenIndices) >= MaxChildren {
						return fmt.Errorf("L%d: maximum children (%d) exceeded for parent '%s'", currentLineNum, MaxChildren, parentElement.SourceElementName)
					}
					parentElement.SourceChildrenIndices = append(parentElement.SourceChildrenIndices, currentElement.SelfIndex)
				}
				if len(blockStack) >= MaxBlockDepth {
					return fmt.Errorf("L%d: max block depth exceeded", currentLineNum)
				}
				blockStack = append(blockStack, BlockStackEntry{indent, currentElement, CtxElement})

				continue // Handled element block start
			}

			// If it looked like a block start but wasn't handled above
			return fmt.Errorf("L%d: invalid block start syntax: '%s'", currentLineNum, trimmed)

		} // --- End of `if blockOpened` ---

		// --- 4. Process Non-Block-Starting Lines (Properties) ---
		// This code only runs if the line didn't start a block (`blockOpened == false`)
		if len(blockStack) > 0 && indent > currentIndent {
			// This line MUST be a property within the current context
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) != 2 {
				// It's indented but doesn't look like 'key: value'
				return fmt.Errorf("L%d: invalid property syntax (expected 'key: value'): '%s'", currentLineNum, trimmed)
			}
			key := strings.TrimSpace(parts[0])
			valueStr := strings.TrimSpace(parts[1]) // Trim whitespace, keep quotes for now

			// Find the relevant context for adding the property
			var parentElement *Element
			var parentStyle *StyleEntry
			var parentComponentDef *ComponentDefinition // For Define->Properties or Define->Body
			inPropertiesBlock := false
			inComponentDefBody := false

			// Search up stack
			for i := len(blockStack) - 1; i >= 0; i-- {
				entry := blockStack[i]
				if entry.Type == CtxProperties {
					inPropertiesBlock = true
					for j := i - 1; j >= 0; j-- {
						if blockStack[j].Type == CtxComponentDef {
							parentComponentDef = blockStack[j].Context.(*ComponentDefinition)
							break
						}
					}
					break
				} else if entry.Type == CtxComponentDefBody {
					inComponentDefBody = true
					for j := i - 1; j >= 0; j-- {
						if blockStack[j].Type == CtxComponentDef {
							parentComponentDef = blockStack[j].Context.(*ComponentDefinition)
							break
						}
					}
					break
				} else if entry.Type == CtxStyle {
					parentStyle = entry.Context.(*StyleEntry)
					break
				} else if entry.Type == CtxElement {
					parentElement = entry.Context.(*Element)
					break
				} else if entry.Type == CtxComponentDef && parentComponentDef == nil {
					parentComponentDef = entry.Context.(*ComponentDefinition)
				}
			}

			// --- Add property based on context ---
			if inPropertiesBlock && parentComponentDef != nil {
				// Define -> Properties { key: Type = Default }
				rest := valueStr // valueStr is "Type = Default" or just "Type"
				valParts := strings.SplitN(rest, "=", 2)
				propType := strings.TrimSpace(valParts[0])
				propDefault := ""
				if len(valParts) == 2 {
					propDefault = trimQuotes(strings.TrimSpace(valParts[1]))
				}
				if len(parentComponentDef.Properties) >= MaxProperties {
					return fmt.Errorf("L%d: max properties defined", currentLineNum)
				}
				pd := ComponentPropertyDef{Name: key, DefaultValueStr: propDefault}
				switch propType {
				case "String":
					pd.ValueTypeHint = ValTypeString
				case "Int":
					pd.ValueTypeHint = ValTypeShort
				case "Bool":
					pd.ValueTypeHint = ValTypeByte
				case "Color":
					pd.ValueTypeHint = ValTypeColor
				case "StyleID":
					pd.ValueTypeHint = ValTypeStyleID
				case "Resource":
					pd.ValueTypeHint = ValTypeResource
				case "Float":
					pd.ValueTypeHint = ValTypeFloat
				default:
					log.Printf("L%d: Warn: Unknown prop type hint '%s'", currentLineNum, propType)
					pd.ValueTypeHint = ValTypeCustom
				}
				parentComponentDef.Properties = append(parentComponentDef.Properties, pd)

			} else if parentStyle != nil {
				// Style { key: value }
				var err error
				switch key {
				case "background_color":
					if c, ok := parseColor(valueStr); ok {
						err = parentStyle.addStyleKrbProperty(PropIDBgColor, ValTypeColor, c[:])
						state.HeaderFlags |= FlagExtendedColor
					} else {
						err = fmt.Errorf("invalid color '%s'", valueStr)
					}
				case "text_color", "foreground_color":
					if c, ok := parseColor(valueStr); ok {
						err = parentStyle.addStyleKrbProperty(PropIDFgColor, ValTypeColor, c[:])
						state.HeaderFlags |= FlagExtendedColor
					} else {
						err = fmt.Errorf("invalid color '%s'", valueStr)
					}
				case "border_color":
					if c, ok := parseColor(valueStr); ok {
						err = parentStyle.addStyleKrbProperty(PropIDBorderColor, ValTypeColor, c[:])
						state.HeaderFlags |= FlagExtendedColor
					} else {
						err = fmt.Errorf("invalid color '%s'", valueStr)
					}
				case "border_width":
					if v, e := strconv.ParseUint(valueStr, 10, 8); e == nil {
						err = parentStyle.addStyleKrbProperty(PropIDBorderWidth, ValTypeByte, []byte{uint8(v)})
					} else {
						err = fmt.Errorf("invalid uint8 '%s':%w", valueStr, e)
					}
				case "layout":
					err = parentStyle.addStyleKrbProperty(PropIDLayoutFlags, ValTypeByte, []byte{parseLayoutString(valueStr)})
				// ... other style props ...
				default:
					log.Printf("L%d: Warn: Unhandled style prop '%s'", currentLineNum, key)
				}
				if err != nil {
					return fmt.Errorf("L%d: error processing style prop '%s': %w", currentLineNum, key, err)
				}

			} else if parentElement != nil {
				// Element { key: value }
				err := parentElement.addSourceProperty(key, valueStr, currentLineNum) // Keep quotes here
				if err != nil {
					return err
				}

			} else if inComponentDefBody && parentComponentDef != nil {
				// Define -> RootType { key: value }
				if parentComponentDef.DefinitionRootType == "" {
					return fmt.Errorf("L%d: property found before root element type", currentLineNum)
				}
				err := parentComponentDef.addDefinitionRootProperty(key, valueStr, currentLineNum) // Keep quotes
				if err != nil {
					return err
				}

			} else {
				// Indented property line, but no valid context found
				return fmt.Errorf("L%d: unexpected property line '%s' in context type %v", currentLineNum, trimmed, currentCtxType)
			}

			continue // Handled property line
		}

		// --- 5. Handle Errors for Lines That Don't Match Above ---
		if indent <= currentIndent && len(blockStack) > 0 {
			// This line is not a block end, not a block start, and not a correctly indented property
			return fmt.Errorf("L%d: unexpected syntax or indentation: '%s'", currentLineNum, trimmed)
		}
		if indent > currentIndent && len(blockStack) == 0 {
			return fmt.Errorf("L%d: unexpected indentation at top level: '%s'", currentLineNum, trimmed)
		}
		// If it reached here, it's likely an unhandled case or invalid syntax at the current indent level
		// Check if it was maybe intended as a block start but missing the '{' ?
		if !blockOpened && (isDefine || isStyle || isProperties || isElement) {
			return fmt.Errorf("L%d: missing '{' to start block: '%s'", currentLineNum, trimmed)
		}
		return fmt.Errorf("L%d: unrecognized syntax: '%s'", currentLineNum, trimmed)

	} // End scanner loop

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading source buffer: %w", err)
	}

	// --- Final Checks ---
	if len(blockStack) != 0 {
		return fmt.Errorf("unclosed block at end of file (last context: %v)", blockStack[len(blockStack)-1].Type)
	}
	if !state.HasApp { // Check if App was defined (handling root component case)
		isRootComponent := len(state.Elements) > 0 && state.Elements[0].IsComponentInstance
		if !isRootComponent && (len(state.Elements) == 0 || state.Elements[0].Type != ElemTypeApp) {
			// Check if App exists anywhere (might indicate incorrect placement)
			appFound := false
			for _, el := range state.Elements {
				if el.Type == ElemTypeApp { appFound = true; break }
			}
			if !appFound { return errors.New("no 'App' element defined") }
			return errors.New("'App' element found but it's not the root element")
		}
		if isRootComponent {
			log.Println("Warning: Root element is a component. 'App' definition is required (potentially within the component).")
			state.HasApp = true // Assume component provides App for now
		}
	}

	return nil // Success
}


// --- State Management Helpers (addString, addResource, find*, etc.) ---
// --- Property Handling Helpers (addKrbProperty, addSourceProperty, etc.) ---
// (Paste the helper functions from previous correct versions here)

// addString adds a string to the state's string table if unique, returning its 0-based index.
func (state *CompilerState) addString(text string) (uint8, error) {
	if text == "" {
		if len(state.Strings) == 0 { state.Strings = append(state.Strings, StringEntry{Text: "", Length: 0, Index: 0}) }
		return 0, nil
	}
	cleaned := trimQuotes(strings.TrimSpace(text))
	if cleaned == "" {
		if len(state.Strings) == 0 { state.Strings = append(state.Strings, StringEntry{Text: "", Length: 0, Index: 0}) }
		return 0, nil
	}
	for i := 1; i < len(state.Strings); i++ {
		if state.Strings[i].Text == cleaned { return state.Strings[i].Index, nil }
	}
	if len(state.Strings) >= MaxStrings { return 0, fmt.Errorf("maximum string limit (%d) exceeded", MaxStrings) }
	if len(state.Strings) == 0 { state.Strings = append(state.Strings, StringEntry{Text: "", Length: 0, Index: 0}) }
	idx := uint8(len(state.Strings))
	entry := StringEntry{ Text: cleaned, Length: len(cleaned), Index: idx, }
	state.Strings = append(state.Strings, entry)
	return idx, nil
}

// addResource adds a resource if unique, returns 0-based index.
func (state *CompilerState) addResource(resType uint8, pathStr string) (uint8, error) {
	pathIdx, err := state.addString(pathStr)
	if err != nil { return 0, fmt.Errorf("failed to add resource path '%s' to string table: %w", pathStr, err) }
    if pathIdx == 0 && len(strings.TrimSpace(pathStr)) > 0 { return 0, fmt.Errorf("failed to add non-empty resource path '%s' resulting in index 0", pathStr) }
	if pathIdx == 0 { return 0, fmt.Errorf("resource path cannot be empty") }
	format := ResFormatExternal
	for i := 0; i < len(state.Resources); i++ {
		if state.Resources[i].Type == resType && state.Resources[i].Format == format && state.Resources[i].DataStringIndex == pathIdx { return state.Resources[i].Index, nil }
	}
	if len(state.Resources) >= MaxResources { return 0, fmt.Errorf("maximum resource limit (%d) exceeded", MaxResources) }
	idx := uint8(len(state.Resources))
	entry := ResourceEntry{ Type: resType, NameIndex: pathIdx, Format: format, DataStringIndex: pathIdx, Index: idx, CalculatedSize: 4, }
	state.Resources = append(state.Resources, entry)
	state.HeaderFlags |= FlagHasResources
	return idx, nil
}

// findStyleByName finds a StyleEntry by its source name.
func (state *CompilerState) findStyleByName(name string) *StyleEntry {
	cleaned := trimQuotes(strings.TrimSpace(name))
	if cleaned == "" { return nil }
	for i := range state.Styles {
		if state.Styles[i].SourceName == cleaned { return &state.Styles[i] }
	}
	return nil
}

// findStyleIDByName finds a style ID (1-based) by its source name.
func (state *CompilerState) findStyleIDByName(name string) uint8 {
	style := state.findStyleByName(name)
	if style != nil { return style.ID }
	return 0
}

// findComponentDef finds a ComponentDefinition by name.
func (state *CompilerState) findComponentDef(name string) *ComponentDefinition {
	if name == "" { return nil }
	for i := range state.ComponentDefs {
		if state.ComponentDefs[i].Name == name { return &state.ComponentDefs[i] }
	}
	return nil
}


// addKrbProperty adds a resolved KRB property to an element.
func (el *Element) addKrbProperty(propID, valType uint8, data []byte) error {
	if len(el.KrbProperties) >= MaxProperties { return fmt.Errorf("L%d: maximum KRB properties (%d) exceeded for element '%s'", el.SourceLineNum, MaxProperties, el.SourceElementName) }
	if len(data) > 255 { return fmt.Errorf("L%d: property data size (%d) exceeds maximum (255) for element '%s', prop ID %d", el.SourceLineNum, len(data), el.SourceElementName, propID) }
	prop := KrbProperty{ PropertyID: propID, ValueType: valType, Size: uint8(len(data)), Value: data, }
	el.KrbProperties = append(el.KrbProperties, prop)
	el.PropertyCount = uint8(len(el.KrbProperties))
	return nil
}

// addKrbStringProperty adds a string property (looks up/adds string, stores index).
func (state *CompilerState) addKrbStringProperty(el *Element, propID uint8, valueStr string) error {
	idx, err := state.addString(valueStr)
	if err != nil { return fmt.Errorf("L%d: failed adding string for property %d: %w", el.SourceLineNum, propID, err) }
	return el.addKrbProperty(propID, ValTypeString, []byte{idx})
}

// addKrbResourceProperty adds a resource property (looks up/adds resource, stores index).
func (state *CompilerState) addKrbResourceProperty(el *Element, propID, resType uint8, pathStr string) error {
	idx, err := state.addResource(resType, pathStr)
	if err != nil { return fmt.Errorf("L%d: failed adding resource for property %d: %w", el.SourceLineNum, propID, err) }
	return el.addKrbProperty(propID, ValTypeResource, []byte{idx})
}

// addStyleKrbProperty adds a resolved KRB property to a style definition.
func (style *StyleEntry) addStyleKrbProperty(propID, valType uint8, data []byte) error {
	if len(style.Properties) >= MaxProperties { return fmt.Errorf("maximum KRB properties (%d) exceeded for style '%s'", MaxProperties, style.SourceName) }
    if len(data) > 255 { return fmt.Errorf("property data size (%d) exceeds maximum (255) for style '%s', prop ID %d", len(data), style.SourceName, propID) }
	prop := KrbProperty{ PropertyID: propID, ValueType: valType, Size: uint8(len(data)), Value: data, }
	style.Properties = append(style.Properties, prop)
	return nil
}

// addStyleKrbStringProperty adds a string property to a style definition.
func (state *CompilerState) addStyleKrbStringProperty(style *StyleEntry, propID uint8, valueStr string) error {
	idx, err := state.addString(valueStr)
	if err != nil { return fmt.Errorf("failed adding string for style property %d in style '%s': %w", propID, style.SourceName, err) }
	return style.addStyleKrbProperty(propID, ValTypeString, []byte{idx})
}

// getSourcePropertyValue retrieves the value string for a given key from an element's source properties.
func (el *Element) getSourcePropertyValue(key string) (string, bool) {
	for i := len(el.SourceProperties) - 1; i >= 0; i-- {
		if el.SourceProperties[i].Key == key { return el.SourceProperties[i].ValueStr, true }
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
	if len(el.SourceProperties) >= MaxProperties { return fmt.Errorf("L%d: maximum source properties (%d) exceeded for element '%s'", lineNum, MaxProperties, el.SourceElementName) }
	prop := SourceProperty{ Key: key, ValueStr: value, LineNum: lineNum, }
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
	if len(def.DefinitionRootProperties) >= MaxProperties { return fmt.Errorf("L%d: maximum root properties (%d) exceeded for component definition '%s'", lineNum, MaxProperties, def.Name) }
	prop := SourceProperty{ Key: key, ValueStr: value, LineNum: lineNum, }
	def.DefinitionRootProperties = append(def.DefinitionRootProperties, prop)
	return nil
}