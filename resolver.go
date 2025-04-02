// resolver.go
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
)

// resolveElementRecursive processes an element, expands components, resolves properties, and recurses.
func (state *CompilerState) resolveElementRecursive(elementIndex int) error {
	if elementIndex < 0 || elementIndex >= len(state.Elements) {
		return fmt.Errorf("internal error: invalid element index %d during resolution", elementIndex)
	}
	el := &state.Elements[elementIndex] // Get pointer to modify

	if el.ProcessedInPass15 {
		return nil // Already processed
	}
	el.ProcessedInPass15 = true // Mark early for recursion

	var originalSourceProperties []SourceProperty // Keep original usage props if expanding

	// --- Expand Component (if necessary) ---
	if el.Type == ElemTypeInternalComponentUsage {
		if el.ComponentDef == nil {
			return fmt.Errorf("L%d: internal error: component instance '%s' has nil definition", el.SourceLineNum, el.SourceElementName)
		}
		def := el.ComponentDef

		originalSourceProperties = make([]SourceProperty, len(el.SourceProperties))
		copy(originalSourceProperties, el.SourceProperties) // Save usage properties

		rootType := "Container" // Default if not specified
		if def.DefinitionRootType != "" {
			rootType = def.DefinitionRootType
		}
		el.Type = getElementTypeFromName(rootType)
		if el.Type == ElemTypeUnknown {
			el.Type = ElemTypeCustomBase
			nameIdx, err := state.addString(rootType)
			if err != nil {
				return fmt.Errorf("L%d: failed adding component root type name '%s': %w", el.SourceLineNum, rootType, err)
			}
			el.IDStringIndex = nameIdx
			log.Printf("L%d: Info: Component '%s' expands to unknown root type '%s', using custom type 0x%X\n", el.SourceLineNum, def.Name, rootType, el.Type)
		}

		mergedProps := make(map[string]SourceProperty)
		for _, prop := range def.DefinitionRootProperties {
			mergedProps[prop.Key] = prop
		}
		for _, prop := range originalSourceProperties {
			mergedProps[prop.Key] = prop
		}
		propHints := make(map[string]uint8)
		for _, propDef := range def.Properties {
			propHints[propDef.Name] = propDef.ValueTypeHint
			if _, exists := mergedProps[propDef.Name]; !exists && propDef.DefaultValueStr != "" {
				mergedProps[propDef.Name] = SourceProperty{
					Key: propDef.Name, ValueStr: propDef.DefaultValueStr, LineNum: def.DefinitionStartLine,
				}
				log.Printf("L%d: Applying default value '%s' for property '%s' in component '%s'\n", el.SourceLineNum, propDef.DefaultValueStr, propDef.Name, def.Name)
			}
		}
		el.SourceProperties = make([]SourceProperty, 0, len(mergedProps))
		for _, prop := range mergedProps {
			el.SourceProperties = append(el.SourceProperties, prop)
		}

		orientationStr, _ := el.getSourcePropertyValue("orientation")
		positionStr, _ := el.getSourcePropertyValue("position")
		directStyleStr, _ := el.getSourcePropertyValue("style")
		if orientationStr == "" {
			orientationStr = "row"
		}
		if positionStr == "" {
			positionStr = "bottom"
		}
		el.OrientationHint = orientationStr
		el.PositionHint = positionStr

		finalStyleID := uint8(0)
		if directStyleStr != "" {
			finalStyleID = state.findStyleIDByName(directStyleStr)
			if finalStyleID == 0 {
				log.Printf("L%d: Warning: Explicit style '%s' not found for component '%s'.\n", el.SourceLineNum, directStyleStr, def.Name)
			}
		} else {
			defRootStyle, hasDefRootStyle := def.getRootPropertyValue("style")
			if hasDefRootStyle {
				finalStyleID = state.findStyleIDByName(defRootStyle)
				if finalStyleID == 0 {
					log.Printf("L%d: Warning: Component definition root style '%s' not found for '%s'.\n", def.DefinitionStartLine, defRootStyle, def.Name)
				}
			}
		}
		el.StyleID = finalStyleID

		el.LayoutFlagsSource = LayoutDirectionRow | LayoutAlignmentStart
		if orientationStr == "column" || orientationStr == "col" {
			el.LayoutFlagsSource = LayoutDirectionColumn | LayoutAlignmentStart
		}
	} // --- End of Component Expansion Specifics ---

	// --- Resolve Specific Header Fields (ID, Style, Pos, Size) FIRST ---
	el.KrbProperties = el.KrbProperties[:0] // Clear KRB properties
	el.PropertyCount = 0
	el.KrbEvents = el.KrbEvents[:0] // Clear KRB events
	el.EventCount = 0

	processedSourcePropKeys := make(map[string]bool) // Track keys handled here

	for _, sp := range el.SourceProperties {
		key := sp.Key
		valStr := sp.ValueStr
		lineNum := sp.LineNum
		cleanVal := trimQuotes(strings.TrimSpace(valStr))

		handled := true // Assume handled unless switched
		var parseErr error

		switch key {
		case "id":
			if cleanVal != "" {
				idIdx, err := state.addString(cleanVal)
				if err != nil {
					parseErr = err
				} else {
					el.IDStringIndex = idIdx
					el.SourceIDName = cleanVal
				}
			} else {
				log.Printf("L%d: Warning: Empty 'id' ignored", lineNum)
			}
		case "style":
			if cleanVal != "" {
				sid := state.findStyleIDByName(cleanVal)
				if sid == 0 && !el.IsComponentInstance { // Only warn if not set by component logic
					log.Printf("L%d: Warning: Style '%s' not found", lineNum, cleanVal)
				}
				// Current logic: let explicit 'style:' override component logic if present.
				el.StyleID = sid
			}
		case "pos_x":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil {
				el.PosX = uint16(v)
			} else {
				parseErr = fmt.Errorf("invalid pos_x '%s': %w", cleanVal, err)
			}
		case "pos_y":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil {
				el.PosY = uint16(v)
			} else {
				parseErr = fmt.Errorf("invalid pos_y '%s': %w", cleanVal, err)
			}
		case "width":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil {
				el.Width = uint16(v)
			} else {
				parseErr = fmt.Errorf("invalid width '%s': %w", cleanVal, err)
			}
		case "height":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil {
				el.Height = uint16(v)
			} else {
				parseErr = fmt.Errorf("invalid height '%s': %w", cleanVal, err)
			}
		default:
			handled = false // Mark as not handled here, process in next loop
		}

		if parseErr != nil {
			return fmt.Errorf("L%d: error processing header field '%s': %w", lineNum, key, parseErr)
		}
		if handled {
			processedSourcePropKeys[key] = true
		}
	}

	// --- Resolve Remaining Source Properties into KRB Properties/Events ---
	for _, sp := range el.SourceProperties {
		key := sp.Key
		valStr := sp.ValueStr // Might have quotes
		lineNum := sp.LineNum

		// Skip properties already handled as header fields or used as hints
		if processedSourcePropKeys[key] {
			continue
		}
		switch key {
		case "orientation", "position": // Hints were already used if needed
			continue
		}

		// --- Handle Layout Property ---
		if key == "layout" {
			layoutByte := parseLayoutString(trimQuotes(strings.TrimSpace(valStr)))
			el.LayoutFlagsSource = layoutByte // Store the explicitly defined layout
			processedSourcePropKeys[key] = true // Mark as handled
			continue                           // Don't add as KRB property
		}

		// --- Handle Event Properties (e.g., onClick) ---
		if key == "onClick" {
			if len(el.KrbEvents) >= MaxEvents {
				return fmt.Errorf("L%d: maximum events (%d) exceeded for element '%s'", lineNum, MaxEvents, el.SourceElementName)
			}
			callbackName := trimQuotes(strings.TrimSpace(valStr))
			if callbackName == "" {
				log.Printf("L%d: Warning: Empty callback name for '%s' ignored.\n", lineNum, key)
				processedSourcePropKeys[key] = true
				continue
			}
			cbIdx, err := state.addString(callbackName)
			if err != nil {
				return fmt.Errorf("L%d: failed adding callback string '%s': %w", lineNum, callbackName, err)
			}
			event := KrbEvent{EventType: EventTypeClick, CallbackID: cbIdx}
			el.KrbEvents = append(el.KrbEvents, event)
			el.EventCount = uint8(len(el.KrbEvents))
			processedSourcePropKeys[key] = true // Mark as handled
			continue
		}

		// --- Convert standard source props to KRB props ---
		var err error
		propAdded := true // Assume property is handled unless switched otherwise
		cleanVal := trimQuotes(strings.TrimSpace(valStr))

		switch key {
		case "background_color":
			if col, ok := parseColor(valStr); ok {
				err = el.addKrbProperty(PropIDBgColor, ValTypeColor, col[:])
				if err == nil {
					state.HeaderFlags |= FlagExtendedColor
				}
			} else {
				err = fmt.Errorf("invalid color value '%s'", valStr)
			}
		case "text_color", "foreground_color":
			if col, ok := parseColor(valStr); ok {
				err = el.addKrbProperty(PropIDFgColor, ValTypeColor, col[:])
				if err == nil {
					state.HeaderFlags |= FlagExtendedColor
				}
			} else {
				err = fmt.Errorf("invalid color value '%s'", valStr)
			}
		case "border_color":
			if col, ok := parseColor(valStr); ok {
				err = el.addKrbProperty(PropIDBorderColor, ValTypeColor, col[:])
				if err == nil {
					state.HeaderFlags |= FlagExtendedColor
				}
			} else {
				err = fmt.Errorf("invalid color value '%s'", valStr)
			}
		case "border_width":
			if bw, convErr := strconv.ParseUint(cleanVal, 10, 8); convErr == nil {
				err = el.addKrbProperty(PropIDBorderWidth, ValTypeByte, []byte{uint8(bw)})
			} else {
				err = fmt.Errorf("invalid border_width '%s': %w", cleanVal, convErr)
			}
		case "border_radius":
			if br, convErr := strconv.ParseUint(cleanVal, 10, 8); convErr == nil {
				err = el.addKrbProperty(PropIDBorderRadius, ValTypeByte, []byte{uint8(br)})
			} else {
				err = fmt.Errorf("invalid border_radius '%s': %w", cleanVal, convErr)
			}
		case "text", "content":
			err = state.addKrbStringProperty(el, PropIDTextContent, valStr)
		case "image_source", "source":
			if el.Type == ElemTypeImage || el.Type == ElemTypeButton {
				err = state.addKrbResourceProperty(el, PropIDImageSource, ResTypeImage, cleanVal)
			} else {
				log.Printf("L%d: Warning: Property '%s' ignored for element type %d ('%s').\n", lineNum, key, el.Type, el.SourceElementName)
				propAdded = false
			}
		case "font_size":
			if fs, convErr := strconv.ParseUint(cleanVal, 10, 16); convErr == nil && fs > 0 && fs <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(fs))
				err = el.addKrbProperty(PropIDFontSize, ValTypeShort, buf)
			} else if convErr != nil {
				err = fmt.Errorf("invalid font_size '%s': %w", cleanVal, convErr)
			} else {
				err = fmt.Errorf("font_size '%s' out of range (1-%d)", cleanVal, math.MaxUint16)
			}
		case "text_alignment":
			align := uint8(0)
			switch cleanVal {
			case "center", "centre":
				align = 1
			case "right", "end":
				align = 2
			case "left", "start":
				align = 0
			default:
				log.Printf("L%d: Warning: Invalid text_alignment '%s', defaulting to left.\n", lineNum, cleanVal)
			}
			err = el.addKrbProperty(PropIDTextAlignment, ValTypeEnum, []byte{align})
		case "gap":
			if g, convErr := strconv.ParseUint(cleanVal, 10, 16); convErr == nil && g <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(g))
				err = el.addKrbProperty(PropIDGap, ValTypeShort, buf)
			} else if convErr != nil {
				err = fmt.Errorf("invalid gap '%s': %w", cleanVal, convErr)
			} else {
				err = fmt.Errorf("gap '%s' out of range (0-%d)", cleanVal, math.MaxUint16)
			}
			// --- App specific properties ---
		case "window_width":
			if el.Type == ElemTypeApp {
				if v, convErr := strconv.ParseUint(cleanVal, 10, 16); convErr == nil && v > 0 && v <= math.MaxUint16 {
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, uint16(v))
					err = el.addKrbProperty(PropIDWindowWidth, ValTypeShort, buf)
				} else if convErr != nil {
					err = fmt.Errorf("invalid window_width '%s': %w", cleanVal, convErr)
				} else {
					err = fmt.Errorf("window_width '%s' out of range", cleanVal)
				}
			} else {
				propAdded = false
			}
		case "window_height":
			if el.Type == ElemTypeApp {
				if v, convErr := strconv.ParseUint(cleanVal, 10, 16); convErr == nil && v > 0 && v <= math.MaxUint16 {
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, uint16(v))
					err = el.addKrbProperty(PropIDWindowHeight, ValTypeShort, buf)
				} else if convErr != nil {
					err = fmt.Errorf("invalid window_height '%s': %w", cleanVal, convErr)
				} else {
					err = fmt.Errorf("window_height '%s' out of range", cleanVal)
				}
			} else {
				propAdded = false
			}
		case "window_title":
			if el.Type == ElemTypeApp {
				err = state.addKrbStringProperty(el, PropIDWindowTitle, valStr)
			} else {
				propAdded = false
			}
		case "resizable":
			if el.Type == ElemTypeApp {
				resBool := (cleanVal == "true")
				valByte := uint8(0)
				if resBool {
					valByte = 1
				}
				err = el.addKrbProperty(PropIDResizable, ValTypeByte, []byte{valByte})
			} else {
				propAdded = false
			}
		case "icon":
			if el.Type == ElemTypeApp {
				err = state.addKrbResourceProperty(el, PropIDIcon, ResTypeImage, cleanVal)
			} else {
				propAdded = false
			}
		case "version":
			if el.Type == ElemTypeApp {
				err = state.addKrbStringProperty(el, PropIDVersion, valStr)
			} else {
				propAdded = false
			}
		case "author":
			if el.Type == ElemTypeApp {
				err = state.addKrbStringProperty(el, PropIDAuthor, valStr)
			} else {
				propAdded = false
			}
		case "keep_aspect":
			if el.Type == ElemTypeApp {
				keepAspectBool := (cleanVal == "true")
				valByte := uint8(0)
				if keepAspectBool {
					valByte = 1
				}
				err = el.addKrbProperty(PropIDKeepAspect, ValTypeByte, []byte{valByte})
			} else {
				propAdded = false
			}
		case "scale_factor":
			if el.Type == ElemTypeApp {
				if scaleF, convErr := strconv.ParseFloat(cleanVal, 64); convErr == nil {
					fixedPointVal := uint16(scaleF * 256.0)
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, fixedPointVal)
					err = el.addKrbProperty(PropIDScaleFactor, ValTypePercentage, buf)
					if err == nil {
						state.HeaderFlags |= FlagFixedPoint
					}
				} else {
					err = fmt.Errorf("invalid scale_factor float value '%s': %w", cleanVal, convErr)
				}
			} else {
				propAdded = false
			}
			// --- End App Specific ---

		// ... TODO: Add cases for ALL other standard KRB properties ...

		default:
			propAdded = false // Mark as not handled by standard conversions
			// Log warning if it wasn't handled previously and isn't a known component prop
			if !processedSourcePropKeys[key] {
				logUnhandledPropWarning(el, key, lineNum)
			}
		} // End switch key

		if err != nil {
			return fmt.Errorf("L%d: error processing property '%s': %w", lineNum, key, err)
		}
		if propAdded {
			processedSourcePropKeys[key] = true // Mark as handled by KRB conversion
		}

	} // End loop through remaining source properties

	// Final check for any unprocessed source properties
	for _, sp := range el.SourceProperties {
		if !processedSourcePropKeys[sp.Key] {
			logUnhandledPropWarning(el, sp.Key, sp.LineNum)
		}
	}

	// --- Finalize Layout Byte ---
	finalLayout := uint8(0)
	if el.LayoutFlagsSource != 0 {
		finalLayout = el.LayoutFlagsSource
	} else if el.StyleID > 0 && int(el.StyleID-1) < len(state.Styles) {
		st := &state.Styles[el.StyleID-1]
		layoutFoundInStyle := false
		for _, prop := range st.Properties {
			if prop.PropertyID == PropIDLayoutFlags && prop.ValueType == ValTypeByte && prop.Size == 1 {
				finalLayout = prop.Value[0]
				layoutFoundInStyle = true
				break
			}
		}
		if !layoutFoundInStyle && el.IsComponentInstance {
			if el.OrientationHint == "column" || el.OrientationHint == "col" {
				finalLayout = LayoutDirectionColumn | LayoutAlignmentStart
			} else {
				finalLayout = LayoutDirectionRow | LayoutAlignmentStart
			}
		}
	} else if el.IsComponentInstance {
		if el.OrientationHint == "column" || el.OrientationHint == "col" {
			finalLayout = LayoutDirectionColumn | LayoutAlignmentStart
		} else {
			finalLayout = LayoutDirectionRow | LayoutAlignmentStart
		}
	} else {
		finalLayout = LayoutDirectionRow | LayoutAlignmentStart
	}
	el.Layout = finalLayout

	// --- Recursively Resolve Children ---
	el.Children = make([]*Element, 0, len(el.SourceChildrenIndices))
	el.ChildCount = 0
	for _, childIndex := range el.SourceChildrenIndices {
		if childIndex < 0 || childIndex >= len(state.Elements) {
			return fmt.Errorf("L%d: internal error: invalid child index %d found for element '%s'", el.SourceLineNum, childIndex, el.SourceElementName)
		}
		err := state.resolveElementRecursive(childIndex)
		if err != nil {
			return fmt.Errorf("failed resolving child %d for element %d ('%s'): %w", childIndex, elementIndex, el.SourceElementName, err)
		}
		el.Children = append(el.Children, &state.Elements[childIndex])
	}
	el.ChildCount = uint8(len(el.Children))

	return nil // Success for this element
}

