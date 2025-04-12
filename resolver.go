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

// resolveElementRecursive is the core function that expands components (if any)
// and resolves .kry source properties into standard KRB properties, custom KRB properties,
// or events for a single element and its children.
func (state *CompilerState) resolveElementRecursive(elementIndex int) error {
	if elementIndex < 0 || elementIndex >= len(state.Elements) {
		return fmt.Errorf("internal error: invalid element index %d during resolution", elementIndex)
	}
	el := &state.Elements[elementIndex] // Use pointer to modify the element in the main slice

	if el.ProcessedInPass15 {
		return nil
	}
	el.ProcessedInPass15 = true // Mark as visited for this pass

	var originalSourceProperties []SourceProperty // Store original props before component merge
	var componentDef *ComponentDefinition = nil    // Store the definition if this is a component instance

	// --- Step 1: Expand Component (if this element is a component usage) ---
	if el.Type == ElemTypeInternalComponentUsage {
		// ... (Component Expansion Logic - Stays the Same) ...
		if el.ComponentDef == nil { return fmt.Errorf("L%d: internal error: component instance '%s' has nil definition", el.SourceLineNum, el.SourceElementName) }
		componentDef = el.ComponentDef
		originalSourceProperties = make([]SourceProperty, len(el.SourceProperties)); copy(originalSourceProperties, el.SourceProperties)
		rootType := componentDef.DefinitionRootType; if rootType == "" { log.Printf("L%d: Warning: Component definition '%s' has no root element type specified. Defaulting to 'Container'.\n", componentDef.DefinitionStartLine, componentDef.Name); rootType = "Container" }
		el.Type = getElementTypeFromName(rootType);
		if el.Type == ElemTypeUnknown { el.Type = ElemTypeCustomBase; nameIdx, err := state.addString(rootType); if err != nil { return fmt.Errorf("L%d: failed adding component root type name '%s': %w", el.SourceLineNum, rootType, err) }; el.IDStringIndex = nameIdx; log.Printf("L%d: Info: Component '%s' expands to unknown root type '%s', using custom type 0x%X\n", el.SourceLineNum, componentDef.Name, rootType, el.Type) }
		el.IsComponentInstance = true;
		mergedPropsMap := make(map[string]SourceProperty);
		for _, prop := range componentDef.DefinitionRootProperties { mergedPropsMap[prop.Key] = prop };
		for _, propDef := range componentDef.Properties { if _, exists := mergedPropsMap[propDef.Name]; !exists && propDef.DefaultValueStr != "" { cleanDefaultVal, _ := cleanAndQuoteValue(propDef.DefaultValueStr); mergedPropsMap[propDef.Name] = SourceProperty{ Key: propDef.Name, ValueStr: cleanDefaultVal, LineNum: componentDef.DefinitionStartLine, } } };
		for _, prop := range originalSourceProperties { mergedPropsMap[prop.Key] = prop };
		el.SourceProperties = make([]SourceProperty, 0, len(mergedPropsMap)); for _, prop := range mergedPropsMap { el.SourceProperties = append(el.SourceProperties, prop) };
		el.OrientationHint = ""; el.PositionHint = "";
		if orientationVal, ok := mergedPropsMap["orientation"]; ok { cleanOrientation := trimQuotes(strings.TrimSpace(orientationVal.ValueStr)); if cleanOrientation != "" { el.OrientationHint = cleanOrientation } };
		if positionVal, ok := mergedPropsMap["position"]; ok { cleanPosition := trimQuotes(strings.TrimSpace(positionVal.ValueStr)); if cleanPosition != "" { el.PositionHint = cleanPosition } };
	}

	// --- Step 2: Reset Resolved Data & Process Standard Header Fields from Merged/Original Source Properties ---
	el.KrbProperties = el.KrbProperties[:0]; el.PropertyCount = 0
	el.KrbCustomProperties = el.KrbCustomProperties[:0]; el.CustomPropCount = 0
	el.KrbEvents = el.KrbEvents[:0]; el.EventCount = 0
	el.LayoutFlagsSource = 0 // Reset layout hint from source
	processedSourcePropKeys := make(map[string]bool) // Track keys handled here or by style logic

	// --- Determine Final Style ID BEFORE processing properties ---
	el.StyleID = 0 // Reset StyleID
	// *** FIX: Correctly handle 'style' property lookup using cleaned value ***
	directStyleStr, hasDirectStyle := el.getSourcePropertyValue("style")
	if hasDirectStyle {
		// Clean the value HERE before looking it up
		cleanDirectStyle, _ := cleanAndQuoteValue(directStyleStr) // Use helper
		if cleanDirectStyle != "" {
			// Lookup using the cleaned name
			el.StyleID = state.findStyleIDByName(cleanDirectStyle)
			if el.StyleID == 0 {
				// Use original string (with quotes) in warning for clarity
				log.Printf("L%d: Warning: Style %s not found for element '%s'.\n", el.SourceLineNum, directStyleStr, el.SourceElementName)
			}
		}
		processedSourcePropKeys["style"] = true // Mark style as processed
	}

	// If it's a component, potentially override StyleID with bar_style or defaults
	if el.IsComponentInstance && componentDef != nil {
		barStyleStr, hasBarStyle := el.getSourcePropertyValue("bar_style")
		if hasBarStyle {
			cleanBarStyle, _ := cleanAndQuoteValue(barStyleStr) // Clean value
			if cleanBarStyle != "" {
				styleIDFromBar := state.findStyleIDByName(cleanBarStyle)
				if styleIDFromBar == 0 {
					log.Printf("L%d: Warning: Component property bar_style %s not found for '%s'.\n", el.SourceLineNum, barStyleStr, componentDef.Name)
				} else {
					el.StyleID = styleIDFromBar // Override with bar_style if found
					log.Printf("L%d: Debug: Using bar_style %s (ID %d) for component '%s'.\n", el.SourceLineNum, barStyleStr, el.StyleID, componentDef.Name)
				}
			} else {
				log.Printf("L%d: Debug: Empty bar_style property provided for component '%s'.\n", el.SourceLineNum, componentDef.Name)
			}
			processedSourcePropKeys["bar_style"] = true // Mark bar_style as processed
		}

		// Apply default component style ONLY if no style was found via direct 'style:' or 'bar_style:'
		if el.StyleID == 0 {
			if componentDef.Name == "TabBar" {
				baseStyleName := "tab_bar_style_base_row" // Default to row
				if el.OrientationHint == "column" || el.OrientationHint == "col" {
					baseStyleName = "tab_bar_style_base_column"
				}
				el.StyleID = state.findStyleIDByName(baseStyleName)
				if el.StyleID == 0 {
					log.Printf("L%d: Warning: Default component style '%s' not found for '%s'.\n", el.SourceLineNum, baseStyleName, componentDef.Name)
				} else {
					log.Printf("L%d: Debug: Applying default style '%s' (ID %d) for component '%s'.\n", el.SourceLineNum, baseStyleName, el.StyleID, componentDef.Name)
				}
			}
			// Add default style lookups for other components here if needed
		}
	}
	// el.StyleID now holds the final resolved style ID (or 0)


	// --- Process Header Fields from Source Properties ---
	for _, sp := range el.SourceProperties {
		key := sp.Key; valStr := sp.ValueStr; lineNum := sp.LineNum
		// Skip keys already handled by style logic
		if processedSourcePropKeys[key] { continue }

		cleanVal, quotedVal := cleanAndQuoteValue(valStr)
		handledAsHeader := true; var parseErr error
		switch key {
		case "id":    if quotedVal != "" { idIdx, err := state.addString(quotedVal); if err == nil { el.IDStringIndex = idIdx; el.SourceIDName = quotedVal } else { parseErr = err } }
		case "pos_x": if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.PosX = uint16(v) } else { parseErr = fmt.Errorf("pos_x uint16 '%s': %w", cleanVal, err) }
		case "pos_y": if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.PosY = uint16(v) } else { parseErr = fmt.Errorf("pos_y uint16 '%s': %w", cleanVal, err) }
		case "width": if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.Width = uint16(v) } else { parseErr = fmt.Errorf("width uint16 '%s': %w", cleanVal, err) }
		case "height":if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.Height = uint16(v) } else { parseErr = fmt.Errorf("height uint16 '%s': %w", cleanVal, err) }
		case "layout":el.LayoutFlagsSource = parseLayoutString(cleanVal) // Store hint from source
		default:      handledAsHeader = false
		}
		if parseErr != nil { return fmt.Errorf("L%d: error processing header field '%s': %w", lineNum, key, parseErr) }
		if handledAsHeader { processedSourcePropKeys[key] = true }
	}

	// --- Step 3: Resolve Remaining Properties (Standard KRB, Custom KRB, Events) ---
	// ... (Rest of Step 3 - Standard/Custom/Event Property handling - Stays the Same) ...
	for _, sp := range el.SourceProperties {
		key := sp.Key; valStr := sp.ValueStr; lineNum := sp.LineNum
		if processedSourcePropKeys[key] { continue } // Skip if handled as header/style field

		cleanVal, quotedVal := cleanAndQuoteValue(valStr)
		propProcessed := false; var err error

		// A. Check Events
		if key == "onClick" || key == "on_click" {
			propProcessed = true; processedSourcePropKeys[key] = true // Mark handled
			if len(el.KrbEvents) < MaxEvents { if quotedVal != "" { cbIdx, addErr := state.addString(quotedVal); if addErr == nil { el.KrbEvents = append(el.KrbEvents, KrbEvent{EventTypeClick, cbIdx}); el.EventCount = uint8(len(el.KrbEvents)) } else { err = fmt.Errorf("add onClick cb '%s': %w", quotedVal, addErr) } } else { log.Printf("L%d: Warn: Empty callback name for '%s' ignored.\n", lineNum, key) } } else { err = fmt.Errorf("max events (%d)", MaxEvents) }
		}

		// B. Check Standard KRB Properties (if not handled as event)
		if !propProcessed {
			propAddedAsStandard := true
			switch key {
			case "background_color": err = addColorProp(el, PropIDBgColor, quotedVal, &state.HeaderFlags)
			case "text_color", "foreground_color": err = addColorProp(el, PropIDFgColor, quotedVal, &state.HeaderFlags)
			case "border_color": err = addColorProp(el, PropIDBorderColor, quotedVal, &state.HeaderFlags)
			case "border_width": err = addByteProp(el, PropIDBorderWidth, cleanVal)
			case "border_radius": err = addByteProp(el, PropIDBorderRadius, cleanVal)
			case "opacity": err = addByteProp(el, PropIDOpacity, cleanVal)
			case "visibility", "visible": vis := uint8(1); switch strings.ToLower(cleanVal) { case "true", "visible", "1": vis = 1; case "false", "hidden", "0": vis = 0; default: err = fmt.Errorf("invalid bool '%s'", cleanVal) }; if err == nil { err = el.addKrbProperty(PropIDVisibility, ValTypeByte, []byte{vis}) }
			case "z_index": err = addShortProp(el, PropIDZindex, cleanVal)
			case "transform": err = state.addKrbStringProperty(el, PropIDTransform, quotedVal)
			case "shadow": err = state.addKrbStringProperty(el, PropIDShadow, quotedVal)
			case "text", "content": err = state.addKrbStringProperty(el, PropIDTextContent, quotedVal)
			case "font_size": err = addShortProp(el, PropIDFontSize, cleanVal)
			case "font_weight": weight := uint8(0); if cleanVal == "bold" || cleanVal == "700" { weight = 1 }; err = el.addKrbProperty(PropIDFontWeight, ValTypeEnum, []byte{weight})
			case "text_alignment": align := uint8(0); switch cleanVal { case "center", "centre": align = 1; case "right", "end": align = 2; default: align = 0 }; err = el.addKrbProperty(PropIDTextAlignment, ValTypeEnum, []byte{align})
			case "gap": err = addShortProp(el, PropIDGap, cleanVal)
			case "padding": err = addEdgeInsetsProp(el, PropIDPadding, cleanVal)
			case "margin": err = addEdgeInsetsProp(el, PropIDMargin, cleanVal)
			case "min_width": err = addShortProp(el, PropIDMinWidth, cleanVal)
			case "min_height": err = addShortProp(el, PropIDMinHeight, cleanVal)
			case "max_width": err = addShortProp(el, PropIDMaxWidth, cleanVal)
			case "max_height": err = addShortProp(el, PropIDMaxHeight, cleanVal)
			case "aspect_ratio": err = addFixedPointProp(el, PropIDAspectRatio, cleanVal, &state.HeaderFlags)
			case "overflow": ovf := uint8(0); switch cleanVal { case "hidden": ovf = 1; case "scroll": ovf = 2; default: ovf = 0 }; err = el.addKrbProperty(PropIDOverflow, ValTypeEnum, []byte{ovf})
			case "image_source", "source": if el.Type == ElemTypeImage || el.Type == ElemTypeButton || el.IsComponentInstance { err = state.addKrbResourceProperty(el, PropIDImageSource, ResTypeImage, quotedVal) } else { propAddedAsStandard = false; }
			case "window_width": if el.Type == ElemTypeApp { err = addShortProp(el, PropIDWindowWidth, cleanVal) } else { propAddedAsStandard = false }
			case "window_height": if el.Type == ElemTypeApp { err = addShortProp(el, PropIDWindowHeight, cleanVal) } else { propAddedAsStandard = false }
			case "window_title": if el.Type == ElemTypeApp { err = state.addKrbStringProperty(el, PropIDWindowTitle, quotedVal) } else { propAddedAsStandard = false }
			case "resizable": if el.Type == ElemTypeApp { b := uint8(0); if cleanVal == "true" || cleanVal == "1" { b = 1 }; err = el.addKrbProperty(PropIDResizable, ValTypeByte, []byte{b}) } else { propAddedAsStandard = false }
			case "icon": if el.Type == ElemTypeApp { err = state.addKrbResourceProperty(el, PropIDIcon, ResTypeImage, quotedVal) } else { propAddedAsStandard = false }
			case "version": if el.Type == ElemTypeApp { err = state.addKrbStringProperty(el, PropIDVersion, quotedVal) } else { propAddedAsStandard = false }
			case "author": if el.Type == ElemTypeApp { err = state.addKrbStringProperty(el, PropIDAuthor, quotedVal) } else { propAddedAsStandard = false }
			case "keep_aspect": if el.Type == ElemTypeApp { b := uint8(0); if cleanVal == "true" || cleanVal == "1" { b = 1 }; err = el.addKrbProperty(PropIDKeepAspect, ValTypeByte, []byte{b}) } else { propAddedAsStandard = false }
			case "scale_factor": if el.Type == ElemTypeApp { err = addFixedPointProp(el, PropIDScaleFactor, cleanVal, &state.HeaderFlags) } else { propAddedAsStandard = false }
			default: propAddedAsStandard = false
			}
			if propAddedAsStandard { propProcessed = true; processedSourcePropKeys[key] = true; if err != nil { return fmt.Errorf("L%d: error processing standard property '%s': %w", lineNum, key, err) } }
		}

		// C. Check Custom Component Properties (if not handled as standard/event)
		if !propProcessed && el.IsComponentInstance && componentDef != nil {
			isDeclaredComponentProp := false; var propDefHint uint8 = ValTypeCustom
			for _, defProp := range componentDef.Properties { if defProp.Name == key { isDeclaredComponentProp = true; propDefHint = defProp.ValueTypeHint; break } }
			if isDeclaredComponentProp {
				propProcessed = true; processedSourcePropKeys[key] = true;
				keyIdx, keyErr := state.addString(key); if keyErr != nil { err = fmt.Errorf("add custom key '%s': %w", key, keyErr) }
				if err == nil {
					var customValue []byte; var customValueType uint8 = ValTypeString; var customValueSize uint8 = 1
					switch propDefHint {
					case ValTypeString, ValTypeStyleID, ValTypeResource: sIdx, sErr := state.addString(quotedVal); if sErr != nil { err = sErr } else { customValue = []byte{sIdx}; customValueType = ValTypeString; customValueSize = 1 }; if propDefHint == ValTypeResource { rType := guessResourceType(key); _, rErr := state.addResource(rType, quotedVal); if rErr != nil { err = rErr } }
					case ValTypeColor: col, ok := parseColor(quotedVal); if !ok { err = fmt.Errorf("invalid Color '%s'", quotedVal) } else { customValue = col[:]; customValueType = ValTypeColor; customValueSize = 4; state.HeaderFlags |= FlagExtendedColor }
					case ValTypeInt, ValTypeShort: v, pErr := strconv.ParseInt(cleanVal, 10, 16); if pErr != nil { err = fmt.Errorf("invalid Int '%s': %w", cleanVal, pErr) } else { if v < math.MinInt16 || v > math.MaxInt16 { err = fmt.Errorf("int value '%s' out of range for Short", cleanVal) } else { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(v)); customValue = buf; customValueType = ValTypeShort; customValueSize = 2 } }
					case ValTypeBool, ValTypeByte: b := uint8(0); lc := strings.ToLower(cleanVal); if lc == "true" || lc == "1" { b = 1 } else if lc != "false" && lc != "0" { log.Printf("L%d: Warn: Invalid Bool '%s' for '%s', using false.", lineNum, cleanVal, key) }; customValue = []byte{b}; customValueType = ValTypeByte; customValueSize = 1
					case ValTypeFloat: f, pErr := strconv.ParseFloat(cleanVal, 64); if pErr != nil { err = fmt.Errorf("invalid Float '%s': %w", cleanVal, pErr) } else { fp := uint16(f * 256.0); buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, fp); customValue = buf; customValueType = ValTypePercentage; customValueSize = 2; state.HeaderFlags |= FlagFixedPoint }
					default: log.Printf("L%d: Info: Treating custom prop '%s' with hint %d as String index.", lineNum, key, propDefHint); sIdx, sErr := state.addString(quotedVal); if sErr != nil { err = sErr } else { customValue = []byte{sIdx}; customValueType = ValTypeString; customValueSize = 1 }
					}
					if err == nil {
						if len(el.KrbCustomProperties) >= MaxProperties { err = fmt.Errorf("max custom props (%d)", MaxProperties) } else {
							customProp := KrbCustomProperty{ KeyIndex: keyIdx, ValueType: customValueType, ValueSize: customValueSize, Value: customValue, }; el.KrbCustomProperties = append(el.KrbCustomProperties, customProp); el.CustomPropCount = uint8(len(el.KrbCustomProperties))
						}
					}
				}
			}
		}

		// D. Log Unhandled (If not processed as standard, event, or custom)
		if !propProcessed { logUnhandledPropWarning(state, el, key, lineNum) }
		if err != nil { return fmt.Errorf("L%d: error processing property '%s': %w", lineNum, key, err) }
	} // End loop through remaining source properties


	// --- Step 4: Finalize Layout Byte ---
	// ... (Layout Byte Logic - Stays the Same) ...
	finalLayout := uint8(0); layoutSource := "Default"; styleLayoutByte := uint8(0); styleLayoutFound := false
	if el.StyleID > 0 { style := state.findStyleByID(el.StyleID); if style != nil && style.IsResolved { for _, prop := range style.Properties { if prop.PropertyID == PropIDLayoutFlags && prop.ValueType == ValTypeByte && prop.Size == 1 { styleLayoutByte = prop.Value[0]; styleLayoutFound = true; break } } } else if style != nil { log.Printf("CRITICAL WARN: Style %d ('%s') for Elem %d ('%s') not resolved when checking layout!", el.StyleID, style.SourceName, el.SelfIndex, el.SourceElementName) } else { log.Printf("CRITICAL WARN: Style %d for Elem %d ('%s') not found when checking layout!", el.StyleID, el.SelfIndex, el.SourceElementName) } }
	if el.LayoutFlagsSource != 0 { finalLayout = el.LayoutFlagsSource; layoutSource = "Explicit" } else if styleLayoutFound { finalLayout = styleLayoutByte; layoutSource = "Style" } else { finalLayout = LayoutDirectionColumn | LayoutAlignmentStart; layoutSource = "Default" }
	el.Layout = finalLayout;
	if layoutSource != "Default" || finalLayout != (LayoutDirectionColumn|LayoutAlignmentStart) { log.Printf("   >> Elem %d ('%s') Final Layout Byte: 0x%02X (Source: %s)", el.SelfIndex, el.SourceElementName, el.Layout, layoutSource) }


	// --- Step 5: Recursively Resolve Children ---
	// ... (Child Resolution Logic - Stays the Same) ...
	el.Children = make([]*Element, 0, len(el.SourceChildrenIndices)); el.ChildCount = 0
	for _, childIndex := range el.SourceChildrenIndices {
		if childIndex < 0 || childIndex >= len(state.Elements) { return fmt.Errorf("L%d: invalid child index %d for '%s'", el.SourceLineNum, childIndex, el.SourceElementName) }
		err := state.resolveElementRecursive(childIndex); if err != nil { return fmt.Errorf("resolving child %d of '%s': %w", childIndex, el.SourceElementName, err) }
		if childIndex < len(state.Elements) { el.Children = append(el.Children, &state.Elements[childIndex]) } else { return fmt.Errorf("internal error: child index %d invalid after resolve for '%s'", childIndex, el.SourceElementName) }
	}
	el.ChildCount = uint8(len(el.Children))


	return nil // Success
} // End resolveElementRecursive

