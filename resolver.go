// resolver.go
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"strconv"
	"strings"
	// "math" // Not needed directly in this file after removing direct layout calculations from hints
)

func (state *CompilerState) resolveElementRecursive(elementIndex int) error {
	if elementIndex < 0 || elementIndex >= len(state.Elements) {
		return fmt.Errorf("internal error: invalid element index %d during resolution", elementIndex)
	}
	el := &state.Elements[elementIndex] // Use pointer to modify the element in the main slice

	// Avoid infinite loops and redundant work if called multiple times within the same pass
	if el.ProcessedInPass15 {
		return nil
	}
	el.ProcessedInPass15 = true // Mark as visited for this pass

	var originalSourceProperties []SourceProperty // Store original props before component merge if needed

	// --- Step 1: Expand Component (if this element is a component usage) ---
	if el.Type == ElemTypeInternalComponentUsage {
		if el.ComponentDef == nil {
			return fmt.Errorf("L%d: internal error: component instance '%s' has nil definition", el.SourceLineNum, el.SourceElementName)
		}
		def := el.ComponentDef

		// Keep a copy of the original properties set on the usage tag, if needed later
		originalSourceProperties = make([]SourceProperty, len(el.SourceProperties))
		copy(originalSourceProperties, el.SourceProperties)

		// Determine the underlying KRB element type from the component definition
		rootType := def.DefinitionRootType
		if rootType == "" {
			rootType = "Container" // Default to Container if not specified in Define
		}
		el.Type = getElementTypeFromName(rootType) // Set the underlying KRB type
		if el.Type == ElemTypeUnknown {
			el.Type = ElemTypeCustomBase
			nameIdx, err := state.addString(rootType)
			if err != nil { return fmt.Errorf("L%d: failed adding component root type name '%s': %w", el.SourceLineNum, rootType, err) }
			el.IDStringIndex = nameIdx
			log.Printf("L%d: Info: Component '%s' expands to unknown root type '%s', using custom type 0x%X\n", el.SourceLineNum, def.Name, rootType, el.Type)
		}
		el.IsComponentInstance = true

		// --- Merge Properties (Definition Root -> Declared Defaults -> Usage Tag) ---
		mergedPropsMap := make(map[string]SourceProperty)
		for _, prop := range def.DefinitionRootProperties { mergedPropsMap[prop.Key] = prop }
		for _, propDef := range def.Properties {
			if _, exists := mergedPropsMap[propDef.Name]; !exists && propDef.DefaultValueStr != "" {
				cleanDefaultVal, _ := cleanAndQuoteValue(propDef.DefaultValueStr)
				mergedPropsMap[propDef.Name] = SourceProperty{ Key: propDef.Name, ValueStr: cleanDefaultVal, LineNum: def.DefinitionStartLine, }
			}
		}
		for _, prop := range originalSourceProperties { mergedPropsMap[prop.Key] = prop }
		el.SourceProperties = make([]SourceProperty, 0, len(mergedPropsMap))
		for _, prop := range mergedPropsMap { el.SourceProperties = append(el.SourceProperties, prop) }

		// --- Store Hints and Determine StyleID (Consumes component-specific style props) ---
		el.OrientationHint = "row" // Default orientation hint
		if orientationVal, ok := el.getSourcePropertyValue("orientation"); ok {
			cleanOrientation := trimQuotes(strings.TrimSpace(orientationVal))
			if cleanOrientation != "" { el.OrientationHint = cleanOrientation } // Only override default if non-empty
		}
		el.PositionHint = "bottom" // Default position hint
		if positionVal, ok := el.getSourcePropertyValue("position"); ok {
			cleanPosition := trimQuotes(strings.TrimSpace(positionVal))
			if cleanPosition != "" { el.PositionHint = cleanPosition } // Only override default if non-empty
		}

		barStyleStr, hasBarStyle := el.getSourcePropertyValue("bar_style")
		directStyleStr, hasDirectStyle := el.getSourcePropertyValue("style")
		finalStyleID := uint8(0)

		if hasBarStyle {
			cleanBarStyle := trimQuotes(strings.TrimSpace(barStyleStr))
			if cleanBarStyle != "" { // Check if bar_style value is non-empty
				finalStyleID = state.findStyleIDByName(cleanBarStyle)
				if finalStyleID == 0 { log.Printf("L%d: Warning: Component property bar_style '%s' not found for '%s'.\n", el.SourceLineNum, cleanBarStyle, def.Name) }
			} else {
				// Explicitly empty bar_style means no style override from this property
				log.Printf("L%d: Debug: Empty bar_style property provided for component '%s'.\n", el.SourceLineNum, def.Name)
			}
		}

		if finalStyleID == 0 && hasDirectStyle { // Only check direct style if bar_style didn't resolve
			cleanDirectStyle := trimQuotes(strings.TrimSpace(directStyleStr))
			if cleanDirectStyle != "" { // Check if style value is non-empty
				finalStyleID = state.findStyleIDByName(cleanDirectStyle)
				if finalStyleID == 0 { log.Printf("L%d: Warning: Explicit style '%s' not found for component usage '%s'.\n", el.SourceLineNum, cleanDirectStyle, def.Name) }
			}
		}

		if finalStyleID == 0 { // Only check definition/defaults if no explicit style found yet
			defRootStyle, hasDefRootStyle := def.getRootPropertyValue("style")
			if hasDefRootStyle {
				cleanDefRootStyle := trimQuotes(strings.TrimSpace(defRootStyle))
				if cleanDefRootStyle != "" { // Check if def style value is non-empty
					finalStyleID = state.findStyleIDByName(cleanDefRootStyle)
					if finalStyleID == 0 { log.Printf("L%d: Warning: Component definition root style '%s' not found for '%s'.\n", def.DefinitionStartLine, cleanDefRootStyle, def.Name) }
				}
			}

			if finalStyleID == 0 { // Only apply component default if definition root didn't specify style
				if def.Name == "TabBar" {
					baseStyleName := "tab_bar_style_base_row" // Default style name
					if el.OrientationHint == "column" || el.OrientationHint == "col" { // Check hint
						baseStyleName = "tab_bar_style_base_column"
					}
					finalStyleID = state.findStyleIDByName(baseStyleName)
					if finalStyleID == 0 { log.Printf("L%d: Warning: Default component style '%s' not found for '%s'.\n", el.SourceLineNum, baseStyleName, def.Name) }
				}
				// Add default style logic for other standard components here
			}
		}
		el.StyleID = finalStyleID // Set the final resolved StyleID

	} // --- End of Component Expansion Block ---

	// --- Step 2: Reset Resolved Data & Process Standard Header Fields ---
	el.KrbProperties = el.KrbProperties[:0]; el.PropertyCount = 0
	el.KrbCustomProperties = el.KrbCustomProperties[:0]; el.CustomPropCount = 0
	el.KrbEvents = el.KrbEvents[:0]; el.EventCount = 0
	el.LayoutFlagsSource = 0 // Reset explicit layout source flag

	processedSourcePropKeys := make(map[string]bool)

	for _, sp := range el.SourceProperties {
		key := sp.Key; valStr := sp.ValueStr; lineNum := sp.LineNum
		cleanVal, quotedVal := cleanAndQuoteValue(valStr)
		handledAsHeader := true; var parseErr error

		switch key {
		case "id":
			if quotedVal != "" { idIdx, err := state.addString(quotedVal); if err == nil { el.IDStringIndex = idIdx; el.SourceIDName = quotedVal } else { parseErr = err } }
		case "style": // Handled during component expansion, mark as processed here too
			// StyleID is already set, just mark this key as processed
		case "pos_x":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.PosX = uint16(v) } else { parseErr = fmt.Errorf("pos_x uint16 '%s': %w", cleanVal, err) }
		case "pos_y":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.PosY = uint16(v) } else { parseErr = fmt.Errorf("pos_y uint16 '%s': %w", cleanVal, err) }
		case "width":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.Width = uint16(v) } else { parseErr = fmt.Errorf("width uint16 '%s': %w", cleanVal, err) }
		case "height":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.Height = uint16(v) } else { parseErr = fmt.Errorf("height uint16 '%s': %w", cleanVal, err) }
		case "layout":
			el.LayoutFlagsSource = parseLayoutString(cleanVal) // Store explicit layout request
		default:
			handledAsHeader = false
		}

		if parseErr != nil { return fmt.Errorf("L%d: error processing header field '%s': %w", lineNum, key, parseErr) }
		if handledAsHeader { processedSourcePropKeys[key] = true }
	}

	// --- Step 3: Resolve Remaining Properties into KRB Standard Props, Custom Props, or Events ---
	for _, sp := range el.SourceProperties {
		key := sp.Key; valStr := sp.ValueStr; lineNum := sp.LineNum
		if processedSourcePropKeys[key] { continue }

		cleanVal, quotedVal := cleanAndQuoteValue(valStr)
		propProcessed := false; var err error

		// --- A. Check Events ---
		if key == "onClick" || key == "on_click" {
			propProcessed = true
			if len(el.KrbEvents) < MaxEvents {
				if quotedVal != "" {
					cbIdx, addErr := state.addString(quotedVal); if addErr == nil { el.KrbEvents = append(el.KrbEvents, KrbEvent{EventTypeClick, cbIdx}); el.EventCount = uint8(len(el.KrbEvents)) } else { err = fmt.Errorf("add onClick callback string '%s': %w", quotedVal, addErr) }
				} else { log.Printf("L%d: Warning: Empty callback name for '%s' ignored.\n", lineNum, key) }
			} else { err = fmt.Errorf("maximum events (%d) exceeded for element '%s'", MaxEvents, el.SourceElementName) }
		} else {
			// --- B. Check Standard KRB Properties ---
			propAddedAsStandard := true
			switch key {
			case "background_color": err = addColorProp(el, PropIDBgColor, cleanVal, &state.HeaderFlags)
			case "text_color", "foreground_color": err = addColorProp(el, PropIDFgColor, cleanVal, &state.HeaderFlags)
			case "border_color": err = addColorProp(el, PropIDBorderColor, cleanVal, &state.HeaderFlags)
			case "border_width": err = addByteProp(el, PropIDBorderWidth, cleanVal)
			case "border_radius": err = addByteProp(el, PropIDBorderRadius, cleanVal)
			case "opacity": err = addByteProp(el, PropIDOpacity, cleanVal)
			case "visibility", "visible": vis := uint8(1); switch strings.ToLower(cleanVal) { case "true", "visible", "1": vis = 1; case "false", "hidden", "0": vis = 0; default: err = fmt.Errorf("invalid boolean visibility '%s'", cleanVal) }; if err == nil { err = el.addKrbProperty(PropIDVisibility, ValTypeByte, []byte{vis}) }
			case "z_index": err = addShortProp(el, PropIDZindex, cleanVal)
			case "transform": err = state.addKrbStringProperty(el, PropIDTransform, valStr)
			case "shadow": err = state.addKrbStringProperty(el, PropIDShadow, valStr)
			case "text", "content": err = state.addKrbStringProperty(el, PropIDTextContent, valStr)
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
			case "window_title": if el.Type == ElemTypeApp { err = state.addKrbStringProperty(el, PropIDWindowTitle, valStr) } else { propAddedAsStandard = false }
			case "resizable": if el.Type == ElemTypeApp { b := uint8(0); if cleanVal == "true" || cleanVal == "1" { b = 1 }; err = el.addKrbProperty(PropIDResizable, ValTypeByte, []byte{b}) } else { propAddedAsStandard = false }
			case "icon": if el.Type == ElemTypeApp { err = state.addKrbResourceProperty(el, PropIDIcon, ResTypeImage, quotedVal) } else { propAddedAsStandard = false }
			case "version": if el.Type == ElemTypeApp { err = state.addKrbStringProperty(el, PropIDVersion, valStr) } else { propAddedAsStandard = false }
			case "author": if el.Type == ElemTypeApp { err = state.addKrbStringProperty(el, PropIDAuthor, valStr) } else { propAddedAsStandard = false }
			case "keep_aspect": if el.Type == ElemTypeApp { b := uint8(0); if cleanVal == "true" || cleanVal == "1" { b = 1 }; err = el.addKrbProperty(PropIDKeepAspect, ValTypeByte, []byte{b}) } else { propAddedAsStandard = false }
			case "scale_factor": if el.Type == ElemTypeApp { err = addFixedPointProp(el, PropIDScaleFactor, cleanVal, &state.HeaderFlags) } else { propAddedAsStandard = false }
			default:
				propAddedAsStandard = false
			}
			if propAddedAsStandard { propProcessed = true; if err != nil { return fmt.Errorf("L%d: error processing standard property '%s': %w", lineNum, key, err) } }
		}

		// --- C. Check Custom Component Properties ---
		if !propProcessed && el.IsComponentInstance && el.ComponentDef != nil {
			isDeclaredComponentProp := false; var propDefHint uint8 = ValTypeCustom
			for _, defProp := range el.ComponentDef.Properties { if defProp.Name == key { isDeclaredComponentProp = true; propDefHint = defProp.ValueTypeHint; break } }

			if isDeclaredComponentProp {
				propProcessed = true
				keyIdx, keyErr := state.addString(key)
				if keyErr != nil { err = fmt.Errorf("add custom key '%s': %w", key, keyErr) } else {
					var customValue []byte; var customValueType uint8 = ValTypeString; var customValueSize uint8 = 1
					switch propDefHint {
					case ValTypeString: sIdx, sErr := state.addString(valStr); if sErr != nil { err = sErr } else { customValue = []byte{sIdx}; customValueType = ValTypeString; customValueSize = 1 }
					case ValTypeInt, ValTypeShort: v, pErr := strconv.ParseInt(cleanVal, 10, 16); if pErr != nil { err = fmt.Errorf("invalid Int '%s': %w", cleanVal, pErr) } else { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(v)); customValue = buf; customValueType = ValTypeShort; customValueSize = 2 }
					case ValTypeBool, ValTypeByte: b := uint8(0); lc := strings.ToLower(cleanVal); if lc == "true" || lc == "1" { b = 1 } else if lc != "false" && lc != "0" { log.Printf("L%d: Warn: Invalid Bool '%s' for '%s', using false.", lineNum, cleanVal, key) }; customValue = []byte{b}; customValueType = ValTypeByte; customValueSize = 1
					case ValTypeColor: col, ok := parseColor(cleanVal); if !ok { err = fmt.Errorf("invalid Color '%s'", cleanVal) } else { customValue = col[:]; customValueType = ValTypeColor; customValueSize = 4; state.HeaderFlags |= FlagExtendedColor }
					case ValTypeResource: rType := guessResourceType(key); rIdx, rErr := state.addResource(rType, quotedVal); if rErr != nil { err = rErr } else { customValue = []byte{rIdx}; customValueType = ValTypeResource; customValueSize = 1 }
					case ValTypeFloat: f, pErr := strconv.ParseFloat(cleanVal, 64); if pErr != nil { err = fmt.Errorf("invalid Float '%s': %w", cleanVal, pErr) } else { fp := uint16(f * 256.0); buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, fp); customValue = buf; customValueType = ValTypePercentage; customValueSize = 2; state.HeaderFlags |= FlagFixedPoint }
					case ValTypeStyleID: sIdx := state.findStyleIDByName(quotedVal); if sIdx == 0 { log.Printf("L%d: Warning: StyleID value '%s' for custom prop '%s' not found, storing index 0.", lineNum, quotedVal, key)}; customValue = []byte{sIdx}; customValueType = ValTypeByte; customValueSize = 1 // Store Style ID as a Byte index (or 0)
					default: log.Printf("L%d: Info: Treating custom prop '%s' with hint %d as String.", lineNum, key, propDefHint); sIdx, sErr := state.addString(valStr); if sErr != nil { err = sErr } else { customValue = []byte{sIdx}; customValueType = ValTypeString; customValueSize = 1 }
					}
					if err == nil {
						if len(el.KrbCustomProperties) >= MaxProperties { err = fmt.Errorf("max custom props (%d)", MaxProperties) } else {
							customProp := KrbCustomProperty{ KeyIndex: keyIdx, ValueType: customValueType, ValueSize: customValueSize, Value: customValue, }; el.KrbCustomProperties = append(el.KrbCustomProperties, customProp); el.CustomPropCount = uint8(len(el.KrbCustomProperties))
						}
					}
				}
			}
		}

		// --- D. Log Unhandled ---
		if !propProcessed { logUnhandledPropWarning(el, key, lineNum) }
		if err != nil { return fmt.Errorf("L%d: error processing property '%s': %w", lineNum, key, err) }

	} // --- End loop through remaining source properties ---


	// --- Step 4: Finalize Layout Byte ---
	// Determines the final layout byte based on explicit source, style, or component hints.
	finalLayout := uint8(0)
	layoutSource := "Default" // For debugging provenance

	// 1. Check for explicit 'layout:' property directly on the element usage
	if el.LayoutFlagsSource != 0 {
		finalLayout = el.LayoutFlagsSource
		layoutSource = "Explicit"
	} else {
		// 2. Check the applied style for a layout property (PROP_ID_LAYOUTFLAGS)
		layoutFromStyle := uint8(0)
		styleLayoutFound := false
		if el.StyleID > 0 && int(el.StyleID-1) < len(state.Styles) {
			st := &state.Styles[el.StyleID-1]
			if st.IsResolved { // Should be resolved by this pass
				for _, prop := range st.Properties {
					if prop.PropertyID == PropIDLayoutFlags && prop.ValueType == ValTypeByte && prop.Size == 1 {
						layoutFromStyle = prop.Value[0]
						styleLayoutFound = true
						break
					}
				}
			} else {
				log.Printf("CRITICAL WARN: Style %d for Elem %d ('%s') was not resolved when checking for layout property!", el.StyleID, el.SelfIndex, el.SourceElementName)
			}
		}

		if styleLayoutFound {
			finalLayout = layoutFromStyle
			layoutSource = "Style"
		} else {
			// 3. *** CORRECTED LOGIC *** Check Component Hints if no explicit or style layout found
			if el.IsComponentInstance {
				// Use OrientationHint to determine the *direction* part
				// Default alignment can be Start
				direction := LayoutDirectionColumn // Default component direction if hint is weird
				// Normalize hint for comparison
				lowerOrientationHint := strings.ToLower(el.OrientationHint)
				switch lowerOrientationHint {
                case "row":
                    direction = LayoutDirectionRow
                case "row_rev", "row-rev":
                    direction = LayoutDirectionRowRev
                case "col_rev", "col-rev", "column-rev":
                    direction = LayoutDirectionColRev
                case "col", "column":
					direction = LayoutDirectionColumn // Explicitly handle column too
				default:
					log.Printf("L%d: Warning: Unknown orientation hint '%s' for component '%s', defaulting to Column.", el.SourceLineNum, el.OrientationHint, el.ComponentDef.Name)
                }


				// Combine derived direction with a default alignment (e.g., Start)
				// TODO: Consider adding AlignmentHint to components if needed
				finalLayout = direction | LayoutAlignmentStart // Combine direction with default start alignment
				layoutSource = "ComponentHint"

			} else {
				// 4. Apply the global default layout for standard elements if no explicit or style layout found
				finalLayout = LayoutDirectionColumn | LayoutAlignmentStart // Default (e.g., Column, Start)
				layoutSource = "Default"
			}
		}
	}

	el.Layout = finalLayout // Set the final Layout byte in the element header structure
	log.Printf("   >> Elem %d ('%s') Final Layout Byte: 0x%02X (Source: %s)",
		el.SelfIndex, el.SourceElementName, el.Layout, layoutSource)


	// --- Step 5: Recursively Resolve Children ---
	el.Children = make([]*Element, 0, len(el.SourceChildrenIndices))
	el.ChildCount = 0

	for _, childIndex := range el.SourceChildrenIndices {
		if childIndex < 0 || childIndex >= len(state.Elements) {
			return fmt.Errorf("L%d: invalid child index %d found for element '%s'", el.SourceLineNum, childIndex, el.SourceElementName)
		}
		err := state.resolveElementRecursive(childIndex)
		if err != nil {
			return fmt.Errorf("failed resolving child %d (of element %d '%s'): %w", childIndex, elementIndex, el.SourceElementName, err)
		}
		if childIndex < len(state.Elements) {
			el.Children = append(el.Children, &state.Elements[childIndex])
		} else {
			return fmt.Errorf("internal error: child index %d became invalid after resolving child tree for parent '%s'", childIndex, el.SourceElementName)
		}
	}
	el.ChildCount = uint8(len(el.Children))

	return nil // Successfully resolved this element and its subtree
}