// resolveComponentsAndProperties is the entry point for pass 1.5.
func (state *CompilerState) resolveComponentsAndProperties() error {
	log.Println("Pass 1.5: Expanding components and resolving properties...")
	for i := range state.Elements {
		state.Elements[i].ProcessedInPass15 = false
	}
	processedCount := 0
	for i := range state.Elements {
		if !state.Elements[i].ProcessedInPass15 {
			if err := state.resolveElementRecursive(i); err != nil {
				return fmt.Errorf("error resolving element tree starting at index %d ('%s'): %w", i, state.Elements[i].SourceElementName, err)
			}
		}
		currentProcessed := 0
		for k := range state.Elements {
			if state.Elements[k].ProcessedInPass15 {
				currentProcessed++
			}
		}
		processedCount = currentProcessed
		if processedCount == len(state.Elements) {
			break
		}
	}
	if processedCount != len(state.Elements) {
		log.Printf("Warning: %d elements processed, but total elements is %d. Potential disconnected elements.\n", processedCount, len(state.Elements))
		for i := range state.Elements {
			if !state.Elements[i].ProcessedInPass15 {
				log.Printf("Attempting to resolve potentially disconnected element %d ('%s')\n", i, state.Elements[i].SourceElementName)
				if err := state.resolveElementRecursive(i); err != nil {
					return fmt.Errorf("error resolving unprocessed element %d ('%s'): %w", i, state.Elements[i].SourceElementName, err)
				}
			}
		}
	}
	log.Printf("   Resolution Pass Complete. Final Element count: %d\n", len(state.Elements))
	return nil
}