// --- Helper Functions Used By resolveElementRecursive ---
// (Add findStyleByID helper below)

// findStyleByID finds a StyleEntry by its 1-based ID.
func (state *CompilerState) findStyleByID(styleID uint8) *StyleEntry {
	if styleID == 0 || int(styleID) > len(state.Styles) {
		return nil // Invalid ID
	}
	// Style array is 0-based, StyleID is 1-based
	return &state.Styles[styleID-1]
}

// guessResourceType provides a simple heuristic based on key name for custom resource properties.
func guessResourceType(key string) uint8 {
	lowerKey := strings.ToLower(key)
	if strings.Contains(lowerKey, "image") || strings.Contains(lowerKey, "icon") || strings.Contains(lowerKey, "sprite") || strings.Contains(lowerKey, "texture") || strings.Contains(lowerKey, "background") {
		return ResTypeImage
	}
	// Add other guesses (Font, Sound etc.) if needed based on common key names
	return ResTypeImage // Default guess if no hints found
}

// cleanAndQuoteValue removes comments, trims space, and returns both the fully cleaned value
// (for parsing) and a version with only outer quotes trimmed (for string table lookups).
func cleanAndQuoteValue(valStr string) (cleanVal, quotedVal string) {
	valuePart := valStr
	trimmedValueBeforeCommentCheck := strings.TrimSpace(valuePart)

	commentIndex := -1
	inQuotes := false
	// Simple quote and comment detection (assumes '#' is not used inside quoted strings)
	for i, r := range trimmedValueBeforeCommentCheck {
		if r == '"' {
			inQuotes = !inQuotes
		}
		// Only treat '#' as comment if NOT inside quotes and not the first char (allow hex colors)
		if r == '#' && !inQuotes && i > 0 {
			commentIndex = i
			break
		}
	}

	// Slice only if comment marker found after the first character
	if commentIndex > 0 {
		valuePart = trimmedValueBeforeCommentCheck[:commentIndex]
	} else {
		// Use the already trimmed string if no comment found or comment is at start
		valuePart = trimmedValueBeforeCommentCheck
	}

	cleanVal = strings.TrimSpace(valuePart) // Final trim after potential slice
	quotedVal = trimQuotes(cleanVal)        // Trim quotes *after* removing comments and spaces
	return
}


