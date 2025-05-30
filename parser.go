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
		originalLine := line
		trimmed := line
		indent := 0
		for _, r := range trimmed {
			if r == ' ' {
				indent++
			} else if r == '\t' {
				indent += 4
			} else {
				break
			}
		}
		trimmed = strings.TrimLeftFunc(trimmed, unicode.IsSpace)

		if strings.HasPrefix(trimmed, "#") {
			trimmed = ""
		} else {
			commentIndex := -1
			inQuotes := false
			for i, r := range trimmed {
				if r == '"' {
					inQuotes = !inQuotes
				}
				if r == '#' && !inQuotes {
					commentIndex = i
					break
				}
			}
			if commentIndex != -1 {
				trimmed = trimmed[:commentIndex]
			}
		}
		trimmed = strings.TrimRightFunc(trimmed, unicode.IsSpace)

		if trimmed == "" {
			continue
		}

		if len(originalLine) > MaxLineLength {
			log.Printf("L%d: Warning: Line exceeds MaxLineLength (%d)\n", currentLineNum, MaxLineLength)
		}

		currentIndent, currentContext, currentCtxType := getCurrentContext()

		// --- 1. Handle Block End ---
		if trimmed == "}" {
			if len(blockStack) == 0 {
				return fmt.Errorf("L%d: mismatched '}'", currentLineNum)
			}

			poppedEntry := blockStack[len(blockStack)-1]
			blockStack = blockStack[:len(blockStack)-1]

			if poppedEntry.Type == CtxEdgeInsetProperty {
				edgeState, ok := poppedEntry.Context.(*EdgeInsetParseState)
				if !ok || edgeState == nil {
					return fmt.Errorf("L%d: internal error: invalid context found when closing edge inset block", currentLineNum)
				}

				var addPropErr error
				baseKey := edgeState.ParentKey

				if edgeState.ParentCtxType == CtxElement {
					if parentEl, ok := edgeState.ParentCtx.(*Element); ok && parentEl != nil {
						if edgeState.Top != nil {
							addPropErr = parentEl.addSourceProperty(baseKey+"_top", *edgeState.Top, edgeState.StartLine)
							if addPropErr != nil {
								break
							}
						}
						if edgeState.Right != nil {
							addPropErr = parentEl.addSourceProperty(baseKey+"_right", *edgeState.Right, edgeState.StartLine)
							if addPropErr != nil {
								break
							}
						}
						if edgeState.Bottom != nil {
							addPropErr = parentEl.addSourceProperty(baseKey+"_bottom", *edgeState.Bottom, edgeState.StartLine)
							if addPropErr != nil {
								break
							}
						}
						if edgeState.Left != nil {
							addPropErr = parentEl.addSourceProperty(baseKey+"_left", *edgeState.Left, edgeState.StartLine)
							if addPropErr != nil {
								break
							}
						}
					} else {
						log.Printf("L%d: Warning: Invalid parent element context found when closing edge inset block for element.", currentLineNum)
					}
				} else if edgeState.ParentCtxType == CtxStyle {
					if parentStyle, ok := edgeState.ParentCtx.(*StyleEntry); ok && parentStyle != nil {
						if edgeState.Top != nil {
							addPropErr = parentStyle.addSourceProperty(baseKey+"_top", *edgeState.Top, edgeState.StartLine)
							if addPropErr != nil {
								break
							}
						}
						if edgeState.Right != nil {
							addPropErr = parentStyle.addSourceProperty(baseKey+"_right", *edgeState.Right, edgeState.StartLine)
							if addPropErr != nil {
								break
							}
						}
						if edgeState.Bottom != nil {
							addPropErr = parentStyle.addSourceProperty(baseKey+"_bottom", *edgeState.Bottom, edgeState.StartLine)
							if addPropErr != nil {
								break
							}
						}
						if edgeState.Left != nil {
							addPropErr = parentStyle.addSourceProperty(baseKey+"_left", *edgeState.Left, edgeState.StartLine)
							if addPropErr != nil {
								break
							}
						}
					} else {
						log.Printf("L%d: Warning: Invalid parent style context found when closing edge inset block for style.", currentLineNum)
					}
				} else {
					return fmt.Errorf("L%d: internal error: unexpected parent context type (%v) for edge inset block", edgeState.StartLine, edgeState.ParentCtxType)
				}

				if addPropErr != nil {
					return fmt.Errorf("L%d: error adding converted edge inset property for '%s': %w", edgeState.StartLine, baseKey, addPropErr)
				}
				if addPropErr == nil {
					// log.Printf("   Parsed Edge Inset Block for '%s' (L%d)\n", edgeState.ParentKey, edgeState.StartLine)
				}
			}
			continue
		}

		// --- 2. Check for Block Start ---
		blockOpened := strings.HasSuffix(trimmed, "{")
		blockContent := trimmed
		if blockOpened {
			braceIndex := strings.LastIndex(trimmed, "{")
			if braceIndex != -1 {
				blockContent = strings.TrimSpace(trimmed[:braceIndex])
			} else {
				blockContent = strings.TrimSpace(trimmed[:len(trimmed)-1]) // Should not happen if HasSuffix is true
			}
		}

		isDefine := strings.HasPrefix(blockContent, "Define ") && blockOpened
		isStyle := strings.HasPrefix(blockContent, "style ") && blockOpened
		isProperties := blockContent == "Properties" && blockOpened
		firstWordIsUpper := false
		fields := strings.Fields(blockContent)
		if len(fields) > 0 && len(fields[0]) > 0 {
			firstWordIsUpper = unicode.IsUpper(rune(fields[0][0]))
		}
		isElement := firstWordIsUpper && blockOpened

		// --- 3. Process Potential Block Start Lines ---
		if blockOpened {
			// --- 3a. Define Block ---
			if isDefine {
				if currentCtxType != CtxNone {
					return fmt.Errorf("L%d: 'Define' must be at the top level", currentLineNum)
				}
				parts := strings.Fields(blockContent)
				if len(parts) == 2 {
					name := parts[1]
					if state.findComponentDef(name) != nil {
						return fmt.Errorf("L%d: component '%s' redefined", currentLineNum, name)
					}
					if len(state.ComponentDefs) >= MaxComponentDefs {
						return fmt.Errorf("L%d: maximum component definitions (%d) exceeded", currentLineNum, MaxComponentDefs)
					}
					def := ComponentDefinition{
						Name:                       name,
						DefinitionStartLine:        currentLineNum,
						Properties:                 make([]ComponentPropertyDef, 0, 4),
						DefinitionRootElementIndex: -1, // Initialize
					}
					state.ComponentDefs = append(state.ComponentDefs, def)
					state.HeaderFlags |= FlagHasComponentDefs // Set the flag

					currentComponentDef := &state.ComponentDefs[len(state.ComponentDefs)-1]
					if len(blockStack) >= MaxBlockDepth {
						return fmt.Errorf("L%d: max block depth exceeded", currentLineNum)
					}
					blockStack = append(blockStack, BlockStackEntry{indent, currentComponentDef, CtxComponentDef})
					log.Printf("   Def: %s\n", name)
				} else {
					return fmt.Errorf("L%d: invalid Define syntax: '%s'", currentLineNum, blockContent)
				}
				continue
			}

			// --- 3b. Style Block ---
			if isStyle {
				if currentCtxType != CtxNone {
					return fmt.Errorf("L%d: 'style' must be at the top level (Current context: %v)", currentLineNum, currentCtxType)
				}
				parts := strings.SplitN(blockContent, "\"", 3)
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
						ID: styleID, SourceName: name, NameIndex: nameIdx,
						Properties: make([]KrbProperty, 0, 4), SourceProperties: make([]SourceProperty, 0, 8),
						CalculatedSize: 3, ExtendsStyleNames: make([]string, 0, 1),
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
				continue
			}

			// --- 3c. Properties Block (inside Define) ---
			if isProperties {
				if currentCtxType != CtxComponentDef {
					return fmt.Errorf("L%d: 'Properties' block must be directly inside a 'Define' block", currentLineNum)
				}
				def, ok := currentContext.(*ComponentDefinition)
				if !ok {
					return fmt.Errorf("L%d: internal error: context for Properties block is not *ComponentDefinition", currentLineNum)
				}

				if def.DefinitionRootElementIndex != -1 {
					return fmt.Errorf("L%d: 'Properties' block must come before the root element definition within 'Define %s'", currentLineNum, def.Name)
				}
				for _, entry := range blockStack { // Check for duplicate 'Properties' block in current CtxComponentDef
					if entry.Type == CtxProperties && entry.Context == currentContext {
						return fmt.Errorf("L%d: multiple 'Properties' blocks are invalid within 'Define %s'", currentLineNum, def.Name)
					}
				}
				if len(blockStack) >= MaxBlockDepth {
					return fmt.Errorf("L%d: max block depth exceeded", currentLineNum)
				}
				blockStack = append(blockStack, BlockStackEntry{indent, currentContext, CtxProperties})
				continue
			}

			// --- 3d. Element/Component Block Start ---
			if isElement {
				elementName := strings.Fields(blockContent)[0]
				parentIndex := -1
				var parentElement *Element
				var componentDefContext *ComponentDefinition // Used if this element is a definition root
				isDefinitionRoot := false

				switch currentCtxType {
				case CtxNone: // Top-level element (App or component usage)
				case CtxElement: // Nested inside another element
					parentElement = currentContext.(*Element)
					parentIndex = parentElement.SelfIndex
				case CtxComponentDef: // This element is the root of a component definition
					def := currentContext.(*ComponentDefinition)
					if def.DefinitionRootElementIndex != -1 {
						return fmt.Errorf("L%d: multiple root elements defined for 'Define %s'. Previous was at index %d.", currentLineNum, def.Name, def.DefinitionRootElementIndex)
					}
					isDefinitionRoot = true
					componentDefContext = def // Keep track of the definition this element belongs to
					parentIndex = -1          // Definition roots have no element parent in the tree structure
					log.Printf("      Def root: %s for Define %s\n", elementName, def.Name)
				case CtxProperties, CtxStyle, CtxEdgeInsetProperty:
					return fmt.Errorf("L%d: cannot define element '%s' directly inside a '%v' block", currentLineNum, elementName, currentCtxType)
				default:
					return fmt.Errorf("L%d: internal error: unexpected context type %v when starting element '%s'", currentLineNum, currentCtxType, elementName)
				}

				if len(state.Elements) >= MaxElements {
					return fmt.Errorf("L%d: maximum elements (%d) exceeded", currentLineNum, MaxElements)
				}

				elementIndex := len(state.Elements)
				el := Element{
					SelfIndex: elementIndex, ParentIndex: parentIndex, SourceElementName: elementName, SourceLineNum: currentLineNum,
					CalculatedSize: KRBElementHeaderSize, SourceProperties: make([]SourceProperty, 0, 8),
					KrbProperties: make([]KrbProperty, 0, 8), KrbCustomProperties: make([]KrbCustomProperty, 0, 2),
					KrbEvents: make([]KrbEvent, 0, 2), SourceChildrenIndices: make([]int, 0, 4),
					Children: make([]*Element, 0, 4),
					IsDefinitionRoot: isDefinitionRoot, // 'isDefinitionRoot' is true ONLY if currentCtxType == CtxComponentDef
				}

				if parentElement != nil && parentElement.IsDefinitionRoot {
					el.IsDefinitionRoot = true
				}

				if compDef := state.findComponentDef(elementName); compDef != nil { // Component Usage
					if isDefinitionRoot {
						return fmt.Errorf("L%d: cannot use component '%s' as the root element definition for 'Define %s'. Root must be a standard element type.", currentLineNum, elementName, componentDefContext.Name)
					}
					el.Type = ElemTypeInternalComponentUsage // Placeholder type, resolved later
					el.ComponentDef = compDef
					el.IsComponentInstance = true
				} else { // Standard Element or root of a Definition
					el.Type = getElementTypeFromName(elementName)
					if el.Type == ElemTypeUnknown { // Potentially a custom element type
						el.Type = ElemTypeCustomBase // Default to custom if not standard
						nameIdx, err := state.addString(elementName)
						if err != nil {
							return fmt.Errorf("L%d: failed adding custom element name '%s': %w", currentLineNum, elementName, err)
						}
						el.IDStringIndex = nameIdx // Store its name as ID for runtime
						log.Printf("L%d: Warn: Unknown element type '%s', treating as custom (type 0x%X with name index %d)\n", currentLineNum, elementName, el.Type, nameIdx)
					}
					if !isDefinitionRoot { // Root checks only for main UI tree elements
						if el.Type == ElemTypeApp {
							if state.HasApp || parentElement != nil {
								return fmt.Errorf("L%d: 'App' element must be the single root element", currentLineNum)
							}
							state.HasApp = true
							state.HeaderFlags |= FlagHasApp
						} else if parentElement == nil && !state.HasApp && !el.IsComponentInstance {
							return fmt.Errorf("L%d: root element must be 'App' or a component usage, found standard element '%s'", currentLineNum, elementName)
						}
					}
				}

				state.Elements = append(state.Elements, el)
				currentElement := &state.Elements[elementIndex]

				if parentElement != nil { // Add to parent's children if normally nested
					if len(parentElement.SourceChildrenIndices) >= MaxChildren {
						return fmt.Errorf("L%d: max children (%d) for parent '%s'", currentLineNum, MaxChildren, parentElement.SourceElementName)
					}
					parentElement.SourceChildrenIndices = append(parentElement.SourceChildrenIndices, currentElement.SelfIndex)
				}
				if isDefinitionRoot && componentDefContext != nil { // Link definition to its root element
					componentDefContext.DefinitionRootElementIndex = currentElement.SelfIndex
				}

				if len(blockStack) >= MaxBlockDepth {
					return fmt.Errorf("L%d: max block depth exceeded", currentLineNum)
				}
				blockStack = append(blockStack, BlockStackEntry{indent, currentElement, CtxElement})
				continue
			}

			// --- 3e. Edge Inset Block Start ---
			parts := strings.SplitN(blockContent, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				if (key == "padding" || key == "margin") && val == "" { // Expecting "padding: {"
					if currentCtxType != CtxElement && currentCtxType != CtxStyle {
						return fmt.Errorf("L%d: '%s: {' block must be inside an Element or Style block (current: %v)", currentLineNum, key, currentCtxType)
					}
					if len(blockStack) >= MaxBlockDepth {
						return fmt.Errorf("L%d: max block depth for '%s: {'", currentLineNum, key)
					}
					edgeState := &EdgeInsetParseState{
						ParentKey: key, ParentCtx: currentContext, ParentCtxType: currentCtxType,
						Indent: indent, StartLine: currentLineNum,
					}
					blockStack = append(blockStack, BlockStackEntry{Indent: indent, Context: edgeState, Type: CtxEdgeInsetProperty})
					// log.Printf("   Start Edge Inset Block for '%s'\n", key)
					continue
				}
			}
			return fmt.Errorf("L%d: invalid block start syntax: '%s'", currentLineNum, trimmed)
		}

		// --- 4. Process Non-Block-Starting Lines (Properties) ---
		if !(len(blockStack) > 0 && indent > currentIndent) { // Must be indented within a block
			msg := "unexpected syntax or indentation"
			if len(blockStack) == 0 {
				msg = "unexpected syntax at top level (expected block definition)"
			}
			return fmt.Errorf("L%d: %s: '%s'", currentLineNum, msg, trimmed)
		}

		parts := strings.SplitN(trimmed, ":", 2)
		keyPart := ""
		if len(parts) > 0 {
			keyPart = strings.TrimSpace(parts[0])
		}
		isPropertyLike := len(parts) == 2 && len(keyPart) > 0 && !strings.ContainsAny(keyPart, "{}")

		contextType := currentCtxType
		contextObject := currentContext

		switch contextType {
		case CtxProperties: // Inside Define -> Properties { key: Type [= Default] }
			if !isPropertyLike {
				return fmt.Errorf("L%d: invalid syntax in Properties (expected 'key: Type [= Default]'): '%s'", currentLineNum, trimmed)
			}
			key := keyPart
			valueStr := strings.TrimSpace(parts[1])
			parentComponentDef, ok := contextObject.(*ComponentDefinition)
			if !ok {
				return fmt.Errorf("L%d: internal error: CtxProperties not *ComponentDefinition", currentLineNum)
			}

			valParts := strings.SplitN(valueStr, "=", 2)
			propTypeStr := strings.TrimSpace(valParts[0])
			propDefault := ""
			if len(valParts) == 2 {
				propDefault = strings.TrimSpace(valParts[1])
			}
			if len(parentComponentDef.Properties) >= MaxProperties {
				return fmt.Errorf("L%d: max props (%d) for component '%s'", currentLineNum, MaxProperties, parentComponentDef.Name)
			}

			pd := ComponentPropertyDef{Name: key, DefaultValueStr: propDefault}
			switch propTypeStr {
			case "String":
				pd.ValueTypeHint = ValTypeString
			case "Int":
				pd.ValueTypeHint = ValTypeInt
			case "Bool":
				pd.ValueTypeHint = ValTypeBool
			case "Color":
				pd.ValueTypeHint = ValTypeColor
			case "StyleID":
				pd.ValueTypeHint = ValTypeStyleID
			case "Resource":
				pd.ValueTypeHint = ValTypeResource
			case "Float":
				pd.ValueTypeHint = ValTypeFloat
			default:
				if strings.HasPrefix(propTypeStr, "Enum(") && strings.HasSuffix(propTypeStr, ")") {
					pd.ValueTypeHint = ValTypeEnum
				} else {
					log.Printf("L%d: Warn: Unknown property type '%s' for '%s'. Treating as custom hint.", currentLineNum, propTypeStr, key)
					pd.ValueTypeHint = ValTypeCustom
				}
			}
			parentComponentDef.Properties = append(parentComponentDef.Properties, pd)
			continue

		case CtxEdgeInsetProperty: // Inside padding: { top: N }
			if !isPropertyLike {
				if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
					log.Printf("L%d: Warn: Invalid syntax in edge inset block: '%s'. Ignored.", currentLineNum, trimmed)
				}
				continue
			}
			key := keyPart
			valueStr := strings.TrimSpace(parts[1])
			edgeState, ok := contextObject.(*EdgeInsetParseState)
			if !ok {
				return fmt.Errorf("L%d: internal error: CtxEdgeInsetProperty not *EdgeInsetParseState", currentLineNum)
			}

			switch key {
			case "top":
				if edgeState.Top != nil {
					log.Printf("L%d: Warn: duplicate 'top' in '%s'.", currentLineNum, edgeState.ParentKey)
				}
				edgeState.Top = &valueStr
			case "right":
				if edgeState.Right != nil {
					log.Printf("L%d: Warn: duplicate 'right' in '%s'.", currentLineNum, edgeState.ParentKey)
				}
				edgeState.Right = &valueStr
			case "bottom":
				if edgeState.Bottom != nil {
					log.Printf("L%d: Warn: duplicate 'bottom' in '%s'.", currentLineNum, edgeState.ParentKey)
				}
				edgeState.Bottom = &valueStr
			case "left":
				if edgeState.Left != nil {
					log.Printf("L%d: Warn: duplicate 'left' in '%s'.", currentLineNum, edgeState.ParentKey)
				}
				edgeState.Left = &valueStr
			case "all":
				if edgeState.Top != nil || edgeState.Right != nil || edgeState.Bottom != nil || edgeState.Left != nil {
					log.Printf("L%d: Warn: 'all' after individual sides in '%s'. Ignored.", currentLineNum, edgeState.ParentKey)
				} else {
					edgeState.Top = &valueStr
					edgeState.Right = &valueStr
					edgeState.Bottom = &valueStr
					edgeState.Left = &valueStr
				}
			case "horizontal":
				if edgeState.Left != nil || edgeState.Right != nil {
					log.Printf("L%d: Warn: 'horizontal' after left/right in '%s'. Ignored.", currentLineNum, edgeState.ParentKey)
				} else {
					edgeState.Left = &valueStr
					edgeState.Right = &valueStr
				}
			case "vertical":
				if edgeState.Top != nil || edgeState.Bottom != nil {
					log.Printf("L%d: Warn: 'vertical' after top/bottom in '%s'. Ignored.", currentLineNum, edgeState.ParentKey)
				} else {
					edgeState.Top = &valueStr
					edgeState.Bottom = &valueStr
				}
			default:
				log.Printf("L%d: Warn: Unexpected key '%s' in '%s: {}'. Ignored.", currentLineNum, key, edgeState.ParentKey)
			}
			continue

		case CtxStyle: // Inside style "name" { key: value }
			if !isPropertyLike {
				return fmt.Errorf("L%d: invalid property syntax in Style block: '%s'", currentLineNum, trimmed)
			}
			key := keyPart
			valueStrRaw := strings.TrimSpace(parts[1])
			parentStyle, ok := contextObject.(*StyleEntry)
			if !ok {
				return fmt.Errorf("L%d: internal error: CtxStyle not *StyleEntry", currentLineNum)
			}

			if key == "extends" {
				if len(parentStyle.ExtendsStyleNames) > 0 {
					return fmt.Errorf("L%d: style '%s' 'extends' multiple times", currentLineNum, parentStyle.SourceName)
				}
				if len(parentStyle.SourceProperties) > 0 {
					log.Printf("L%d: Warn: 'extends' should be first in style '%s'.", currentLineNum, parentStyle.SourceName)
				}

				trimmedValue := strings.TrimSpace(valueStrRaw)
				var baseNames []string
				if strings.HasPrefix(trimmedValue, "[") && strings.HasSuffix(trimmedValue, "]") { // List of styles
					content := strings.TrimSpace(trimmedValue[1 : len(trimmedValue)-1])
					if content != "" {
						for _, pn := range strings.Split(content, ",") {
							cn, wq := cleanAndQuoteValue(pn)
							if !wq || cn == "" {
								return fmt.Errorf("L%d: invalid style name '%s' in 'extends' for '%s'", currentLineNum, pn, parentStyle.SourceName)
							}
							if cn == parentStyle.SourceName {
								return fmt.Errorf("L%d: style '%s' cannot extend itself", currentLineNum, parentStyle.SourceName)
							}
							baseNames = append(baseNames, cn)
						}
						if len(baseNames) == 0 {
							return fmt.Errorf("L%d: empty 'extends' list for '%s'", currentLineNum, parentStyle.SourceName)
						}
					}
				} else { // Single style name
					bn, wq := cleanAndQuoteValue(trimmedValue)
					if !wq || bn == "" {
						return fmt.Errorf("L%d: 'extends' needs quoted name or list for '%s', got '%s'", currentLineNum, parentStyle.SourceName, trimmedValue)
					}
					if bn == parentStyle.SourceName {
						return fmt.Errorf("L%d: style '%s' cannot extend itself", currentLineNum, parentStyle.SourceName)
					}
					baseNames = []string{bn}
				}
				parentStyle.ExtendsStyleNames = baseNames
				// log.Printf("   Parsed Style Extends: %s -> %v\n", parentStyle.SourceName, baseNames)
			} else {
				if err := parentStyle.addSourceProperty(key, valueStrRaw, currentLineNum); err != nil {
					return fmt.Errorf("L%d: %w", currentLineNum, err)
				}
			}
			continue

		case CtxElement: // Inside Element { key: value } (applies to main tree elements AND template definition roots)
			if !isPropertyLike {
				if fields := strings.Fields(trimmed); len(fields) > 0 && unicode.IsUpper(rune(fields[0][0])) {
					return fmt.Errorf("L%d: potential nested element '%s' needs its own block '{'", currentLineNum, fields[0])
				}
				elName := "unknown element"
				if elCtx, ok := contextObject.(*Element); ok {
					elName = elCtx.SourceElementName
				}
				return fmt.Errorf("L%d: invalid property syntax in Element '%s' (expected 'key: value'): '%s'", currentLineNum, elName, trimmed)
			}
			key := keyPart
			valueStr := strings.TrimSpace(parts[1])
			currentElement, ok := contextObject.(*Element)
			if !ok {
				return fmt.Errorf("L%d: internal error: CtxElement not *Element", currentLineNum)
			}

			if err := currentElement.addSourceProperty(key, valueStr, currentLineNum); err != nil {
				return err
			}
			continue

		case CtxComponentDef: // Properties are not allowed directly under "Define Name {", only "Properties {}" or the root element
			log.Printf("L%d: Warning: Property '%s' directly under Define block. Ignored. Use 'Properties {}' or define on root element.", currentLineNum, keyPart)
			continue

		default: // Should not happen if block stack logic is correct
			if contextType == CtxNone {
				continue
			} // Ignore indented lines at top level if they slip through somehow
			return fmt.Errorf("L%d: internal error: unexpected context %v for property line '%s'", currentLineNum, contextType, trimmed)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading source buffer: %w", err)
	}

	// --- Final Checks ---
	if len(blockStack) != 0 {
		// ... (Your existing unclosed block error reporting is good) ...
		lastEntry := blockStack[len(blockStack)-1]
		contextStr := fmt.Sprintf("%v", lastEntry.Type)
		// Add more specific context if possible
		switch lastEntry.Type {
		case CtxElement:
			if el, ok := lastEntry.Context.(*Element); ok {
				contextStr = fmt.Sprintf("Element '%s' L%d", el.SourceElementName, el.SourceLineNum)
			}
		case CtxStyle:
			if st, ok := lastEntry.Context.(*StyleEntry); ok {
				contextStr = fmt.Sprintf("Style '%s'", st.SourceName)
			}
		case CtxComponentDef:
			if def, ok := lastEntry.Context.(*ComponentDefinition); ok {
				contextStr = fmt.Sprintf("Define '%s' L%d", def.Name, def.DefinitionStartLine)
			}
		case CtxProperties:
			contextStr = "Properties block" // Could find parent Define name if needed
		case CtxEdgeInsetProperty:
			if es, ok := lastEntry.Context.(*EdgeInsetParseState); ok {
				contextStr = fmt.Sprintf("'%s: {}' L%d", es.ParentKey, es.StartLine)
			}
		}
		return fmt.Errorf("unclosed block at end of file (last open: %s)", contextStr)
	}

	// Root element validation
	rootElementIndex := -1
	for i, el := range state.Elements {
		if el.ParentIndex == -1 && !el.IsDefinitionRoot { // Is a main UI tree root element
			rootElementIndex = i
			break
		}
	}

	if rootElementIndex == -1 { // No main UI tree root found
		if len(state.Elements) == 0 && len(state.ComponentDefs) == 0 {
			return errors.New("no content found: define 'App' or components")
		}
		if len(state.Elements) > 0 { // Elements exist, but all are template parts
			onlyTemplateParts := true
			for _, el := range state.Elements {
				if !el.IsDefinitionRoot {
					onlyTemplateParts = false
					break
				}
			}
			if onlyTemplateParts && len(state.ComponentDefs) > 0 {
				log.Println("Info: Only component definitions found. No main 'App' element or root component instance.")
				// This is valid for a component library file.
			} else {
				// This case implies elements that are not definition roots but also not identified as main tree roots
				return errors.New("internal error: elements present but no main UI tree root identified")
			}
		} else if len(state.ComponentDefs) > 0 { // Only defs, no elements at all
			log.Println("Info: Only component definitions found. No main 'App' element or root component instance.")
		} else { // Should be caught by the first condition of this block
			return errors.New("no root element defined (must be 'App' or a component instance in the main UI tree)")
		}

	} else { // Main UI tree root was found
		rootElement := state.Elements[rootElementIndex]
		if rootElement.Type != ElemTypeApp && !rootElement.IsComponentInstance {
			return fmt.Errorf("main UI tree root element must be 'App' or a component instance, but found '%s' (type 0x%X) at L%d", rootElement.SourceElementName, rootElement.Type, rootElement.SourceLineNum)
		}
		if rootElement.Type == ElemTypeApp && !state.HasApp { // Ensure HasApp flag consistency
			state.HasApp = true
			state.HeaderFlags |= FlagHasApp
		}
		if rootElement.IsComponentInstance && !state.HasApp { // Root component instance implies an App wrapper
			log.Println("Info: Root element is a component instance. Assuming 'App' container behavior.")
			state.HasApp = true // Mark as having an effective App root conceptually
		}
	}

	// Check component definitions
	for i := range state.ComponentDefs {
		if state.ComponentDefs[i].DefinitionRootElementIndex == -1 {
			return fmt.Errorf("component definition 'Define %s' (L%d) is missing its root element template (e.g., Container { ... })", state.ComponentDefs[i].Name, state.ComponentDefs[i].DefinitionStartLine)
		}
	}

	return nil
}

