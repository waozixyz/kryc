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
func (state *CompilerState) resolveElementRecursive(elementIndex int) error {
	if elementIndex < 0 || elementIndex >= len(state.Elements) {
		return fmt.Errorf("internal error: invalid element index %d during resolution", elementIndex)
	}
	el := &state.Elements[elementIndex] // Use pointer to modify

	if el.ProcessedInPass15 {
		return nil // Already processed this element in this pass
	}
	el.ProcessedInPass15 = true // Mark early to prevent infinite recursion

	var originalSourceProperties []SourceProperty

	// --- 1. Expand Component (if it is one) ---
	if el.Type == ElemTypeInternalComponentUsage {
		if el.ComponentDef == nil {
			return fmt.Errorf("L%d: internal error: component instance '%s' has nil definition", el.SourceLineNum, el.SourceElementName)
		}
		def := el.ComponentDef

		// Save the properties applied *at the usage site*
		originalSourceProperties = make([]SourceProperty, len(el.SourceProperties))
		copy(originalSourceProperties, el.SourceProperties)

		// Determine the base element type from the definition
		rootType := def.DefinitionRootType
		if rootType == "" {
			// Default to Container if not specified, or error? Let's default for now.
			rootType = "Container"
			log.Printf("L%d: Info: Component '%s' definition has no root element type, defaulting to 'Container'.", def.DefinitionStartLine, def.Name)
		}
		el.Type = getElementTypeFromName(rootType) // Update element type
		if el.Type == ElemTypeUnknown {
			el.Type = ElemTypeCustomBase // Treat as custom if name not recognized
			nameIdx, err := state.addString(rootType)
			if err != nil { return fmt.Errorf("L%d: failed adding component root type name '%s': %w", el.SourceLineNum, rootType, err) }
			el.IDStringIndex = nameIdx // Store name index for custom type identification
			log.Printf("L%d: Info: Component '%s' expands to unknown root type '%s', using custom type 0x%X\n", el.SourceLineNum, def.Name, rootType, el.Type)
		}
		el.IsComponentInstance = true // Keep this flag, might be useful

		// --- Merge Properties ---
		// Start with definition's root properties, then apply defaults, then usage properties override all.
		mergedProps := make(map[string]SourceProperty)

		// 1. Apply properties defined on the root element *inside* the Define block
		for _, prop := range def.DefinitionRootProperties {
			mergedProps[prop.Key] = prop
		}

		// 2. Apply defaults from the 'Properties' block if not already set
		propHints := make(map[string]uint8) // Store type hints for later validation if needed
		for _, propDef := range def.Properties {
			propHints[propDef.Name] = propDef.ValueTypeHint
			if _, exists := mergedProps[propDef.Name]; !exists && propDef.DefaultValueStr != "" {

				// --- ** FIX: Strip comment from default value before applying ** ---
				defaultValue := propDef.DefaultValueStr
				trimmedDefaultBeforeCommentCheck := strings.TrimSpace(defaultValue)
				commentIndexDef := -1; inQuotesDef := false
				for i, r := range trimmedDefaultBeforeCommentCheck {
					if r == '"' { inQuotesDef = !inQuotesDef }
					if r == '#' && !inQuotesDef { commentIndexDef = i; break }
				}
				finalDefaultValue := ""
				if commentIndexDef != -1 {
					if commentIndexDef == 0 { finalDefaultValue = trimmedDefaultBeforeCommentCheck } else { finalDefaultValue = trimmedDefaultBeforeCommentCheck[:commentIndexDef] }
				} else { finalDefaultValue = trimmedDefaultBeforeCommentCheck }
				finalDefaultValue = strings.TrimSpace(finalDefaultValue) // Final trim
				// --- ** END FIX ** ---

				// Apply the cleaned default value
				mergedProps[propDef.Name] = SourceProperty{
					Key:      propDef.Name,
					ValueStr: finalDefaultValue, // Use cleaned value
					LineNum:  def.DefinitionStartLine,
				}
				log.Printf("L%d: Applying default value '%s' for property '%s' in component '%s'\n", el.SourceLineNum, finalDefaultValue, propDef.Name, def.Name)
			}
		}

		// 3. Apply properties from the component usage site (overrides previous)
		for _, prop := range originalSourceProperties {
			mergedProps[prop.Key] = prop
		}

		// Replace the element's source properties with the final merged set
		el.SourceProperties = make([]SourceProperty, 0, len(mergedProps))
		for _, prop := range mergedProps {
			el.SourceProperties = append(el.SourceProperties, prop)
		}

		// --- Handle Component-Specific Hints (Orientation/Position) and Default Style ---
		// These hints are processed *after* merging properties
		barStyleStr, _ := el.getSourcePropertyValue("bar_style") // Component specific prop
		directStyleStr, _ := el.getSourcePropertyValue("style")   // Standard style prop

		// Set hints based on final merged values or defaults
		el.OrientationHint = "row" // Default
		if v, ok := el.getSourcePropertyValue("orientation"); ok { el.OrientationHint = trimQuotes(strings.TrimSpace(v))}
		el.PositionHint = "bottom" // Default
		if v, ok := el.getSourcePropertyValue("position"); ok { el.PositionHint = trimQuotes(strings.TrimSpace(v))}

		// Determine the final StyleID to apply (complex logic based on component)
		finalStyleID := uint8(0)
		if barStyleStr != "" { // 1. Check component-specific 'bar_style' first
			cleanBarStyle := trimQuotes(strings.TrimSpace(barStyleStr))
			finalStyleID = state.findStyleIDByName(cleanBarStyle)
			if finalStyleID == 0 { log.Printf("L%d: Warning: Component property bar_style '%s' not found for '%s'.\n", el.SourceLineNum, cleanBarStyle, def.Name) }
		} else if directStyleStr != "" { // 2. Check standard 'style' property on usage
		    cleanDirectStyle := trimQuotes(strings.TrimSpace(directStyleStr))
			finalStyleID = state.findStyleIDByName(cleanDirectStyle)
			if finalStyleID == 0 { log.Printf("L%d: Warning: Explicit style '%s' not found for component '%s'.\n", el.SourceLineNum, cleanDirectStyle, def.Name) }
		} else { // 3. Check for 'style' on the root element inside the Define block
			defRootStyle, hasDefRootStyle := def.getRootPropertyValue("style")
			if hasDefRootStyle {
			    cleanDefRootStyle := trimQuotes(strings.TrimSpace(defRootStyle))
				finalStyleID = state.findStyleIDByName(cleanDefRootStyle)
				if finalStyleID == 0 { log.Printf("L%d: Warning: Component definition root style '%s' not found for '%s'.\n", def.DefinitionStartLine, cleanDefRootStyle, def.Name) }
			} else { // 4. Apply component's default orientation-based style
                 if def.Name == "TabBar" { // Example: Specific logic for TabBar component
                     baseStyleName := "tab_bar_style_base_row"
                     if el.OrientationHint == "column" || el.OrientationHint == "col" {
                          baseStyleName = "tab_bar_style_base_column"
                     }
                     finalStyleID = state.findStyleIDByName(baseStyleName)
                     if finalStyleID == 0 { log.Printf("L%d: Warning: Default component style '%s' not found for '%s'.\n", el.SourceLineNum, baseStyleName, def.Name)}
                 }
                 // Add logic for other components if they have default styles
            }
		}
		el.StyleID = finalStyleID // Set the final determined StyleID

		// Determine initial LayoutFlagsSource based on orientation hint for components like TabBar
        // This might be overridden by an explicit 'layout:' property later
        el.LayoutFlagsSource = 0 // Reset
        if def.Name == "TabBar" {
            el.LayoutFlagsSource = LayoutDirectionRow | LayoutAlignmentStart // Default for row
            if el.OrientationHint == "column" || el.OrientationHint == "col" {
                el.LayoutFlagsSource = LayoutDirectionColumn | LayoutAlignmentStart
            }
        }
        // Add logic for other components that imply layout

	} // --- End of Component Expansion ---

	// --- 2. Resolve Standard Header Fields (ID, Pos, Size) ---
	// These might be set directly or come from merged properties if expanded
	el.KrbProperties = el.KrbProperties[:0] // Clear previous KRB properties before resolving
	el.PropertyCount = 0
	el.KrbEvents = el.KrbEvents[:0] // Clear previous events
	el.EventCount = 0
	// Note: StyleID was potentially set during component expansion, process 'style:' prop again to allow override

	processedSourcePropKeys := make(map[string]bool)

	for _, sp := range el.SourceProperties {
		key := sp.Key
		valStr := sp.ValueStr // Original string value
		lineNum := sp.LineNum

		// --- Strip comments for parsing non-string values ---
        valuePart := valStr
        trimmedValueBeforeCommentCheck := strings.TrimSpace(valuePart)
        commentIndex := -1; inQuotes := false
        for i, r := range trimmedValueBeforeCommentCheck {
            if r == '"' { inQuotes = !inQuotes }
            if r == '#' && !inQuotes { commentIndex = i; break }
        }
        if commentIndex != -1 {
            if commentIndex == 0 { valuePart = trimmedValueBeforeCommentCheck } else { valuePart = trimmedValueBeforeCommentCheck[:commentIndex] }
        } else { valuePart = trimmedValueBeforeCommentCheck }
        cleanVal := strings.TrimSpace(valuePart) // Value used for parsing numbers, enums etc.
        quotedVal := trimQuotes(cleanVal) // Value with quotes removed, used for string lookups
        // --- End comment stripping ---


		handled := true // Assume handled as header/special field unless switched
		var parseErr error

		switch key {
		case "id":
			if quotedVal != "" { // Use quote-trimmed value for ID lookup
				idIdx, err := state.addString(quotedVal) // Add the clean ID string
				if err != nil { parseErr = err } else { el.IDStringIndex = idIdx; el.SourceIDName = quotedVal }
			} else { log.Printf("L%d: Warning: Empty 'id' ignored", lineNum) }
		case "style": // Handle standard 'style' property - potentially overrides component logic
			if quotedVal != "" { // Use quote-trimmed value for style name lookup
				sid := state.findStyleIDByName(quotedVal)
				if sid == 0 { log.Printf("L%d: Warning: Style '%s' not found", lineNum, quotedVal) }
				el.StyleID = sid // Set/Override StyleID based on direct 'style:' prop
			}
		case "pos_x":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.PosX = uint16(v) } else { parseErr = fmt.Errorf("invalid pos_x uint16 '%s': %w", cleanVal, err) }
		case "pos_y":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.PosY = uint16(v) } else { parseErr = fmt.Errorf("invalid pos_y uint16 '%s': %w", cleanVal, err) }
		case "width":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.Width = uint16(v) } else { parseErr = fmt.Errorf("invalid width uint16 '%s': %w", cleanVal, err) }
		case "height":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.Height = uint16(v) } else { parseErr = fmt.Errorf("invalid height uint16 '%s': %w", cleanVal, err) }
        case "orientation", "position", "bar_style": // Component hints/props - consume them
             // Already processed during component expansion if needed
             handled = true // Mark as handled so they don't become unhandled warnings
		default:
			handled = false // Mark as needing processing as a standard KRB property or event
		}

		if parseErr != nil {
			return fmt.Errorf("L%d: error processing header/special field '%s': %w", lineNum, key, parseErr)
		}
		if handled {
			processedSourcePropKeys[key] = true // Mark as processed
		}
	}

	// --- 3. Resolve Remaining Source Properties into KRB Properties/Events ---
	for _, sp := range el.SourceProperties {
		key := sp.Key
		valStr := sp.ValueStr // Original string value
		lineNum := sp.LineNum

		if processedSourcePropKeys[key] { continue } // Skip if already handled

		// --- Strip comments for parsing ---
        valuePart := valStr
        trimmedValueBeforeCommentCheck := strings.TrimSpace(valuePart)
        commentIndex := -1; inQuotes := false
        for i, r := range trimmedValueBeforeCommentCheck {
            if r == '"' { inQuotes = !inQuotes }
            if r == '#' && !inQuotes { commentIndex = i; break }
        }
        if commentIndex != -1 {
             if commentIndex == 0 { valuePart = trimmedValueBeforeCommentCheck } else { valuePart = trimmedValueBeforeCommentCheck[:commentIndex] }
        } else { valuePart = trimmedValueBeforeCommentCheck }
        cleanVal := strings.TrimSpace(valuePart) // Value for parsing numbers, enums etc.
        quotedVal := trimQuotes(cleanVal) // Value for string/resource lookups
        // --- End comment stripping ---


		// --- Handle Layout Property ---
		if key == "layout" {
			layoutByte := parseLayoutString(cleanVal) // Use comment-stripped value
			el.LayoutFlagsSource = layoutByte   // Store the explicit source value
			processedSourcePropKeys[key] = true
			continue                            // 'layout' sets the source flag, doesn't become a KRB property itself
		}

		// --- Handle Event Properties ---
		if key == "onClick" || key == "on_click" { // Allow snake_case too
			if len(el.KrbEvents) >= MaxEvents { return fmt.Errorf("L%d: maximum events (%d) exceeded for element '%s'", lineNum, MaxEvents, el.SourceElementName) }
			callbackName := quotedVal // Use quote-trimmed value for function name
			if callbackName == "" {
				log.Printf("L%d: Warning: Empty callback name for '%s' ignored.\n", lineNum, key)
			} else {
				cbIdx, err := state.addString(callbackName) // Add the clean callback name string
				if err != nil { return fmt.Errorf("L%d: failed adding callback string '%s': %w", lineNum, callbackName, err) }
				event := KrbEvent{EventType: EventTypeClick, CallbackID: cbIdx}
				el.KrbEvents = append(el.KrbEvents, event)
				el.EventCount = uint8(len(el.KrbEvents))
			}
			processedSourcePropKeys[key] = true
			continue
		}
		// Add other event types (onChange, onSubmit, etc.) here if needed

		// --- Convert standard KRY props to KRB props ---
		var err error
		propAdded := true // Assume handled unless switched

		// Use cleanVal for parsing, quotedVal for string/resource lookups, valStr for raw string content
		switch key {
		case "background_color":
			if col, ok := parseColor(cleanVal); ok { if e := el.addKrbProperty(PropIDBgColor, ValTypeColor, col[:]); e == nil { state.HeaderFlags |= FlagExtendedColor } else { err = e } } else { err = fmt.Errorf("invalid color value '%s'", cleanVal) }
		case "text_color", "foreground_color":
			if col, ok := parseColor(cleanVal); ok { if e := el.addKrbProperty(PropIDFgColor, ValTypeColor, col[:]); e == nil { state.HeaderFlags |= FlagExtendedColor } else { err = e } } else { err = fmt.Errorf("invalid color value '%s'", cleanVal) }
		case "border_color":
			if col, ok := parseColor(cleanVal); ok { if e := el.addKrbProperty(PropIDBorderColor, ValTypeColor, col[:]); e == nil { state.HeaderFlags |= FlagExtendedColor } else { err = e } } else { err = fmt.Errorf("invalid color value '%s'", cleanVal) }
		case "border_width":
			if bw, e := strconv.ParseUint(cleanVal, 10, 8); e == nil { err = el.addKrbProperty(PropIDBorderWidth, ValTypeByte, []byte{uint8(bw)}) } else { err = fmt.Errorf("invalid border_width uint8 '%s': %w", cleanVal, e) }
		case "border_radius":
			if br, e := strconv.ParseUint(cleanVal, 10, 8); e == nil { err = el.addKrbProperty(PropIDBorderRadius, ValTypeByte, []byte{uint8(br)}) } else { err = fmt.Errorf("invalid border_radius uint8 '%s': %w", cleanVal, e) }
		case "text", "content":
			err = state.addKrbStringProperty(el, PropIDTextContent, valStr) // Use original valStr to preserve quotes
		case "image_source", "source":
			if el.Type == ElemTypeImage || el.Type == ElemTypeButton { err = state.addKrbResourceProperty(el, PropIDImageSource, ResTypeImage, quotedVal) } else { log.Printf("L%d: Warning: Property '%s' ignored for element type %d ('%s').\n", lineNum, key, el.Type, el.SourceElementName); propAdded = false }
		case "font_size":
			if fs, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && fs > 0 && fs <= math.MaxUint16 { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(fs)); err = el.addKrbProperty(PropIDFontSize, ValTypeShort, buf) } else if e != nil { err = fmt.Errorf("invalid font_size uint16 '%s': %w", cleanVal, e) } else { err = fmt.Errorf("font_size '%s' out of range (1-%d)", cleanVal, math.MaxUint16) }
		case "text_alignment":
			align := uint8(0); switch cleanVal { case "center", "centre": align = 1; case "right", "end": align = 2; case "left", "start": align = 0; default: log.Printf("L%d: Warning: Invalid text_alignment '%s', defaulting to left.\n", lineNum, cleanVal) }; err = el.addKrbProperty(PropIDTextAlignment, ValTypeEnum, []byte{align})
		case "gap":
			if g, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && g <= math.MaxUint16 { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(g)); err = el.addKrbProperty(PropIDGap, ValTypeShort, buf) } else if e != nil { err = fmt.Errorf("invalid gap uint16 '%s': %w", cleanVal, e) } else { err = fmt.Errorf("gap '%s' out of range (0-%d)", cleanVal, math.MaxUint16) }
		// --- App specific properties ---
		case "window_width": if el.Type == ElemTypeApp { if v, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && v > 0 && v <= math.MaxUint16 { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(v)); err = el.addKrbProperty(PropIDWindowWidth, ValTypeShort, buf) } else if e != nil { err = fmt.Errorf("invalid window_width '%s': %w", cleanVal, e) } else { err = fmt.Errorf("window_width '%s' out of range", cleanVal) } } else { propAdded = false }
		case "window_height": if el.Type == ElemTypeApp { if v, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && v > 0 && v <= math.MaxUint16 { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(v)); err = el.addKrbProperty(PropIDWindowHeight, ValTypeShort, buf) } else if e != nil { err = fmt.Errorf("invalid window_height '%s': %w", cleanVal, e) } else { err = fmt.Errorf("window_height '%s' out of range", cleanVal) } } else { propAdded = false }
		case "window_title": if el.Type == ElemTypeApp { err = state.addKrbStringProperty(el, PropIDWindowTitle, valStr) } else { propAdded = false } // Use original valStr
		case "resizable": if el.Type == ElemTypeApp { resBool := (cleanVal == "true"); valByte := uint8(0); if resBool { valByte = 1 }; err = el.addKrbProperty(PropIDResizable, ValTypeByte, []byte{valByte}) } else { propAdded = false }
		case "icon": if el.Type == ElemTypeApp { err = state.addKrbResourceProperty(el, PropIDIcon, ResTypeImage, quotedVal) } else { propAdded = false } // Use quotedVal for path
		case "version": if el.Type == ElemTypeApp { err = state.addKrbStringProperty(el, PropIDVersion, valStr) } else { propAdded = false } // Use original valStr
		case "author": if el.Type == ElemTypeApp { err = state.addKrbStringProperty(el, PropIDAuthor, valStr) } else { propAdded = false } // Use original valStr
		case "keep_aspect": if el.Type == ElemTypeApp { keepAspectBool := (cleanVal == "true"); valByte := uint8(0); if keepAspectBool { valByte = 1 }; err = el.addKrbProperty(PropIDKeepAspect, ValTypeByte, []byte{valByte}) } else { propAdded = false }
		case "scale_factor": if el.Type == ElemTypeApp { if scaleF, e := strconv.ParseFloat(cleanVal, 64); e == nil { fixedPointVal := uint16(scaleF * 256.0); buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, fixedPointVal); err = el.addKrbProperty(PropIDScaleFactor, ValTypePercentage, buf); if err == nil { state.HeaderFlags |= FlagFixedPoint } } else { err = fmt.Errorf("invalid scale_factor float '%s': %w", cleanVal, e) } } else { propAdded = false }
		// --- End App Specific ---
        // --- Other Standard Properties ---
        case "opacity": if op, e := strconv.ParseUint(cleanVal, 10, 8); e == nil { err = el.addKrbProperty(PropIDOpacity, ValTypeByte, []byte{uint8(op)}) } else { err = fmt.Errorf("invalid opacity uint8 (0-255) '%s': %w", cleanVal, e) }
        case "visibility", "visible": visBool := false; switch strings.ToLower(cleanVal) { case "true", "visible", "1": visBool = true; case "false", "hidden", "0": visBool = false; default: err = fmt.Errorf("invalid boolean visibility '%s'", cleanVal) }; if err == nil { valByte := uint8(0); if visBool { valByte = 1 }; err = el.addKrbProperty(PropIDVisibility, ValTypeByte, []byte{valByte}) }
        case "z_index": if z, e := strconv.ParseUint(cleanVal, 10, 16); e == nil { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(z)); err = el.addKrbProperty(PropIDZindex, ValTypeShort, buf) } else { err = fmt.Errorf("invalid z_index uint16 '%s': %w", cleanVal, e) }
        // Add more standard KRB properties as needed (Padding, Margin, Min/Max Width/Height, AspectRatio, Transform, Shadow, Overflow...)

		default:
			propAdded = false // Mark as not handled by standard conversions
			// Log warning only if it wasn't handled previously (like component hints)
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

	// Final check for any unprocessed source properties (useful for debugging)
	// for _, sp := range el.SourceProperties {
	// 	if !processedSourcePropKeys[sp.Key] {
	// 		logUnhandledPropWarning(el, sp.Key, sp.LineNum) // Already logged if default case hit
	// 	}
	// }

	// --- 4. Finalize Layout Byte ---
	// Determine the effective layout byte, considering explicit layout, style, or component defaults
	finalLayout := uint8(0)
	if el.LayoutFlagsSource != 0 { // Explicit layout: property takes highest precedence
		finalLayout = el.LayoutFlagsSource
	} else if el.StyleID > 0 && int(el.StyleID-1) < len(state.Styles) { // Check applied style
		st := &state.Styles[el.StyleID-1]
		layoutFoundInStyle := false
		for _, prop := range st.Properties { // Use resolved properties from style
			if prop.PropertyID == PropIDLayoutFlags && prop.ValueType == ValTypeByte && prop.Size == 1 {
				finalLayout = prop.Value[0]
				layoutFoundInStyle = true
				break
			}
		}
		// If style didn't define layout, fall back to component hints or global default
		if !layoutFoundInStyle {
            if el.IsComponentInstance && el.LayoutFlagsSource != 0 { // Check if component expansion set a default
                 finalLayout = el.LayoutFlagsSource
            } else {
                 finalLayout = LayoutDirectionRow | LayoutAlignmentStart // Global default
            }
		}
	} else if el.IsComponentInstance && el.LayoutFlagsSource != 0 { // Component hint if no style applied/defined layout
         finalLayout = el.LayoutFlagsSource
    } else { // Global default if nothing else specifies
		finalLayout = LayoutDirectionRow | LayoutAlignmentStart
	}
	el.Layout = finalLayout // Set the final Layout byte in the element header

	// --- 5. Recursively Resolve Children ---
	el.Children = make([]*Element, 0, len(el.SourceChildrenIndices)) // Reset Children slice
	el.ChildCount = 0
	for _, childIndex := range el.SourceChildrenIndices {
		if childIndex < 0 || childIndex >= len(state.Elements) {
			return fmt.Errorf("L%d: internal error: invalid child index %d found for element '%s'", el.SourceLineNum, childIndex, el.SourceElementName)
		}
		// Recursively call this function for each child
		err := state.resolveElementRecursive(childIndex)
		if err != nil {
			// Add context about the parent when reporting child resolution errors
			return fmt.Errorf("failed resolving child %d (of element %d '%s'): %w", childIndex, elementIndex, el.SourceElementName, err)
		}
		// Add the processed child to the parent's resolved children list
		el.Children = append(el.Children, &state.Elements[childIndex])
	}
	el.ChildCount = uint8(len(el.Children)) // Update final child count

	return nil // Success for this element and its subtree
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