// --- Property Adding Helpers (addColorProp, addByteProp, etc.) ---
// These helpers add STANDARD KRB properties to el.KrbProperties

func addColorProp(el *Element, propID uint8, valueStr string, headerFlags *uint16) error {
	// *** MODIFICATION: Use valueStr directly, parseColor handles # check ***
	col, ok := parseColor(valueStr)
	if !ok {
		return fmt.Errorf("invalid color '%s'", valueStr)
	}
	err := el.addKrbProperty(propID, ValTypeColor, col[:]) // Add to STANDARD properties
	if err == nil {
		*headerFlags |= FlagExtendedColor
	} // Ensure flag is set if RGBA color used
	return err
}

func addByteProp(el *Element, propID uint8, cleanVal string) error {
	v, err := strconv.ParseUint(cleanVal, 10, 8)
	if err != nil {
		return fmt.Errorf("invalid uint8 '%s': %w", cleanVal, err)
	}
	return el.addKrbProperty(propID, ValTypeByte, []byte{uint8(v)}) // Add to STANDARD properties
}

func addShortProp(el *Element, propID uint8, cleanVal string) error {
	v, err := strconv.ParseUint(cleanVal, 10, 16)
	if err != nil {
		return fmt.Errorf("invalid uint16 '%s': %w", cleanVal, err)
	}
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(v))
	return el.addKrbProperty(propID, ValTypeShort, buf) // Add to STANDARD properties
}