// --- Helper functions previously defined (addString, addResource, findComponentDef, findParentContext, etc.) ---
// --- Element/Style addSourceProperty, addKrbProperty, etc. ---
// These should remain as they were, as they operate on the correct structs.

// addString adds a string to the state's string table if unique, returning its 0-based index.
func (state *CompilerState) addString(text string) (uint8, error) {
	if text == "" {
		if len(state.Strings) == 0 {
			state.Strings = append(state.Strings, StringEntry{Text: "", Length: 0, Index: 0})
		}
		return 0, nil
	}
	cleaned := trimQuotes(strings.TrimSpace(text))
	if cleaned == "" { // After cleaning, it might become empty
		if len(state.Strings) == 0 {
			state.Strings = append(state.Strings, StringEntry{Text: "", Length: 0, Index: 0})
		}
		return 0, nil
	}
	// Search for existing non-empty strings (index 0 is reserved for empty)
	for i := 1; i < len(state.Strings); i++ {
		if state.Strings[i].Text == cleaned {
			return state.Strings[i].Index, nil
		}
	}
	if len(state.Strings) >= MaxStrings {
		return 0, fmt.Errorf("maximum string limit (%d) exceeded when adding '%s'", MaxStrings, cleaned)
	}
	// Ensure index 0 exists if adding the first non-empty string
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
	pathIdx, err := state.addString(pathStr) // pathStr will be cleaned by addString
	if err != nil {
		return 0, fmt.Errorf("failed to add resource path '%s' to string table: %w", pathStr, err)
	}
	// addString returns 0 for an empty string. A resource path cannot be meaningfully empty.
	if pathIdx == 0 && strings.TrimSpace(pathStr) != "" {
		// This case means addString returned 0, but the original pathStr was not just whitespace.
		// This could happen if addString had an issue or if the cleaned path became "" but original wasn't.
		// For safety, ensure that if pathStr was non-empty, we get a non-zero index.
		// However, addString logic was updated to handle this. This check is more of a safeguard.
		return 0, fmt.Errorf("resource path '%s' resolved to empty string index", pathStr)
	}
	if pathIdx == 0 && strings.TrimSpace(pathStr) == "" { // Explicitly disallow empty or whitespace-only paths
		return 0, fmt.Errorf("resource path cannot be empty or whitespace only")
	}

	format := ResFormatExternal // Currently only external resources are supported
	for i := 0; i < len(state.Resources); i++ {
		if state.Resources[i].Type == resType && state.Resources[i].Format == format && state.Resources[i].DataStringIndex == pathIdx {
			return state.Resources[i].Index, nil
		}
	}
	if len(state.Resources) >= MaxResources {
		return 0, fmt.Errorf("maximum resource limit (%d) exceeded", MaxResources)
	}
	idx := uint8(len(state.Resources))
	entry := ResourceEntry{Type: resType, NameIndex: pathIdx, Format: format, DataStringIndex: pathIdx, Index: idx, CalculatedSize: 4} // External resource entry is 4 bytes
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
	if len(data) > 255 { // KRB Property.Size is 1 byte
		return fmt.Errorf("L%d: property data size (%d) exceeds maximum (255) for element '%s', prop ID 0x%X", el.SourceLineNum, len(data), el.SourceElementName, propID)
	}
	prop := KrbProperty{PropertyID: propID, ValueType: valType, Size: uint8(len(data)), Value: data}
	el.KrbProperties = append(el.KrbProperties, prop)
	// el.PropertyCount is finalized in the resolver/writer after all properties are added.
	return nil
}