// --- Helper Functions Used By resolveElementRecursive ---

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

func addColorProp(el *Element, propID uint8, cleanVal string, headerFlags *uint16) error {
	col, ok := parseColor(cleanVal)
	if !ok { return fmt.Errorf("invalid color '%s'", cleanVal) }
	err := el.addKrbProperty(propID, ValTypeColor, col[:]) // Add to STANDARD properties
	if err == nil { *headerFlags |= FlagExtendedColor }     // Ensure flag is set if RGBA color used
	return err
}

func addByteProp(el *Element, propID uint8, cleanVal string) error {
	v, err := strconv.ParseUint(cleanVal, 10, 8)
	if err != nil { return fmt.Errorf("invalid uint8 '%s': %w", cleanVal, err) }
	return el.addKrbProperty(propID, ValTypeByte, []byte{uint8(v)}) // Add to STANDARD properties
}

func addShortProp(el *Element, propID uint8, cleanVal string) error {
	v, err := strconv.ParseUint(cleanVal, 10, 16)
	if err != nil { return fmt.Errorf("invalid uint16 '%s': %w", cleanVal, err) }
	buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(v))
	return el.addKrbProperty(propID, ValTypeShort, buf) // Add to STANDARD properties
}

func addFixedPointProp(el *Element, propID uint8, cleanVal string, headerFlags *uint16) error {
	f, err := strconv.ParseFloat(cleanVal, 64)
	if err != nil { return fmt.Errorf("invalid float '%s': %w", cleanVal, err) }
	fixedPointVal := uint16(f * 256.0) // Convert float to 8.8 fixed-point (uint16)
	buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, fixedPointVal)
	err = el.addKrbProperty(propID, ValTypePercentage, buf) // Add to STANDARD properties
	if err == nil { *headerFlags |= FlagFixedPoint }        // Ensure flag is set
	return err
}