func addFixedPointProp(el *Element, propID uint8, cleanVal string, headerFlags *uint16) error {
	f, err := strconv.ParseFloat(cleanVal, 64)
	if err != nil {
		return fmt.Errorf("invalid float '%s': %w", cleanVal, err)
	}
	fixedPointVal := uint16(f * 256.0) // Convert float to 8.8 fixed-point (uint16)
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, fixedPointVal)
	err = el.addKrbProperty(propID, ValTypePercentage, buf) // Add to STANDARD properties
	if err == nil {
		*headerFlags |= FlagFixedPoint
	} // Ensure flag is set
	return err
}

// addEdgeInsetsProp handles simple EdgeInsets where one value applies to all sides
func addEdgeInsetsProp(el *Element, propID uint8, cleanVal string) error {
	// TODO: Enhance this to handle multi-value strings like "10", "5 10", "5 10 15 20"
	v, err := strconv.ParseUint(cleanVal, 10, 8) // Assuming uint8 for padding/margin
	if err != nil {
		return fmt.Errorf("invalid uint8 for edge inset '%s': %w", cleanVal, err)
	}
	valByte := uint8(v)
	// Apply the same value to top, right, bottom, left
	buf := []byte{valByte, valByte, valByte, valByte}
	return el.addKrbProperty(propID, ValTypeEdgeInsets, buf) // Add to STANDARD properties
}


