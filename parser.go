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
	blockStack := make([]BlockStackEntry, 0, MaxBlockDepth)

	getCurrentContext := func() (indent int, context interface{}, ctxType BlockContextType) {
		if len(blockStack) > 0 {
			top := blockStack[len(blockStack)-1]
			return top.Indent, top.Context, top.Type
		}
		return -1, nil, CtxNone
	}

	for scanner.Scan() {
		currentLineNum++
		state.CurrentLineNum = currentLineNum
		rawLine := scanner.Text()

		trimmedForIndent, indent := trimLeadingWhitespaceAndGetIndent(rawLine)
		trimmedLine := stripCommentAndTrim(trimmedForIndent)

		if trimmedLine == "" {
			continue
		}

		if len(rawLine) > MaxLineLength {
			log.Printf("L%d: Warning: Line exceeds MaxLineLength (%d)\n", currentLineNum, MaxLineLength)
		}

		_, currentContext, currentCtxType := getCurrentContext()

		// --- 1. Handle Block End ---
		if trimmedLine == "}" {
			if len(blockStack) == 0 {
				return fmt.Errorf("L%d: mismatched '}'", currentLineNum)
			}

			poppedEntry := blockStack[len(blockStack)-1]
			// We defer popping from blockStack until after potential processing
			// to ensure context is still available if needed.

			if poppedEntry.Type == CtxEdgeInsetProperty {
				edgeState, ok := poppedEntry.Context.(*EdgeInsetParseState)
				if !ok || edgeState == nil {
					return fmt.Errorf("L%d: internal error: invalid context found when closing edge inset block", currentLineNum)
				}

				var addPropErr error
				baseKey := edgeState.ParentKey

				processEdgeProp := func(keySuffix string, value *string) error {
					if value != nil {
						if edgeState.ParentCtxType == CtxElement {
							if parentEl, ok := edgeState.ParentCtx.(*Element); ok && parentEl != nil {
								return parentEl.addSourceProperty(baseKey+keySuffix, *value, edgeState.StartLine)
							}
							log.Printf("L%d: Warning: Invalid parent element context for edge inset prop '%s%s'.", currentLineNum, baseKey, keySuffix)
							return nil // Non-fatal, just log
						} else if edgeState.ParentCtxType == CtxStyle {
							if parentStyle, ok := edgeState.ParentCtx.(*StyleEntry); ok && parentStyle != nil {
								return parentStyle.addSourceProperty(baseKey+keySuffix, *value, edgeState.StartLine)
							}
							log.Printf("L%d: Warning: Invalid parent style context for edge inset prop '%s%s'.", currentLineNum, baseKey, keySuffix)
							return nil // Non-fatal, just log
						} else {
							return fmt.Errorf("internal error: unexpected parent context type (%v) for edge inset block at L%d", edgeState.ParentCtxType, edgeState.StartLine)
						}
					}
					return nil
				}

				addPropErr = processEdgeProp("_top", edgeState.Top)
				if addPropErr != nil {
					return fmt.Errorf("L%d: error adding converted edge inset property for '%s_top': %w", edgeState.StartLine, baseKey, addPropErr)
				}
				addPropErr = processEdgeProp("_right", edgeState.Right)
				if addPropErr != nil {
					return fmt.Errorf("L%d: error adding converted edge inset property for '%s_right': %w", edgeState.StartLine, baseKey, addPropErr)
				}
				addPropErr = processEdgeProp("_bottom", edgeState.Bottom)
				if addPropErr != nil {
					return fmt.Errorf("L%d: error adding converted edge inset property for '%s_bottom': %w", edgeState.StartLine, baseKey, addPropErr)
				}
				addPropErr = processEdgeProp("_left", edgeState.Left)
				if addPropErr != nil {
					return fmt.Errorf("L%d: error adding converted edge inset property for '%s_left': %w", edgeState.StartLine, baseKey, addPropErr)
				}
			}
			blockStack = blockStack[:len(blockStack)-1] // Pop the stack
			continue
		}

		// --- 2. Process Line: Block Start or Properties ---
		firstWord, restOfLineAfterWord := splitFirstWord(trimmedLine)

		// --- 2a. Define Block ---
		if firstWord == "Define" && strings.HasSuffix(restOfLineAfterWord, "{") {
			if currentCtxType != CtxNone {
				return fmt.Errorf("L%d: 'Define' must be at the top level", currentLineNum)
			}
			parts := strings.Fields(strings.TrimSuffix(restOfLineAfterWord, "{"))
			if len(parts) == 1 {
				name := parts[0]
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
					DefinitionRootElementIndex: -1,
				}
				state.ComponentDefs = append(state.ComponentDefs, def)
				state.HeaderFlags |= FlagHasComponentDefs
				currentComponentDef := &state.ComponentDefs[len(state.ComponentDefs)-1]
				if len(blockStack) >= MaxBlockDepth {
					return fmt.Errorf("L%d: max block depth exceeded", currentLineNum)
				}
				blockStack = append(blockStack, BlockStackEntry{indent, currentComponentDef, CtxComponentDef})
				log.Printf("   Def: %s\n", name)
			} else {
				return fmt.Errorf("L%d: invalid Define syntax: '%s'", currentLineNum, trimmedLine)
			}
			continue
		}

		// --- 2b. Style Block ---
		if firstWord == "style" && strings.Contains(restOfLineAfterWord, "\"") && strings.HasSuffix(restOfLineAfterWord, "{") {
			if currentCtxType != CtxNone {
				return fmt.Errorf("L%d: 'style' must be at the top level", currentLineNum)
			}
			nameContent := strings.TrimSpace(strings.TrimSuffix(restOfLineAfterWord, "{"))
			if strings.HasPrefix(nameContent, "\"") && strings.HasSuffix(nameContent, "\"") && len(nameContent) > 1 {
				name := nameContent[1 : len(nameContent)-1]
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
					ID:                styleID,
					SourceName:        name,
					NameIndex:         nameIdx,
					Properties:        make([]KrbProperty, 0, 4),
					SourceProperties:  make([]SourceProperty, 0, 8),
					CalculatedSize:    3, // Base size for StyleHeader (ID, NameIdx, PropCount)
					ExtendsStyleNames: make([]string, 0, 1),
				}
				state.Styles = append(state.Styles, styleEntry)
				currentStyle := &state.Styles[len(state.Styles)-1]
				if len(blockStack) >= MaxBlockDepth {
					return fmt.Errorf("L%d: max block depth exceeded", currentLineNum)
				}
				blockStack = append(blockStack, BlockStackEntry{indent, currentStyle, CtxStyle})
				state.HeaderFlags |= FlagHasStyles
			} else {
				return fmt.Errorf("L%d: invalid style syntax: '%s', use 'style \"name\" {'", currentLineNum, trimmedLine)
			}
			continue
		}

		// --- 2c. Properties Block (inside Define) ---
		if (firstWord == "Properties" && restOfLineAfterWord == "{") || trimmedLine == "Properties {" {
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
			for _, entry := range blockStack {
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

		// --- 2d. Element/Component Block Start or Inline Properties ---
		isPotentialElementStart := len(firstWord) > 0 && unicode.IsUpper(rune(firstWord[0])) && strings.Contains(restOfLineAfterWord, "{")

		if isPotentialElementStart {
			elementName := firstWord
			contentAfterElementName := restOfLineAfterWord

			parentIndex := -1
			var parentElement *Element
			var componentDefContext *ComponentDefinition
			isDefinitionRoot := false

			switch currentCtxType {
			case CtxNone:
			case CtxElement:
				parentElement = currentContext.(*Element)
				parentIndex = parentElement.SelfIndex
			case CtxComponentDef:
				def := currentContext.(*ComponentDefinition)
				if def.DefinitionRootElementIndex != -1 {
					return fmt.Errorf("L%d: multiple root elements defined for 'Define %s'. Previous was at index %d.", currentLineNum, def.Name, def.DefinitionRootElementIndex)
				}
				isDefinitionRoot = true
				componentDefContext = def
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
				SelfIndex:             elementIndex,
				ParentIndex:           parentIndex,
				SourceElementName:     elementName,
				SourceLineNum:         currentLineNum,
				CalculatedSize:        KRBElementHeaderSize, // Base size, props/children will add to it
				SourceProperties:      make([]SourceProperty, 0, 8),
				KrbProperties:         make([]KrbProperty, 0, 8),
				KrbCustomProperties:   make([]KrbCustomProperty, 0, 2),
				KrbEvents:             make([]KrbEvent, 0, 2),
				SourceChildrenIndices: make([]int, 0, 4),
				Children:              make([]*Element, 0, 4),
				IsDefinitionRoot:      isDefinitionRoot,
			}
			if parentElement != nil && parentElement.IsDefinitionRoot {
				el.IsDefinitionRoot = true // Propagate template context
			}

			if compDef := state.findComponentDef(elementName); compDef != nil {
				if isDefinitionRoot {
					return fmt.Errorf("L%d: cannot use component '%s' as the root element definition for 'Define %s'. Root must be a standard element type.", currentLineNum, elementName, componentDefContext.Name)
				}
				el.Type = ElemTypeInternalComponentUsage
				el.ComponentDef = compDef
				el.IsComponentInstance = true
			} else {
				el.Type = getElementTypeFromName(elementName)
				if el.Type == ElemTypeUnknown {
					el.Type = ElemTypeCustomBase
					nameIdx, err := state.addString(elementName)
					if err != nil {
						return err
					}
					el.IDStringIndex = nameIdx
					log.Printf("L%d: Warn: Unknown element type '%s', treating as custom (type 0x%X with name index %d)\n", currentLineNum, elementName, el.Type, nameIdx)
				}
				if !isDefinitionRoot {
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

			if parentElement != nil {
				if len(parentElement.SourceChildrenIndices) >= MaxChildren {
					return fmt.Errorf("L%d: max children (%d) for parent '%s'", currentLineNum, MaxChildren, parentElement.SourceElementName)
				}
				parentElement.SourceChildrenIndices = append(parentElement.SourceChildrenIndices, currentElement.SelfIndex)
			}
			if isDefinitionRoot && componentDefContext != nil {
				componentDefContext.DefinitionRootElementIndex = currentElement.SelfIndex
			}

			inlinePropsStr, childBlockFollows, isSelfClosedOnLine := extractContentOfFirstBlockOnLine(strings.TrimSpace(contentAfterElementName))

			if inlinePropsStr != "" {
				err := state.parseAndAddPropertiesToElement(currentElement, inlinePropsStr, currentLineNum)
				if err != nil {
					return err
				}
			}

			if !isSelfClosedOnLine { // True if "Elem {" OR "Elem {props} {" (child block) OR "Elem {props...NO_CLOSING_BRACE_YET"
				if len(blockStack) >= MaxBlockDepth {
					return fmt.Errorf("L%d: max block depth exceeded for element '%s'", currentLineNum, elementName)
				}
				blockStack = append(blockStack, BlockStackEntry{indent, currentElement, CtxElement})
			} else if childBlockFollows { // Case: "Elem {props} {child_block_start}"
				// The first block self-closed, but an explicit child block follows.
				// Need to push context for the child block.
				if len(blockStack) >= MaxBlockDepth {
					return fmt.Errorf("L%d: max block depth exceeded before child block for element '%s'", currentLineNum, elementName)
				}
				blockStack = append(blockStack, BlockStackEntry{indent, currentElement, CtxElement})
			}
			// If isSelfClosedOnLine is true AND childBlockFollows is false, the element is fully defined on this line.
			// Example: Image { source:"..." }
			// No push to stack needed.
			continue
		}

		// --- 2e. Edge Inset Block Start (e.g., padding: {) ---
		// This specific check must come after general element parsing because an element could be named "padding" or "margin".
		// The distinction is "padding: {" vs "Padding {".
		partsForEdgeInset := strings.SplitN(trimmedLine, ":", 2)
		if len(partsForEdgeInset) == 2 {
			key := strings.TrimSpace(partsForEdgeInset[0])
			val := strings.TrimSpace(partsForEdgeInset[1])
			if (key == "padding" || key == "margin") && val == "{" {
				if currentCtxType != CtxElement && currentCtxType != CtxStyle {
					return fmt.Errorf("L%d: '%s: {' block must be inside an Element or Style block (current: %v)", currentLineNum, key, currentCtxType)
				}
				if len(blockStack) >= MaxBlockDepth {
					return fmt.Errorf("L%d: max block depth for '%s: {'", currentLineNum, key)
				}
				edgeState := &EdgeInsetParseState{
					ParentKey:     key,
					ParentCtx:     currentContext,
					ParentCtxType: currentCtxType,
					Indent:        indent,
					StartLine:     currentLineNum,
				}
				blockStack = append(blockStack, BlockStackEntry{Indent: indent, Context: edgeState, Type: CtxEdgeInsetProperty})
				continue
			}
		}

		// --- 3. Process Non-Block-Starting Lines (Properties or invalid syntax) ---
		if len(blockStack) == 0 {
			return fmt.Errorf("L%d: unexpected syntax at top level: '%s' (expected block definition or @directive)", currentLineNum, trimmedLine)
		}
		// Heuristic: if a line is not indented further than its block opener, it might be an error.
		// This needs careful consideration with how `indent` is calculated and compared to `blockStack[len(blockStack)-1].Indent`.
		// For now, rely on context type.

		switch currentCtxType {
		case CtxElement:
			currentElement, ok := currentContext.(*Element)
			if !ok {
				return fmt.Errorf("L%d: internal: CtxElement context error", currentLineNum)
			}
			err := state.parseAndAddPropertiesToElement(currentElement, trimmedLine, currentLineNum)
			if err != nil {
				return err
			}
		case CtxStyle:
			currentStyle, ok := currentContext.(*StyleEntry)
			if !ok {
				return fmt.Errorf("L%d: internal: CtxStyle context error", currentLineNum)
			}
			err := state.parseAndAddPropertiesToStyle(currentStyle, trimmedLine, currentLineNum)
			if err != nil {
				return err
			}
		case CtxProperties:
			if strings.Contains(trimmedLine, ";") {
				return fmt.Errorf("L%d: semicolons not allowed in 'Properties' definitions line: '%s'", currentLineNum, trimmedLine)
			}
			propParts := strings.SplitN(trimmedLine, ":", 2)
			if len(propParts) != 2 {
				return fmt.Errorf("L%d: invalid syntax in Properties (expected 'key: Type [= Default]'): '%s'", currentLineNum, trimmedLine)
			}
			key := strings.TrimSpace(propParts[0])
			valueStr := strings.TrimSpace(propParts[1])
			parentComponentDef, _ := currentContext.(*ComponentDefinition)

			valTypeAndDefault := strings.SplitN(valueStr, "=", 2)
			propTypeStr := strings.TrimSpace(valTypeAndDefault[0])
			propDefaultStr := ""
			if len(valTypeAndDefault) == 2 {
				propDefaultStr = strings.TrimSpace(valTypeAndDefault[1])
			}

			if len(parentComponentDef.Properties) >= MaxProperties {
				return fmt.Errorf("L%d: max props (%d) for component '%s'", currentLineNum, MaxProperties, parentComponentDef.Name)
			}
			pd := ComponentPropertyDef{Name: key, DefaultValueStr: propDefaultStr}
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
		case CtxEdgeInsetProperty:
			if strings.Contains(trimmedLine, ";") {
				return fmt.Errorf("L%d: semicolons not allowed in edge inset definitions line: '%s'", currentLineNum, trimmedLine)
			}
			propParts := strings.SplitN(trimmedLine, ":", 2)
			if len(propParts) != 2 {
				log.Printf("L%d: Warn: Invalid syntax in edge inset block: '%s'. Expected 'side: value'. Ignored.", currentLineNum, trimmedLine)
				continue
			}
			key := strings.TrimSpace(propParts[0])
			valueStr := strings.TrimSpace(propParts[1])
			edgeState, _ := currentContext.(*EdgeInsetParseState)
			switch key {
			case "top":
				if edgeState.Top != nil {
					log.Printf("L%d: Warn: duplicate 'top' in '%s'. Overwriting.", currentLineNum, edgeState.ParentKey)
				}
				edgeState.Top = &valueStr
			case "right":
				if edgeState.Right != nil {
					log.Printf("L%d: Warn: duplicate 'right' in '%s'. Overwriting.", currentLineNum, edgeState.ParentKey)
				}
				edgeState.Right = &valueStr
			case "bottom":
				if edgeState.Bottom != nil {
					log.Printf("L%d: Warn: duplicate 'bottom' in '%s'. Overwriting.", currentLineNum, edgeState.ParentKey)
				}
				edgeState.Bottom = &valueStr
			case "left":
				if edgeState.Left != nil {
					log.Printf("L%d: Warn: duplicate 'left' in '%s'. Overwriting.", currentLineNum, edgeState.ParentKey)
				}
				edgeState.Left = &valueStr
			case "all":
				if edgeState.Top != nil || edgeState.Right != nil || edgeState.Bottom != nil || edgeState.Left != nil {
					log.Printf("L%d: Warn: 'all' specified after individual sides in '%s'. 'all' will overwrite.", currentLineNum, edgeState.ParentKey)
				}
				edgeState.Top = &valueStr
				edgeState.Right = &valueStr
				edgeState.Bottom = &valueStr
				edgeState.Left = &valueStr
			case "horizontal":
				if edgeState.Left != nil || edgeState.Right != nil {
					log.Printf("L%d: Warn: 'horizontal' specified after 'left' or 'right' in '%s'. 'horizontal' will overwrite.", currentLineNum, edgeState.ParentKey)
				}
				edgeState.Left = &valueStr
				edgeState.Right = &valueStr
			case "vertical":
				if edgeState.Top != nil || edgeState.Bottom != nil {
					log.Printf("L%d: Warn: 'vertical' specified after 'top' or 'bottom' in '%s'. 'vertical' will overwrite.", currentLineNum, edgeState.ParentKey)
				}
				edgeState.Top = &valueStr
				edgeState.Bottom = &valueStr
			default:
				log.Printf("L%d: Warn: Unexpected key '%s' in '%s: {}' block. Ignored.", currentLineNum, key, edgeState.ParentKey)
			}
		case CtxComponentDef:
			log.Printf("L%d: Warning: Property-like syntax '%s' directly under Define block. Ignored. Use 'Properties {}' or define on root element.", currentLineNum, trimmedLine)
		default:
			return fmt.Errorf("L%d: internal error: unexpected context %v for property line '%s'", currentLineNum, currentCtxType, trimmedLine)
		}
		continue
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading source buffer: %w", err)
	}

	if len(blockStack) != 0 {
		lastEntry := blockStack[len(blockStack)-1]
		contextStr := fmt.Sprintf("%v", lastEntry.Type)
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
			contextStr = "Properties block"
		case CtxEdgeInsetProperty:
			if es, ok := lastEntry.Context.(*EdgeInsetParseState); ok {
				contextStr = fmt.Sprintf("'%s: {}' L%d", es.ParentKey, es.StartLine)
			}
		}
		return fmt.Errorf("unclosed block at end of file (last open: %s)", contextStr)
	}

	rootElementIndex := -1
	for i, el := range state.Elements {
		if el.ParentIndex == -1 && !el.IsDefinitionRoot {
			rootElementIndex = i
			break
		}
	}
	if rootElementIndex == -1 {
		if len(state.Elements) == 0 && len(state.ComponentDefs) == 0 && len(state.Styles) == 0 { // Check styles too
			return errors.New("no content found: define 'App', components, or styles")
		}
		allAreTemplatesOrStyles := true
		if len(state.Elements) > 0 {
			for _, el := range state.Elements {
				if !el.IsDefinitionRoot {
					allAreTemplatesOrStyles = false
					break
				}
			}
		}
		if allAreTemplatesOrStyles && (len(state.ComponentDefs) > 0 || len(state.Styles) > 0) {
			log.Println("Info: Only component definitions and/or styles found. No main 'App' element or root component instance.")
		} else if len(state.Elements) > 0 { // Elements exist, but not a valid root structure
			return errors.New("internal error: elements present but no main UI tree root identified, or it's not an App/Component instance")
		}
	} else {
		rootElement := state.Elements[rootElementIndex]
		if rootElement.Type != ElemTypeApp && !rootElement.IsComponentInstance {
			return fmt.Errorf("main UI tree root element must be 'App' or a component instance, but found '%s' (type 0x%X) at L%d", rootElement.SourceElementName, rootElement.Type, rootElement.SourceLineNum)
		}
		if rootElement.Type == ElemTypeApp && !state.HasApp {
			state.HasApp = true
			state.HeaderFlags |= FlagHasApp
		}
		if rootElement.IsComponentInstance && !state.HasApp {
			log.Println("Info: Root element is a component instance. Assuming 'App' container behavior.")
			state.HasApp = true
		}
	}

	for i := range state.ComponentDefs {
		if state.ComponentDefs[i].DefinitionRootElementIndex == -1 {
			return fmt.Errorf("component definition 'Define %s' (L%d) is missing its root element template (e.g., Container { ... })", state.ComponentDefs[i].Name, state.ComponentDefs[i].DefinitionStartLine)
		}
	}

	return nil
}

// parseAndAddPropertiesToElement parses a string of semicolon-separated properties
// and adds them to the given Element.
func (state *CompilerState) parseAndAddPropertiesToElement(el *Element, propsStr string, lineNum int) error {
	individualPropStrings := splitPropertiesStringBySemicolon(propsStr)

	for _, propPairStr := range individualPropStrings {
		trimmedPair := strings.TrimSpace(propPairStr)
		if trimmedPair == "" {
			continue
		}
		parts := strings.SplitN(trimmedPair, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("L%d: invalid property format '%s' (from full string '%s') for element '%s'", lineNum, trimmedPair, propsStr, el.SourceElementName)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if err := el.addSourceProperty(key, value, lineNum); err != nil {
			return fmt.Errorf("L%d: %w", lineNum, err) // Error from addSourceProperty
		}
	}
	return nil
}

// parseAndAddPropertiesToStyle parses semicolon-separated properties for a StyleEntry.

func (state *CompilerState) parseAndAddPropertiesToStyle(style *StyleEntry, propsStr string, lineNum int) error {
	individualPropStrings := splitPropertiesStringBySemicolon(propsStr)

	for i, propPairStr := range individualPropStrings {
		trimmedPair := strings.TrimSpace(propPairStr)
		if trimmedPair == "" {
			continue
		}

		parts := strings.SplitN(trimmedPair, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("L%d: invalid property format '%s' (from full string '%s') for style '%s'", lineNum, trimmedPair, propsStr, style.SourceName)
		}

		key := strings.TrimSpace(parts[0])
		valueRaw := strings.TrimSpace(parts[1])

		if key == "extends" {
			if i > 0 {
				return fmt.Errorf("L%d: 'extends' must be the first property in style '%s', found after '%s'", lineNum, style.SourceName, individualPropStrings[i-1])
			}

			// Check if 'extends' was already processed from a previous line in the same style block.
			// This check is a bit more robust than just looking at SourceProperties because
			// SourceProperties might not yet include the 'extends' from this specific line if it's a multi-line definition.
			// The core logic is that ExtendsStyleNames should only be populated once.
			if len(style.ExtendsStyleNames) > 0 && style.SourcePropertiesContainsKey("extends") {
				// If ExtendsStyleNames is already populated AND we've already recorded an "extends" source property,
				// it implies 'extends' was defined on a previous line.
				return fmt.Errorf("L%d: style '%s' 'extends' property defined multiple times. Consolidate into one 'extends' (possibly with an array).", lineNum, style.SourceName)
			}

			// Check if valueRaw is an array like ["style1", "style2"]
			if strings.HasPrefix(valueRaw, "[") && strings.HasSuffix(valueRaw, "]") {
				// Attempt to parse as an array of strings
				arrayContent := valueRaw[1 : len(valueRaw)-1] // Remove brackets

				if strings.TrimSpace(arrayContent) == "" { // Handle empty array extends: []
					style.ExtendsStyleNames = []string{} // No base styles, this is valid.
					// Add the raw "[]" as a source property for completeness
					if err := style.addSourceProperty(key, valueRaw, lineNum); err != nil {
						return fmt.Errorf("L%d adding empty 'extends' array source property: %w", lineNum, err)
					}
					continue
				}

				baseStyleNamesRaw := strings.Split(arrayContent, ",")
				var parsedBaseNames []string
				for _, nameRaw := range baseStyleNamesRaw {
					trimmedNameRaw := strings.TrimSpace(nameRaw)
					// cleanAndQuoteValue also removes the quotes around each individual style name
					cleanedName, wasQuoted := cleanAndQuoteValue(trimmedNameRaw)

					if !wasQuoted || cleanedName == "" {
						return fmt.Errorf("L%d: 'extends' array for style '%s' contains non-quoted or empty style name: '%s'. All names in the array must be quoted strings.", lineNum, style.SourceName, trimmedNameRaw)
					}
					if cleanedName == style.SourceName {
						return fmt.Errorf("L%d: style '%s' cannot extend itself (found in extends array)", lineNum, style.SourceName)
					}
					parsedBaseNames = append(parsedBaseNames, cleanedName)
				}

				if len(parsedBaseNames) == 0 && strings.TrimSpace(arrayContent) != "" {
					// This case can happen if arrayContent was e.g. "," or " , " but no valid names
					return fmt.Errorf("L%d: 'extends' array for style '%s' resulted in no valid style names from content: '%s'", lineNum, style.SourceName, arrayContent)
				}

				style.ExtendsStyleNames = parsedBaseNames
				// Add the raw array string as a source property for completeness
				if err := style.addSourceProperty(key, valueRaw, lineNum); err != nil {
					return fmt.Errorf("L%d adding 'extends' array source property: %w", lineNum, err)
				}

			} else {
				// Attempt to parse as a single quoted string
				cleanedValue, wasQuoted := cleanAndQuoteValue(valueRaw)
				if !wasQuoted {
					return fmt.Errorf("L%d: 'extends' value '%s' for style '%s' must be a quoted string or an array of quoted strings.", lineNum, valueRaw, style.SourceName)
				}
				if cleanedValue == "" {
					return fmt.Errorf("L%d: 'extends' value is an empty string for style '%s'", lineNum, style.SourceName)
				}
				if cleanedValue == style.SourceName {
					return fmt.Errorf("L%d: style '%s' cannot extend itself", lineNum, style.SourceName)
				}
				style.ExtendsStyleNames = []string{cleanedValue}
				// Add the single string as a source property
				if err := style.addSourceProperty(key, valueRaw, lineNum); err != nil {
					return fmt.Errorf("L%d adding single 'extends' source property: %w", lineNum, err)
				}
			}
		} else { // Not an 'extends' key, handle as a normal style property
			if err := style.addSourceProperty(key, valueRaw, lineNum); err != nil {
				return fmt.Errorf("L%d adding style property '%s': %w", lineNum, key, err)
			}
		}
	}
	return nil
}

func trimLeadingWhitespaceAndGetIndent(line string) (trimmed string, indent int) {
	for i, r := range line {
		if r == ' ' {
			indent++
		} else if r == '\t' {
			indent += 4 // Standard tab width assumption
		} else {
			return line[i:], indent
		}
	}
	return "", indent
}

func stripCommentAndTrim(line string) string {
	commentIdx := -1
	inString := false
	var prevRune rune
	for i, char := range line {
		if char == '"' && prevRune != '\\' {
			inString = !inString
		}
		if char == '#' && !inString {
			commentIdx = i
			break
		}
		prevRune = char
	}
	if commentIdx != -1 {
		line = line[:commentIdx]
	}
	return strings.TrimSpace(line)
}

func splitFirstWord(line string) (firstWord, rest string) {
	trimmed := strings.TrimSpace(line)
	spaceIdx := strings.IndexFunc(trimmed, unicode.IsSpace)
	if spaceIdx == -1 {
		return trimmed, ""
	}
	return trimmed[:spaceIdx], strings.TrimSpace(trimmed[spaceIdx+1:])
}

func extractContentOfFirstBlockOnLine(lineSegmentStartingWithBrace string) (contentString string, childBlockFollows bool, isSelfClosed bool) {
	trimmedSegment := strings.TrimSpace(lineSegmentStartingWithBrace)
	if !strings.HasPrefix(trimmedSegment, "{") {
		return "", false, false
	}

	balance := 0
	firstBraceContentEnd := -1
	inString := false
	var prevRune rune

	for i, r := range trimmedSegment {
		if r == '"' && prevRune != '\\' {
			inString = !inString
		}
		prevRune = r

		if r == '{' && !inString {
			balance++
		} else if r == '}' && !inString {
			balance--
			if balance == 0 {
				firstBraceContentEnd = i
				break
			}
		}
	}

	if firstBraceContentEnd != -1 {
		contentString = strings.TrimSpace(trimmedSegment[1:firstBraceContentEnd])
		isSelfClosed = true

		remainingAfterFirstBlock := strings.TrimSpace(trimmedSegment[firstBraceContentEnd+1:])
		if strings.HasPrefix(remainingAfterFirstBlock, "{") {
			childBlockFollows = true
			isSelfClosed = false
		} else if remainingAfterFirstBlock != "" {
			// This is an error condition, e.g., "Element {props} garbage"
			// The parser calling this should probably error out.
			// For this function, we report what we found structurally.
		}
		return contentString, childBlockFollows, isSelfClosed
	}
	return "", false, false
}

func splitPropertiesStringBySemicolon(propsStr string) []string {
	var props []string
	var currentProp strings.Builder
	inQuotes := false
	var prevRune rune
	runes := []rune(strings.TrimSpace(propsStr)) // Trim overall string first

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '"' && prevRune != '\\' { // Basic quote handling
			inQuotes = !inQuotes
		}
		prevRune = r

		if r == ';' && !inQuotes {
			propCandidate := strings.TrimSpace(currentProp.String())
			if propCandidate != "" { // Add only if non-empty after trim
				props = append(props, propCandidate)
			}
			currentProp.Reset()
			continue
		}
		currentProp.WriteRune(r)
	}

	lastPropCandidate := strings.TrimSpace(currentProp.String())
	if lastPropCandidate != "" { // Add the last property if any content remains
		props = append(props, lastPropCandidate)
	}
	return props
}

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
		return 0, fmt.Errorf("maximum string limit (%d) exceeded when adding '%s'", MaxStrings, cleaned)
	}
	if len(state.Strings) == 0 {
		state.Strings = append(state.Strings, StringEntry{Text: "", Length: 0, Index: 0})
	}
	idx := uint8(len(state.Strings))
	entry := StringEntry{Text: cleaned, Length: len(cleaned), Index: idx}
	state.Strings = append(state.Strings, entry)
	return idx, nil
}

func (state *CompilerState) addResource(resType uint8, pathStr string) (uint8, error) {
	pathIdx, err := state.addString(pathStr)
	if err != nil {
		return 0, fmt.Errorf("failed to add resource path '%s' to string table: %w", pathStr, err)
	}
	if pathIdx == 0 && strings.TrimSpace(pathStr) != "" {
		return 0, fmt.Errorf("resource path '%s' resolved to empty string index", pathStr)
	}
	if pathIdx == 0 && strings.TrimSpace(pathStr) == "" {
		return 0, fmt.Errorf("resource path cannot be empty or whitespace only")
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

func (el *Element) addSourceProperty(key, value string, lineNum int) error {
	for i := range el.SourceProperties {
		if el.SourceProperties[i].Key == key {
			el.SourceProperties[i].ValueStr = value
			el.SourceProperties[i].LineNum = lineNum
			return nil
		}
	}
	if len(el.SourceProperties) >= MaxProperties {
		return fmt.Errorf("maximum source properties (%d) exceeded for element '%s' when adding '%s'", MaxProperties, el.SourceElementName, key)
	}
	prop := SourceProperty{Key: key, ValueStr: value, LineNum: lineNum}
	el.SourceProperties = append(el.SourceProperties, prop)
	return nil
}