// addEdgeInsetsProp handles simple EdgeInsets where one value applies to all sides
func addEdgeInsetsProp(el *Element, propID uint8, cleanVal string) error {
    v, err := strconv.ParseUint(cleanVal, 10, 8) // Assuming uint8 for padding/margin
    if err != nil { return fmt.Errorf("invalid uint8 for edge inset '%s': %w", cleanVal, err) }
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

	// Handle cases with no elements or no root elements (should be caught by parser ideally)
	if len(rootIndices) == 0 && len(state.Elements) > 0 {
		return fmt.Errorf("internal error: no root elements found (ParentIndex == -1) but elements exist")
	}

	// Resolve each distinct element tree starting from its root
	for _, rootIndex := range rootIndices {
		// Check processed flag again in case roots share nodes (shouldn't happen in valid tree)
		if !state.Elements[rootIndex].ProcessedInPass15 {
			if err := state.resolveElementRecursive(rootIndex); err != nil {
				// Provide context about which root tree failed
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
		// Depending on strictness, consider returning an error:
		// return fmt.Errorf("found %d unprocessed elements after resolving roots", len(unprocessed))
	}

	log.Printf("   Resolution Pass Complete. Final Element count: %d\n", len(state.Elements))
	return nil // Pass completed successfully
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


// Helper function to log warnings about properties that were not handled.
func logUnhandledPropWarning(el *Element, key string, lineNum int) {
	// Keys handled directly in header processing or consumed during component expansion
	// These should not trigger an "unhandled" warning.
	handledKeys := map[string]bool{
		"id": true, "style": true, "layout": true, // Handled for LayoutFlagsSource or StyleID
		"pos_x": true, "pos_y": true, "width": true, "height": true,
		// Component-specific keys consumed during expansion (won't be processed later)
		"bar_style": true, // Example: consumed for style selection
		// Event keys are handled separately
		"onClick": true, "on_click": true,
		// Add other handled/consumed keys here
	}

	// If the key was already handled by specific logic, exit.
	if handledKeys[key] {
		return
	}

	// If it reached here, it wasn't a header field, event, consumed key, standard KRB prop, OR custom KRB prop.
	// It's truly unhandled by this compiler configuration.
	if el.IsComponentInstance && el.ComponentDef != nil {
		// Check if it *was* declared in 'Properties {}' but perhaps had an unsupported type hint or other issue
		// preventing it from becoming a custom prop.
		isDeclared := false
		for _, propDef := range el.ComponentDef.Properties {
			if propDef.Name == key { isDeclared = true; break }
		}
		if isDeclared {
			log.Printf("L%d: Info: Declared component property '%s' for '%s' was not mapped to standard or custom KRB property (check type hint?). Ignored.\n", lineNum, key, el.ComponentDef.Name)
		} else {
			// It wasn't even declared in the component definition.
			log.Printf("L%d: Warning: Unhandled/undeclared property '%s' found on component instance '%s'. Ignored.\n", lineNum, key, el.ComponentDef.Name)
		}
	} else {
		// It's an unhandled property on a standard (non-component) element.
		log.Printf("L%d: Warning: Unhandled property '%s' for standard element '%s'. Ignored.\n", lineNum, key, el.SourceElementName)
	}
}


// adjustLayoutForPosition - Placeholder: Remains a no-op in this model.
// Runtime interprets position, or writer does simple child reordering based on hint.
func (state *CompilerState) adjustLayoutForPosition() error {
	log.Println("Pass 1.7: Adjust Layout for 'position' (Skipped - Runtime interpretation or simple write-time reorder assumed).")
	// No compiler-side layout adjustments based on 'position' custom property.
	return nil
}