// --- Entry point for this resolution pass ---
func (state *CompilerState) resolveComponentsAndProperties() error {
	log.Println("Pass 1.5: Expanding components and resolving properties...")

	// Reset processed flag for all elements before starting this pass
	for i := range state.Elements {
		state.Elements[i].ProcessedInPass15 = false
	}

	// Find root elements (those without a parent) to start the recursive resolution
	rootIndices := []int{}
	for i := range state.Elements {
		if state.Elements[i].ParentIndex == -1 {
			rootIndices = append(rootIndices, i)
		}
	}

	if len(rootIndices) == 0 && len(state.Elements) > 0 {
		return fmt.Errorf("internal error: no root elements found (ParentIndex == -1) but elements exist")
	}

	// Resolve each distinct element tree starting from its root
	for _, rootIndex := range rootIndices {
		if rootIndex < 0 || rootIndex >= len(state.Elements) { // Added bounds check
			log.Printf("Warning: Skipping invalid root index %d\n", rootIndex)
			continue
		}
		if !state.Elements[rootIndex].ProcessedInPass15 {
			if err := state.resolveElementRecursive(rootIndex); err != nil {
				rootName := fmt.Sprintf("index %d", rootIndex)
				if rootIndex >= 0 && rootIndex < len(state.Elements) {
					rootName = fmt.Sprintf("'%s' (index %d)", state.Elements[rootIndex].SourceElementName, rootIndex)
				}
				return fmt.Errorf("error resolving element tree starting at root %s: %w", rootName, err)
			}
		}
	}

	// Optional: Verify that all elements were actually processed (detect orphans)
	processedCount := 0
	unprocessed := []int{}
	for k := range state.Elements {
		if state.Elements[k].ProcessedInPass15 {
			processedCount++
		} else {
			unprocessed = append(unprocessed, k)
		}
	}
	if processedCount != len(state.Elements) {
		log.Printf("Warning: %d/%d elements processed. Unprocessed indices: %v. Potential disconnected elements or recursion error.", processedCount, len(state.Elements), unprocessed)
	}

	log.Printf("   Resolution Pass Complete. Final Element count: %d\n", len(state.Elements))
	return nil
}