// Helper for ComponentDefinition to get a root property value
func (def *ComponentDefinition) getRootPropertyValue(key string) (string, bool) {
	for i := len(def.DefinitionRootProperties) - 1; i >= 0; i-- {
		if def.DefinitionRootProperties[i].Key == key {
			return def.DefinitionRootProperties[i].ValueStr, true
		}
	}
	return "", false
}

// Helper function to log unhandled property warnings consistently
func logUnhandledPropWarning(el *Element, key string, lineNum int) {
	// Avoid warning about hints or already processed header fields again
	switch key {
	case "orientation", "position", "id", "style", "pos_x", "pos_y", "width", "height", "layout", "onClick":
		return
	}
	if el.IsComponentInstance && el.ComponentDef != nil {
		hintFound := false
		for _, propDef := range el.ComponentDef.Properties {
			if propDef.Name == key {
				hintFound = true
				break
			}
		}
		if hintFound {
			log.Printf("L%d: Info: Property '%s' (defined in component '%s') not directly mapped to standard KRB property for element '%s'.\n", lineNum, key, el.ComponentDef.Name, el.SourceElementName)
		} else {
			log.Printf("L%d: Warning: Unhandled property '%s' for element '%s' (component instance '%s').\n", lineNum, key, el.SourceElementName, el.ComponentDef.Name)
		}
	} else {
		log.Printf("L%d: Warning: Unhandled property '%s' for element '%s'.\n", lineNum, key, el.SourceElementName)
	}
}

// --- Pass 1.7: Adjust Layout (Ignoring Position) ---
func (state *CompilerState) adjustLayoutForPosition() error {
	log.Println("Pass 1.7: Adjust Layout for 'position' (Skipped - Renderer Handled).")
	return nil
}