// addKrbStringProperty adds a string property (looks up/adds string, stores index).
func (state *CompilerState) addKrbStringProperty(el *Element, propID uint8, valueStr string) error {
	idx, err := state.addString(valueStr)
	if err != nil {
		return fmt.Errorf("L%d: failed adding string for property 0x%X ('%s'): %w", el.SourceLineNum, propID, valueStr, err)
	}
	return el.addKrbProperty(propID, ValTypeString, []byte{idx})
}

// addKrbResourceProperty adds a resource property (looks up/adds resource, stores index).
func (state *CompilerState) addKrbResourceProperty(el *Element, propID, resType uint8, pathStr string) error {
	idx, err := state.addResource(resType, pathStr)
	if err != nil {
		return fmt.Errorf("L%d: failed adding resource for property 0x%X ('%s'): %w", el.SourceLineNum, propID, pathStr, err)
	}
	return el.addKrbProperty(propID, ValTypeResource, []byte{idx})
}

// addSourceProperty adds a raw key-value pair from the .kry source to an element. Overwrites if key exists.
func (el *Element) addSourceProperty(key, value string, lineNum int) error {
	for i := range el.SourceProperties { // Check if property key already exists
		if el.SourceProperties[i].Key == key {
			el.SourceProperties[i].ValueStr = value // Update value and line number
			el.SourceProperties[i].LineNum = lineNum
			return nil
		}
	}
	// Add new property if limit not reached
	if len(el.SourceProperties) >= MaxProperties {
		return fmt.Errorf("L%d: maximum source properties (%d) exceeded for element '%s' when adding '%s'", lineNum, MaxProperties, el.SourceElementName, key)
	}
	prop := SourceProperty{Key: key, ValueStr: value, LineNum: lineNum}
	el.SourceProperties = append(el.SourceProperties, prop)
	return nil
}