// Helper for ComponentDefinition to get a property value defined on its root element
func (def *ComponentDefinition) getRootPropertyValue(key string) (string, bool) {
	// Search backwards allows later properties in the definition to override earlier ones if needed
	for i := len(def.DefinitionRootProperties) - 1; i >= 0; i-- {
		if def.DefinitionRootProperties[i].Key == key {
			return def.DefinitionRootProperties[i].ValueStr, true
		}
	}
	return "", false // Property not found on the definition root
}

// *** ADDED HELPER ***
// getStringIndex finds the index of an existing string. Used by logUnhandledPropWarning.
func (state *CompilerState) getStringIndex(text string) (uint8, bool) { // Method on state
	cleaned := trimQuotes(strings.TrimSpace(text)) // Ensure consistent lookup
	if cleaned == "" {
		// Ensure index 0 exists if needed
		if len(state.Strings) == 0 {
			state.Strings = append(state.Strings, StringEntry{Text: "", Length: 0, Index: 0})
		}
		return 0, true
	}
	for i := 1; i < len(state.Strings); i++ { // Start from 1
		if state.Strings[i].Text == cleaned {
			return state.Strings[i].Index, true
		}
	}
	return 0, false // Not found
}

// --- logUnhandledPropWarning (Signature changed to accept state) ---
// Helper function to log warnings about properties that were not handled.
func logUnhandledPropWarning(state *CompilerState, el *Element, key string, lineNum int) { // Added 'state *CompilerState'
	// Keys handled directly in header processing or consumed during component expansion
	handledKeys := map[string]bool{
		"id": true, "style": true, "layout": true, // Handled for LayoutFlagsSource or StyleID
		"pos_x": true, "pos_y": true, "width": true, "height": true,
		// Component-specific keys consumed during expansion
		"bar_style": true,
		"orientation": true, // Consumed for hint/default style, maybe becomes custom prop
		// Event keys are handled separately
		"onClick": true, "on_click": true,
		// Add other handled/consumed keys here
	}

	if handledKeys[key] {
		return
	}

	// Check if it *was* handled as a custom property (even if declared or not)
	isCustomProp := false
	if el.IsComponentInstance {
		// *** Now 'state' is available to call the method ***
		keyIdx, keyExists := state.getStringIndex(key)
		if keyExists {
			for _, cp := range el.KrbCustomProperties {
				if cp.KeyIndex == keyIdx {
					isCustomProp = true
					break
				}
			}
		}
	}
	if isCustomProp {
		return
	} // Don't warn if it became a custom property

	// If it reached here, it's truly unhandled.
	if el.IsComponentInstance && el.ComponentDef != nil {
		isDeclared := false
		for _, propDef := range el.ComponentDef.Properties {
			if propDef.Name == key {
				isDeclared = true
				break
			}
		}
		if isDeclared {
			log.Printf("L%d: Info: Declared component property '%s' for '%s' was not mapped to standard or custom KRB property. Ignored.\n", lineNum, key, el.ComponentDef.Name)
		} else {
			log.Printf("L%d: Warning: Unhandled/undeclared property '%s' found on component instance '%s'. Ignored.\n", lineNum, key, el.ComponentDef.Name)
		}
	} else {
		log.Printf("L%d: Warning: Unhandled property '%s' for standard element '%s'. Ignored.\n", lineNum, key, el.SourceElementName)
	}
}


// adjustLayoutForPosition - Placeholder: Remains a no-op in this model.
func (state *CompilerState) adjustLayoutForPosition() error {
	log.Println("Pass 1.7: Adjusting layout (position handling)...")
	// No compiler-side layout adjustments based on 'position' custom property.
	// Runtime interprets position, or writer does simple child reordering based on hint.
	return nil
}