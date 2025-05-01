// resolver.go
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	// "sort" // No longer needed here if writer handles sorting
	"strconv"
	"strings"
)

// Convention Key for storing the original Define'd component name in custom properties
const componentNameConventionKey = "_componentName"

// --- resolveComponentsAndProperties (Entry point for Pass 1.5) ---
// Iterates through root elements and starts the recursive resolution process.
func (state *CompilerState) resolveComponentsAndProperties() error {
	log.Println("Pass 1.5: Expanding components and resolving element properties...")

	// Reset processed flag for all elements before starting this pass.
	// This prevents processing elements multiple times if there are complex references (though shouldn't happen with tree structure).
	for i := range state.Elements {
		state.Elements[i].ProcessedInPass15 = false
	}

	// Find root elements (those with no parent) to start the recursive resolution.
	rootIndices := []int{}
	for i := range state.Elements {
		if state.Elements[i].ParentIndex == -1 {
			rootIndices = append(rootIndices, i)
		}
	}

	// Check if elements exist but no root was found (indicates an error in parsing/linking).
	if len(rootIndices) == 0 && len(state.Elements) > 0 {
		return fmt.Errorf("internal error: no root elements found (ParentIndex == -1) but elements exist")
	}

	// Resolve each distinct element tree starting from its root.
	for _, rootIndex := range rootIndices {
		// Basic bounds check for safety.
		if rootIndex < 0 || rootIndex >= len(state.Elements) {
			log.Printf("Warning: Skipping invalid root index %d\n", rootIndex)
			continue
		}
		// Only process if not already visited in this pass.
		if !state.Elements[rootIndex].ProcessedInPass15 {
			if err := state.resolveElementRecursive(rootIndex); err != nil {
				// Try to get a meaningful name for the error message.
				rootName := fmt.Sprintf("index %d", rootIndex)
				if rootIndex >= 0 && rootIndex < len(state.Elements) {
					rootName = fmt.Sprintf("'%s' (index %d)", state.Elements[rootIndex].SourceElementName, rootIndex)
				}
				return fmt.Errorf("error resolving element tree starting at root %s: %w", rootName, err)
			}
		}
	}

	// Optional: Final check to ensure all elements were processed.
	// If not, it might indicate disconnected elements or an error in the recursion/linking.
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


func handleSimpleMarginProperty(el *Element, propID uint8, cleanVal string) error {
	v, err := strconv.ParseUint(cleanVal, 10, 8) // Assuming uint8 for values
	if err != nil {
		return fmt.Errorf("invalid uint8 value for simple margin '%s': %w", cleanVal, err)
	}
	valByte := uint8(v)
	// Apply the same value to top, right, bottom, left
	buf := []byte{valByte, valByte, valByte, valByte}
	return el.addKrbProperty(propID, ValTypeEdgeInsets, buf)
}


// --- resolveElementRecursive (Corrected and Fully Expanded) ---
// Core recursive function for resolving a single element.
func (state *CompilerState) resolveElementRecursive(elementIndex int) error {

	// --- Initial Checks and Setup ---

	// Bounds check for safety.
	if elementIndex < 0 || elementIndex >= len(state.Elements) {
		return fmt.Errorf("internal error: invalid element index %d during resolution", elementIndex)
	}

	// Get a pointer to the element in the main slice to modify it directly.
	el := &state.Elements[elementIndex]

	// Avoid processing the same element multiple times in this pass.
	if el.ProcessedInPass15 {
		return nil // Already processed, skip.
	}
	el.ProcessedInPass15 = true // Mark as visited for this pass.

	// Variables to hold component-specific info if this element is an instance.
	var originalSourceProperties []SourceProperty // Stores properties from the usage tag (e.g., <TabBar id="nav">)
	var componentDef *ComponentDefinition = nil    // Points to the component's definition (Define TabBar { ... })
	isComponentInstance := false                   // Flag set if expansion happens here.


	// --- Step 1: Expand Component (if this element represents a component usage marker) ---
	if el.Type == ElemTypeInternalComponentUsage {

		if el.ComponentDef == nil {
			return fmt.Errorf("L%d: internal error: component instance '%s' has nil definition", el.SourceLineNum, el.SourceElementName)
		}

		// Store component definition and mark as instance
		componentDef = el.ComponentDef
		isComponentInstance = true
		el.IsComponentInstance = true // Mark element struct as well

		// Store the original properties defined on the usage tag before merging.
		originalSourceProperties = make([]SourceProperty, len(el.SourceProperties))
		copy(originalSourceProperties, el.SourceProperties)

		// Determine the root element type specified in the 'Define' block.
		rootType := componentDef.DefinitionRootType
		if rootType == "" {
			log.Printf("L%d: Warning: Component definition '%s' has no root element type specified. Defaulting to 'Container'.\n", componentDef.DefinitionStartLine, componentDef.Name)
			rootType = "Container"
		}

		// Update the element's type to match the component's defined root type.
		el.Type = getElementTypeFromName(rootType) // Uses helper from utils.go

		// Handle if the component's root type is not a standard Kryon element type.
		if el.Type == ElemTypeUnknown {
			el.Type = ElemTypeCustomBase // Use the base custom type ID.
			nameIdx, err := state.addString(rootType)
			if err != nil {
				return fmt.Errorf("L%d: failed adding component root type name '%s' to string table: %w", el.SourceLineNum, rootType, err)
			}
			// Use the IDStringIndex field in the KRB header to store the name index of the custom type.
			el.IDStringIndex = nameIdx
			log.Printf("L%d: Info: Component '%s' expands to unknown root type '%s', using custom type 0x%X with name index %d\n", el.SourceLineNum, componentDef.Name, rootType, el.Type, nameIdx)
		}

		// --- Merge Properties (Definition Root -> Definition Defaults -> Usage Tag) ---
		mergedPropsMap := make(map[string]SourceProperty)

		// 1. Start with properties defined *on the root element* within the Define block.
		for _, prop := range componentDef.DefinitionRootProperties {
			mergedPropsMap[prop.Key] = prop
		}

		// 2. Add default values for properties declared in `Properties {}`, if not already set.
		for _, propDef := range componentDef.Properties {
			if _, exists := mergedPropsMap[propDef.Name]; !exists && propDef.DefaultValueStr != "" {
				cleanDefaultVal, _ := cleanAndQuoteValue(propDef.DefaultValueStr) // Clean the default value string.
				// Create a SourceProperty entry for the default value.
				mergedPropsMap[propDef.Name] = SourceProperty{
					Key:      propDef.Name,
					ValueStr: cleanDefaultVal,
					LineNum:  componentDef.DefinitionStartLine, // Attribute line number to the definition.
				}
			}
		}

		// 3. Apply properties from the usage tag (e.g., <TabBar id="nav" ...>), overwriting definition/defaults.
		for _, prop := range originalSourceProperties {
			mergedPropsMap[prop.Key] = prop
		}

		// Replace the element's source properties list with the final merged result.
		el.SourceProperties = make([]SourceProperty, 0, len(mergedPropsMap))
		for _, prop := range mergedPropsMap {
			el.SourceProperties = append(el.SourceProperties, prop)
		}

		// Store component-specific hints (like position/orientation) if present in merged props.
		// These might be used later (e.g., by the writer for simple reordering, or default style lookup).
		el.OrientationHint = ""
		el.PositionHint = ""
		if orientationVal, ok := mergedPropsMap["orientation"]; ok {
			cleanOrientation := trimQuotes(strings.TrimSpace(orientationVal.ValueStr))
			if cleanOrientation != "" {
				el.OrientationHint = cleanOrientation
			}
		}
		if positionVal, ok := mergedPropsMap["position"]; ok {
			cleanPosition := trimQuotes(strings.TrimSpace(positionVal.ValueStr))
			if cleanPosition != "" {
				el.PositionHint = cleanPosition
			}
		}
	} // --- End Component Expansion block ---


	// --- Step 2: Reset Resolved Data & Process Standard Header Fields ---

	// Clear out any previously resolved KRB data for this element before processing source properties.
	el.KrbProperties = el.KrbProperties[:0]       // Standard KRB properties list
	el.PropertyCount = 0
	el.KrbCustomProperties = el.KrbCustomProperties[:0] // Custom KRB properties list
	el.CustomPropCount = 0
	el.KrbEvents = el.KrbEvents[:0]               // KRB events list
	el.EventCount = 0
	el.LayoutFlagsSource = 0                       // Reset layout hint derived directly from source 'layout:' property

	// Track which source property keys are processed into header fields or style directives.
	processedSourcePropKeys := make(map[string]bool)


	// --- Determine Final Style ID ---
	el.StyleID = 0 // Reset StyleID for this element to 0 (no style).

	// 1. Check for direct 'style:' property on the element usage tag.
	directStyleStr, hasDirectStyle := el.getSourcePropertyValue("style")
	if hasDirectStyle {
		cleanDirectStyle, _ := cleanAndQuoteValue(directStyleStr)
		if cleanDirectStyle != "" {
			styleID := state.findStyleIDByName(cleanDirectStyle) // Look up style ID by name.
			if styleID == 0 {
				// Style name was given but not found. Log a warning.
				log.Printf("L%d: Warning: Style %s not found for element '%s'. Check style definitions and includes.\n", el.SourceLineNum, directStyleStr, el.SourceElementName)
			} else {
				// Found the style, assign its ID.
				el.StyleID = styleID
			}
		}
		// Mark 'style' as handled so it's not processed again later.
		processedSourcePropKeys["style"] = true
	}

	// 2. If it's a component instance, check for specific style override keys (like 'bar_style') and component defaults.
	if el.IsComponentInstance && componentDef != nil {
		// Check for component-specific style override property (e.g., 'bar_style' for TabBar).
		barStyleStr, hasBarStyle := el.getSourcePropertyValue("bar_style")
		if hasBarStyle {
			cleanBarStyle, _ := cleanAndQuoteValue(barStyleStr)
			if cleanBarStyle != "" {
				styleIDFromBar := state.findStyleIDByName(cleanBarStyle)
				if styleIDFromBar == 0 {
					log.Printf("L%d: Warning: Component property bar_style %s not found for '%s'.\n", el.SourceLineNum, barStyleStr, componentDef.Name)
				} else {
					// Override the StyleID if 'bar_style' is valid and found.
					el.StyleID = styleIDFromBar
				}
			}
			// Mark 'bar_style' as handled.
			processedSourcePropKeys["bar_style"] = true
		}

		// 3. Apply default component style ONLY if no style was set by 'style:' or 'bar_style:'.
		if el.StyleID == 0 {
			// Example logic for TabBar default style based on orientation hint.
			if componentDef.Name == "TabBar" {
				baseStyleName := "tab_bar_style_base_row" // Default style name
				// Check the orientation hint stored earlier during component expansion.
				if el.OrientationHint == "column" || el.OrientationHint == "col" {
					baseStyleName = "tab_bar_style_base_column"
				}
				// Look up the default style ID.
				defaultStyleID := state.findStyleIDByName(baseStyleName)
				if defaultStyleID == 0 {
					log.Printf("L%d: Warning: Default component style '%s' not found for '%s'.\n", el.SourceLineNum, baseStyleName, componentDef.Name)
				} else {
					// Assign the default style ID.
					el.StyleID = defaultStyleID
				}
			}
			// Add logic here to look up default styles for other defined components...
		}
	}
	// --- Final el.StyleID is now determined ---


	// --- Process Header Fields from Source Properties ---
	// Map specific source properties directly to fields in the Element struct
	// which will later populate the KRB Element Header.
	for _, sp := range el.SourceProperties {
		key := sp.Key
		valStr := sp.ValueStr
		lineNum := sp.LineNum

		// Skip keys already handled (like 'style', 'bar_style').
		if processedSourcePropKeys[key] {
			continue
		}

		cleanVal, quotedVal := cleanAndQuoteValue(valStr) // Get cleaned values.
		handledAsHeader := false // Flag to track if the key matches a header field.
		var parseErr error      // Store potential errors during parsing.

		// Check against known header-related keys.
		switch key {
		case "id":
			handledAsHeader = true
			if quotedVal != "" {
				var idIdx uint8
				var err error
				idIdx, err = state.addString(quotedVal) // Get or add string index for the ID name.
				if err == nil {
					el.IDStringIndex = idIdx    // Store 0-based index for KRB header.
					el.SourceIDName = quotedVal // Keep original string name for debugging.
				} else {
					parseErr = err // Store error if adding string failed.
				}
			} // Ignore empty id strings.

		case "pos_x":
			handledAsHeader = true
			var v uint64
			var err error
			v, err = strconv.ParseUint(cleanVal, 10, 16) // Parse as uint16.
			if err == nil {
				el.PosX = uint16(v)
			} else {
				parseErr = fmt.Errorf("parsing pos_x uint16 from '%s': %w", cleanVal, err)
			}

		case "pos_y":
			handledAsHeader = true
			var v uint64
			var err error
			v, err = strconv.ParseUint(cleanVal, 10, 16)
			if err == nil {
				el.PosY = uint16(v)
			} else {
				parseErr = fmt.Errorf("parsing pos_y uint16 from '%s': %w", cleanVal, err)
			}

		case "width":
			handledAsHeader = true
			var v uint64
			var err error
			v, err = strconv.ParseUint(cleanVal, 10, 16)
			if err == nil {
				el.Width = uint16(v)
			} else {
				parseErr = fmt.Errorf("parsing width uint16 from '%s': %w", cleanVal, err)
			}

		case "height":
			handledAsHeader = true
			var v uint64
			var err error
			v, err = strconv.ParseUint(cleanVal, 10, 16)
			if err == nil {
				el.Height = uint16(v)
			} else {
				parseErr = fmt.Errorf("parsing height uint16 from '%s': %w", cleanVal, err)
			}

		case "layout":
			handledAsHeader = true
			// Store the parsed byte from 'layout: ...' string as a source hint.
			// The final Layout byte calculation happens later (Step 4), considering style inheritance.
			el.LayoutFlagsSource = parseLayoutString(cleanVal) // Uses helper from utils.go

		default:
			// Key doesn't match a direct header field.
			handledAsHeader = false
		}

		// Handle any parsing error encountered for this header field.
		if parseErr != nil {
			return fmt.Errorf("L%d: error processing header field '%s': %w", lineNum, key, parseErr)
		}

		// Mark the key as processed if it was handled as a header field.
		if handledAsHeader {
			processedSourcePropKeys[key] = true
		}
	} // --- End Process Header Fields Loop ---


	// --- Step 3: Resolve Remaining Properties (Standard KRB, Custom KRB, Events) ---

	// --- Temporary storage specifically for padding values during iteration ---
	var parsedPaddingTop *uint8
	var parsedPaddingRight *uint8
	var parsedPaddingBottom *uint8
	var parsedPaddingLeft *uint8
	var parsedPaddingShort string
	var foundPaddingShort bool = false
	var foundPaddingLong bool = false
	// --- End temporary padding storage ---

	// Iterate through the source properties AGAIN to handle everything not processed as a header field.
	for _, sp := range el.SourceProperties {
		key := sp.Key
		valStr := sp.ValueStr
		lineNum := sp.LineNum

		// Skip properties already processed (header fields, style directives).
		if processedSourcePropKeys[key] {
			continue
		}

		// Clean the value string for parsing.
		cleanVal, quotedVal := cleanAndQuoteValue(valStr)

		// Flags to track how this property is handled in this iteration.
		propProcessedAsEvent := false
		propProcessedAsStandardOrPaddingMargin := false // Covers standard KRB props AND padding/margin temp storage
		propProcessedAsCustom := false
		var handleErr error // Accumulate errors for this specific property.


		// A. Check for Event Handlers (e.g., onClick)
		if key == "onClick" || key == "on_click" { // Add other event keys like onChange here if needed
			propProcessedAsEvent = true
			if len(el.KrbEvents) < MaxEvents {
				if quotedVal != "" {
					var cbIdx uint8
					var addErr error
					cbIdx, addErr = state.addString(quotedVal)
					if addErr == nil {
						el.KrbEvents = append(el.KrbEvents, KrbEvent{EventTypeClick, cbIdx})
					} else {
						handleErr = fmt.Errorf("add onClick callback name '%s' to string table: %w", quotedVal, addErr)
					}
				} else {
					log.Printf("L%d: Warn: Empty callback name for '%s' ignored.\n", lineNum, key)
				}
			} else {
				handleErr = fmt.Errorf("maximum events (%d) reached for element '%s'", MaxEvents, el.SourceElementName)
			}
		} // --- End Event Check ---


		// B. Check Standard KRB Properties (if not handled as an event)
		if !propProcessedAsEvent {

			// Switch maps source keys to standard KRB Property IDs.
			switch key {

			// --- PADDING LOGIC (Store Temporarily, handle later) ---
			case "padding":
				parsedPaddingShort = cleanVal
				foundPaddingShort = true
				propProcessedAsStandardOrPaddingMargin = true
			case "padding_top":
				var v uint64
				var e error
				v, e = strconv.ParseUint(cleanVal, 10, 8)
				if e == nil { valByte := uint8(v); parsedPaddingTop = &valByte; foundPaddingLong = true; propProcessedAsStandardOrPaddingMargin = true } else { handleErr = fmt.Errorf("invalid uint8 for padding_top '%s': %w", cleanVal, e) }
			case "padding_right":
				var v uint64
				var e error
				v, e = strconv.ParseUint(cleanVal, 10, 8)
				if e == nil { valByte := uint8(v); parsedPaddingRight = &valByte; foundPaddingLong = true; propProcessedAsStandardOrPaddingMargin = true } else { handleErr = fmt.Errorf("invalid uint8 for padding_right '%s': %w", cleanVal, e) }
			case "padding_bottom":
				var v uint64
				var e error
				v, e = strconv.ParseUint(cleanVal, 10, 8)
				if e == nil { valByte := uint8(v); parsedPaddingBottom = &valByte; foundPaddingLong = true; propProcessedAsStandardOrPaddingMargin = true } else { handleErr = fmt.Errorf("invalid uint8 for padding_bottom '%s': %w", cleanVal, e) }
			case "padding_left":
				var v uint64
				var e error
				v, e = strconv.ParseUint(cleanVal, 10, 8)
				if e == nil { valByte := uint8(v); parsedPaddingLeft = &valByte; foundPaddingLong = true; propProcessedAsStandardOrPaddingMargin = true } else { handleErr = fmt.Errorf("invalid uint8 for padding_left '%s': %w", cleanVal, e) }
			// --- END PADDING LOGIC ---

			// --- MARGIN LOGIC (Using simple helper function - CORRECTED CALL) ---
			case "margin":
				handleErr = handleSimpleMarginProperty(el, PropIDMargin, cleanVal) // Call the correct helper
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)

			// --- Other Standard Properties (Add Directly) ---
			case "background_color":
				handleErr = addColorProp(el, PropIDBgColor, quotedVal, &state.HeaderFlags)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "text_color", "foreground_color":
				handleErr = addColorProp(el, PropIDFgColor, quotedVal, &state.HeaderFlags)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "border_color":
				handleErr = addColorProp(el, PropIDBorderColor, quotedVal, &state.HeaderFlags)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "border_width":
				handleErr = addByteProp(el, PropIDBorderWidth, cleanVal)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "border_radius":
				handleErr = addByteProp(el, PropIDBorderRadius, cleanVal)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "opacity":
				handleErr = addByteProp(el, PropIDOpacity, cleanVal)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "visibility", "visible":
				var vis uint8 = 1 // Default to visible
				var parseError error
				switch strings.ToLower(cleanVal) {
					case "true", "visible", "1": vis = 1
					case "false", "hidden", "0": vis = 0
					default: parseError = fmt.Errorf("invalid boolean value '%s'", cleanVal)
				}
				if parseError == nil { handleErr = el.addKrbProperty(PropIDVisibility, ValTypeByte, []byte{vis}) } else { handleErr = parseError }
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "z_index":
				zIndexInt, parseError := strconv.ParseInt(cleanVal, 10, 16)
				if parseError == nil {
					var zIndexBytes [2]byte
					binary.LittleEndian.PutUint16(zIndexBytes[:], uint16(zIndexInt))
					handleErr = el.addKrbProperty(PropIDZindex, ValTypeShort, zIndexBytes[:])
				} else { handleErr = fmt.Errorf("invalid int16 for z_index '%s': %w", cleanVal, parseError) }
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "transform":
				handleErr = state.addKrbStringProperty(el, PropIDTransform, quotedVal)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "shadow":
				handleErr = state.addKrbStringProperty(el, PropIDShadow, quotedVal)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "text", "content":
				handleErr = state.addKrbStringProperty(el, PropIDTextContent, quotedVal)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "font_size":
				handleErr = addShortProp(el, PropIDFontSize, cleanVal)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "font_weight":
				var weight uint8 = 0
				if cleanVal == "bold" || cleanVal == "700" { weight = 1 }
				handleErr = el.addKrbProperty(PropIDFontWeight, ValTypeEnum, []byte{weight})
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "text_alignment":
				var align uint8 = 0
				switch cleanVal { case "center", "centre": align = 1; case "right", "end": align = 2 }
				handleErr = el.addKrbProperty(PropIDTextAlignment, ValTypeEnum, []byte{align})
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "gap":
				handleErr = addShortProp(el, PropIDGap, cleanVal)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "min_width":
				handleErr = addShortProp(el, PropIDMinWidth, cleanVal)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "min_height":
				handleErr = addShortProp(el, PropIDMinHeight, cleanVal)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "max_width":
				handleErr = addShortProp(el, PropIDMaxWidth, cleanVal)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "max_height":
				handleErr = addShortProp(el, PropIDMaxHeight, cleanVal)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "aspect_ratio":
				handleErr = addFixedPointProp(el, PropIDAspectRatio, cleanVal, &state.HeaderFlags)
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "overflow":
				var ovf uint8 = 0
				switch cleanVal { case "hidden": ovf = 1; case "scroll": ovf = 2 }
				handleErr = el.addKrbProperty(PropIDOverflow, ValTypeEnum, []byte{ovf})
				propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
			case "image_source", "source":
				if el.Type == ElemTypeImage || el.Type == ElemTypeButton || el.IsComponentInstance {
					handleErr = state.addKrbResourceProperty(el, PropIDImageSource, ResTypeImage, quotedVal)
					propProcessedAsStandardOrPaddingMargin = (handleErr == nil)
				}
				// Implicit else: Not applicable to this type, wasn't standard
			// App-specific properties (only add if element type is App)
			case "window_width":
				if el.Type == ElemTypeApp { handleErr = addShortProp(el, PropIDWindowWidth, cleanVal); propProcessedAsStandardOrPaddingMargin = (handleErr == nil) }
			case "window_height":
				if el.Type == ElemTypeApp { handleErr = addShortProp(el, PropIDWindowHeight, cleanVal); propProcessedAsStandardOrPaddingMargin = (handleErr == nil) }
			case "window_title":
				if el.Type == ElemTypeApp { handleErr = state.addKrbStringProperty(el, PropIDWindowTitle, quotedVal); propProcessedAsStandardOrPaddingMargin = (handleErr == nil) }
			case "resizable":
				if el.Type == ElemTypeApp { b := uint8(0); if cleanVal == "true" || cleanVal == "1" { b = 1 }; handleErr = el.addKrbProperty(PropIDResizable, ValTypeByte, []byte{b}); propProcessedAsStandardOrPaddingMargin = (handleErr == nil) }
			case "icon":
				if el.Type == ElemTypeApp { handleErr = state.addKrbResourceProperty(el, PropIDIcon, ResTypeImage, quotedVal); propProcessedAsStandardOrPaddingMargin = (handleErr == nil) }
			case "version":
				if el.Type == ElemTypeApp { handleErr = state.addKrbStringProperty(el, PropIDVersion, quotedVal); propProcessedAsStandardOrPaddingMargin = (handleErr == nil) }
			case "author":
				if el.Type == ElemTypeApp { handleErr = state.addKrbStringProperty(el, PropIDAuthor, quotedVal); propProcessedAsStandardOrPaddingMargin = (handleErr == nil) }
			case "keep_aspect":
				if el.Type == ElemTypeApp { b := uint8(0); if cleanVal == "true" || cleanVal == "1" { b = 1 }; handleErr = el.addKrbProperty(PropIDKeepAspect, ValTypeByte, []byte{b}); propProcessedAsStandardOrPaddingMargin = (handleErr == nil) }
			case "scale_factor":
				if el.Type == ElemTypeApp { handleErr = addFixedPointProp(el, PropIDScaleFactor, cleanVal, &state.HeaderFlags); propProcessedAsStandardOrPaddingMargin = (handleErr == nil) }

			default:
				// Key didn't match any standard property or padding/margin key.
				// Flag that it wasn't standard *for this switch*
				propProcessedAsStandardOrPaddingMargin = false // Explicitly mark not handled here
			} // End switch key for standard properties

		} // --- End Standard Property Check ---


		// C. Check Custom Component Properties (if not handled above AND it's a component instance)
		if !propProcessedAsEvent && !propProcessedAsStandardOrPaddingMargin && isComponentInstance && componentDef != nil {
			isDeclaredComponentProp := false
			var propDefHint uint8 = ValTypeCustom // Default type hint if not specified.

			// Check if this key was declared in the component's Properties block in the Define section.
			for _, defProp := range componentDef.Properties {
				if defProp.Name == key {
					isDeclaredComponentProp = true
					propDefHint = defProp.ValueTypeHint // Use the type hint from the definition (e.g., String, Int, Color).
					break
				}
			}

			// If it IS a declared property, process it as a KRB Custom Property.
			if isDeclaredComponentProp {
				propProcessedAsCustom = true // Mark handled as custom.

				// --- Convert Key to Index ---
				keyIdx, keyErr := state.addString(key) // Get string table index for the property key name.
				if keyErr != nil {
					handleErr = fmt.Errorf("failed to add custom property key '%s' to string table: %w", key, keyErr)
				}

				// --- Convert Value String to Binary based on Type Hint ---
				if handleErr == nil {
					var customValue []byte    // Final binary value data.
					var customValueType uint8 // KRB Value Type code (e.g., ValTypeString, ValTypeShort).
					var customValueSize uint8 // Size of binary value data in bytes.
					var parseValErr error     // Error during value conversion.

					// Convert source string value (valStr) to binary based on the hint from Define->Properties.
					switch propDefHint {
					case ValTypeString, ValTypeStyleID, ValTypeResource: // Store value as string table index.
						var sIdx uint8
						var sErr error
						sIdx, sErr = state.addString(quotedVal)
						if sErr != nil { parseValErr = sErr } else { customValue = []byte{sIdx}; customValueType = ValTypeString; customValueSize = 1 }
						// If hinted as resource, also ensure it's added to the resource table.
						if propDefHint == ValTypeResource && parseValErr == nil {
							_, _ = state.addResource(guessResourceType(key), quotedVal)
						}
					case ValTypeColor: // Store as RGBA byte array (4 bytes).
						colBytes, ok := parseColor(quotedVal) // Expects format like "#RRGGBBAA"
						if !ok { parseValErr = fmt.Errorf("invalid Color format '%s'", quotedVal) } else { customValue = colBytes[:]; customValueType = ValTypeColor; customValueSize = 4; state.HeaderFlags |= FlagExtendedColor }
					case ValTypeInt, ValTypeShort: // Store as KRB Short (int16, 2 bytes little-endian).
						var v int64
						var pErr error
						v, pErr = strconv.ParseInt(cleanVal, 10, 16) // Parse as signed 16-bit first.
						if pErr != nil { parseValErr = fmt.Errorf("invalid Int '%s': %w", cleanVal, pErr) } else {
							if v < math.MinInt16 || v > math.MaxInt16 { // Check range.
								parseValErr = fmt.Errorf("int value '%s' out of range for Short", cleanVal)
							} else {
								buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(v)); customValue = buf; customValueType = ValTypeShort; customValueSize = 2
							}
						}
					case ValTypeBool, ValTypeByte: // Store as KRB Byte (0 or 1).
						var b uint8 = 0
						lc := strings.ToLower(cleanVal)
						if lc == "true" || lc == "1" { b = 1 } else if lc != "false" && lc != "0" && lc != "" { log.Printf("L%d: Warn: Invalid Bool value '%s' for component property '%s', using false.", lineNum, cleanVal, key) }
						customValue = []byte{b}; customValueType = ValTypeByte; customValueSize = 1
					case ValTypeFloat: // Store as KRB Percentage (8.8 fixed point, 2 bytes little-endian).
						var f float64
						var pErr error
						f, pErr = strconv.ParseFloat(cleanVal, 64)
						if pErr != nil { parseValErr = fmt.Errorf("invalid Float '%s': %w", cleanVal, pErr) } else {
							fp := uint16(math.Round(f * 256.0)); buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, fp); customValue = buf; customValueType = ValTypePercentage; customValueSize = 2; state.HeaderFlags |= FlagFixedPoint
						}
					default: // Unknown hint or explicit ValTypeCustom - store value as string index as fallback.
						log.Printf("L%d: Info: Treating custom prop '%s' with unknown/custom hint %d as String index.", lineNum, key, propDefHint)
						var sIdx uint8
						var sErr error
						sIdx, sErr = state.addString(quotedVal)
						if sErr != nil { parseValErr = sErr } else { customValue = []byte{sIdx}; customValueType = ValTypeString; customValueSize = 1 }
					} // End switch propDefHint

					// If value parsing and conversion succeeded, add the custom property to the element's list.
					if parseValErr == nil {
						if len(el.KrbCustomProperties) >= MaxProperties {
							handleErr = fmt.Errorf("maximum custom properties (%d) reached for element '%s'", MaxProperties, el.SourceElementName)
						} else {
							customProp := KrbCustomProperty{
								KeyIndex:   keyIdx,
								ValueType:  customValueType,
								ValueSize:  customValueSize,
								Value:      customValue,
							}
							el.KrbCustomProperties = append(el.KrbCustomProperties, customProp)
							// Final CustomPropCount set later based on len(el.KrbCustomProperties).
						}
					} else {
						// Store the error from value conversion.
						handleErr = parseValErr
					}
				} // End if keyErr == nil (key index obtained)
			} // End if isDeclaredComponentProp
		} // --- End Custom Property Check ---


		// D. Log Unhandled Property Warning
		// Only log if it wasn't event, wasn't standard/padding/margin, AND wasn't custom.
		if !propProcessedAsEvent && !propProcessedAsStandardOrPaddingMargin && !propProcessedAsCustom {
			logUnhandledPropWarning(state, el, key, lineNum)
			// Mark as processed (by warning) to avoid duplicate logs if structure changes.
			processedSourcePropKeys[key] = true
		}

		// Handle any critical error accumulated during this property's processing.
		if handleErr != nil {
			// If it was a padding/margin parsing error collected earlier, store it to return after finalization attempt.
			isPaddingMarginKey := key == "padding" || strings.HasPrefix(key, "padding_") || key == "margin" || strings.HasPrefix(key, "margin_")
			if isPaddingMarginKey {
                 log.Printf("L%d: Error recorded during processing of padding/margin key '%s': %v. Will attempt finalization.", lineNum, key, handleErr)
                 // Store error to potentially return if finalization fails? Or return immediately?
                 // For now, we log and rely on the finalization step to return errors.
			} else {
				// For non-padding/margin errors, return immediately.
				return fmt.Errorf("L%d: error processing property '%s': %w", lineNum, key, handleErr)
			}
		}

	} // --- End loop through SourceProperties ---


	// --- Step 3.1: Finalize Padding Property (Process collected values AFTER the main loop) ---
	if foundPaddingShort || foundPaddingLong {
		var finalTop, finalRight, finalBottom, finalLeft uint8 = 0, 0, 0, 0 // Default padding to 0
		var paddingErr error // Store errors specifically from this finalization step

		// --- Determine Final Values ---
		// Give precedence to long-form keys (padding_top etc.) if they were found.
		if foundPaddingLong {
			if parsedPaddingTop != nil { finalTop = *parsedPaddingTop }
			if parsedPaddingRight != nil { finalRight = *parsedPaddingRight }
			if parsedPaddingBottom != nil { finalBottom = *parsedPaddingBottom }
			if parsedPaddingLeft != nil { finalLeft = *parsedPaddingLeft }

			// Optionally log if both forms were used.
			if foundPaddingShort {
				log.Printf("L%d: Info: Both short-form 'padding' and long-form 'padding_*' keys found for element '%s'. Long-form keys ('padding_top', etc.) take precedence.", el.SourceLineNum, el.SourceElementName)
			}
		} else if foundPaddingShort {
			// Only short form was found ("padding: ..."), parse it now.
			parts := strings.Fields(parsedPaddingShort) // Split by space e.g., "10 5" -> ["10", "5"]

			// Parse based on the number of values provided.
			switch len(parts) {
			case 1: // Single value: applies to all sides.
				v, err := strconv.ParseUint(parts[0], 10, 8)
				if err != nil {
					paddingErr = fmt.Errorf("invalid uint8 value '%s' for single padding: %w", parts[0], err)
				} else {
					valByte := uint8(v)
					finalTop, finalRight, finalBottom, finalLeft = valByte, valByte, valByte, valByte
				}
			case 2: // Two values: [vertical] [horizontal].
				v1, err1 := strconv.ParseUint(parts[0], 10, 8) // Vertical (Top/Bottom)
				v2, err2 := strconv.ParseUint(parts[1], 10, 8) // Horizontal (Right/Left)
				if err1 != nil || err2 != nil {
					paddingErr = fmt.Errorf("invalid uint8 values in '%s %s' for padding: %v / %v", parts[0], parts[1], err1, err2)
				} else {
					vert, horiz := uint8(v1), uint8(v2)
					finalTop, finalBottom = vert, vert
					finalRight, finalLeft = horiz, horiz
				}
			case 4: // Four values: [top] [right] [bottom] [left].
				v1, err1 := strconv.ParseUint(parts[0], 10, 8) // Top
				v2, err2 := strconv.ParseUint(parts[1], 10, 8) // Right
				v3, err3 := strconv.ParseUint(parts[2], 10, 8) // Bottom
				v4, err4 := strconv.ParseUint(parts[3], 10, 8) // Left
				if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
					paddingErr = fmt.Errorf("invalid uint8 values in '%s %s %s %s' for padding: %v/%v/%v/%v", parts[0], parts[1], parts[2], parts[3], err1, err2, err3, err4)
				} else {
					finalTop, finalRight, finalBottom, finalLeft = uint8(v1), uint8(v2), uint8(v3), uint8(v4)
				}
			default: // Invalid number of values for shorthand.
				paddingErr = fmt.Errorf("invalid number of values (%d) for padding shorthand '%s', expected 1, 2, or 4", len(parts), parsedPaddingShort)
			}
		} // End parsing short form

		// --- Handle Errors and Add KRB Property ---
		// If any error occurred during the parsing of padding values.
		if paddingErr != nil {
			return fmt.Errorf("L%d: error processing padding for element '%s': %w", el.SourceLineNum, el.SourceElementName, paddingErr)
		}

		// Create the 4-byte buffer for the EdgeInsets value.
		paddingBuf := []byte{finalTop, finalRight, finalBottom, finalLeft}

		// Add the single KRB property for padding.
		addErr := el.addKrbProperty(PropIDPadding, ValTypeEdgeInsets, paddingBuf)
		if addErr != nil {
			// This error (e.g., MaxProperties reached) should be fatal.
			return fmt.Errorf("L%d: failed to add final KRB padding property for element '%s': %w", el.SourceLineNum, el.SourceElementName, addErr)
		}

		// Mark ALL potential padding keys as definitively processed in the main map
		// to ensure they don't trigger the unhandled warning later.
		processedSourcePropKeys["padding"] = true
		processedSourcePropKeys["padding_top"] = true
		processedSourcePropKeys["padding_right"] = true
		processedSourcePropKeys["padding_bottom"] = true
		processedSourcePropKeys["padding_left"] = true

	} // --- End Finalize Padding Property ---


	// --- Step 3.2: Finalize Margin Property (if implemented similarly) ---
	// if foundMarginShort || foundMarginLong { ... Add similar logic ... }


    // --- Step 3.5: ADD THE _componentName CONVENTION PROPERTY ---
	// Add the special _componentName property *only* if this element
	// was directly expanded from a component definition (`isComponentInstance` flag is true).
	// This allows the runtime to reliably identify the original component type.
	if isComponentInstance && componentDef != nil {
		var conventionErr error
		// log.Printf("   Adding convention property '_componentName: %s' for Elem %d ('%s')\n", componentDef.Name, el.SelfIndex, el.SourceElementName)

		// Get string index for the convention key "_componentName".
		var keyIdx uint8
		var keyErr error
		keyIdx, keyErr = state.addString(componentNameConventionKey)
		if keyErr != nil {
			conventionErr = fmt.Errorf("failed to add convention key '%s' to string table: %w", componentNameConventionKey, keyErr)
		}

		// Get string index for the component's actual name (e.g., "TabBar").
		var valIdx uint8
		var valErr error
		if conventionErr == nil { // Only proceed if key was added
			valIdx, valErr = state.addString(componentDef.Name)
			if valErr != nil {
				conventionErr = fmt.Errorf("failed to add component name '%s' to string table: %w", componentDef.Name, valErr)
			}
		}


		// Create and add the custom property if indices were obtained successfully.
		if conventionErr == nil {
			if len(el.KrbCustomProperties) >= MaxProperties {
				conventionErr = fmt.Errorf("maximum custom properties (%d) reached before adding _componentName for '%s'", MaxProperties, componentDef.Name)
			} else {
				// Create the custom property entry for the component name.
				componentNameProp := KrbCustomProperty{
					KeyIndex:   keyIdx,          // Index of "_componentName"
					ValueType:  ValTypeString,   // Value is a string index
					ValueSize:  1,               // Size of the string index (1 byte)
					Value:      []byte{valIdx},  // The index of the component name string ("TabBar")
				}
				// Append it to the element's list of custom properties.
				el.KrbCustomProperties = append(el.KrbCustomProperties, componentNameProp)
				// Final CustomPropCount set later based on len(el.KrbCustomProperties).
			}
		}

		// Handle any error from adding the convention property.
		if conventionErr != nil {
			return fmt.Errorf("L%d: error adding component name convention property for '%s': %w", el.SourceLineNum, componentDef.Name, conventionErr)
		}
	} // --- END Step 3.5 ---


	// --- Step 4: Finalize Layout Byte ---
	// Determine the final layout byte based on precedence:
	// 1. Explicit 'layout:' property on element/component usage.
	// 2. Layout defined in the applied style.
	// 3. Default layout.

	var finalLayout uint8 = 0 // Start with default layout assumption
	var layoutSource string = "Default" // For debugging
	var styleLayoutByte uint8 = 0
	var styleLayoutFound bool = false

	// Check if the resolved style defines layout flags.
	if el.StyleID > 0 {
		style := state.findStyleByID(el.StyleID) // findStyleByID defined elsewhere
		if style != nil {
			if style.IsResolved { // Ensure style itself was resolved
				// Look for PropIDLayoutFlags within the style's resolved KRB properties.
				for _, prop := range style.Properties {
					if prop.PropertyID == PropIDLayoutFlags && prop.ValueType == ValTypeByte && prop.Size == 1 {
						styleLayoutByte = prop.Value[0]
						styleLayoutFound = true
						break // Found the layout property in the style
					}
				}
			} else {
				// This should ideally not happen if the style resolution pass worked correctly.
				log.Printf("CRITICAL WARN: Style %d ('%s') for Elem %d ('%s') was not marked as resolved when checking layout flags!", el.StyleID, style.SourceName, el.SelfIndex, el.SourceElementName)
			}
		}
	}

	// Determine final layout based on precedence.
	if el.LayoutFlagsSource != 0 {
		// Highest precedence: Explicit 'layout:' property found on the element.
		finalLayout = el.LayoutFlagsSource
		layoutSource = "Explicit"
	} else if styleLayoutFound {
		// Next precedence: Layout defined in the applied style.
		finalLayout = styleLayoutByte
		layoutSource = "Style"
	} else {
		// Lowest precedence: Default layout if none specified explicitly or in style.
		finalLayout = LayoutDirectionColumn | LayoutAlignmentStart // Default: Column, Align Start.
		layoutSource = "Default"
	}

	// Store the final calculated layout byte on the element struct.
	el.Layout = finalLayout

	// Optional logging for layout debugging.
	// Only log if it's not the absolute default to reduce noise.
	if layoutSource != "Default" || finalLayout != (LayoutDirectionColumn|LayoutAlignmentStart) {
		log.Printf("   >> Elem %d ('%s') Final Layout Byte: 0x%02X (Source: %s)\n", el.SelfIndex, el.SourceElementName, el.Layout, layoutSource)
	}
	// --- End Step 4 ---


	// --- Step 5: Recursively Resolve Children ---

	// Prepare the runtime Children slice (pointers to other elements in the main state.Elements slice).
	el.Children = make([]*Element, 0, len(el.SourceChildrenIndices))
	el.ChildCount = 0 // Final count will be set later based on len(el.Children).

	// Iterate through the indices of children identified during the initial parsing phase.
	for _, childIndex := range el.SourceChildrenIndices {
		// Basic bounds check.
		if childIndex < 0 || childIndex >= len(state.Elements) {
			return fmt.Errorf("L%d: invalid child index %d found for parent element '%s'", el.SourceLineNum, childIndex, el.SourceElementName)
		}

		// Recursively call this function for the child element.
		childErr := state.resolveElementRecursive(childIndex)
		if childErr != nil {
			// Provide context in the error message if child resolution fails.
			childName := fmt.Sprintf("index %d", childIndex)
			if childIndex >= 0 && childIndex < len(state.Elements) {
				childName = fmt.Sprintf("'%s' (index %d)", state.Elements[childIndex].SourceElementName, childIndex)
			}
			return fmt.Errorf("error resolving child %s of element '%s': %w", childName, el.SourceElementName, childErr)
		}

		// If resolution succeeded, add a pointer to the resolved child element struct.
		// Double-check bounds after recursion (shouldn't change, but defensive).
		if childIndex < len(state.Elements) {
			el.Children = append(el.Children, &state.Elements[childIndex])
		} else {
			// This indicates a severe internal inconsistency.
			return fmt.Errorf("internal error: child index %d became invalid after recursive resolve for parent '%s'", childIndex, el.SourceElementName)
		}
	}
	// Final el.ChildCount is set later before writing, based on len(el.Children).
	// --- End Step 5 ---


	// --- Resolution successful for this element and its children ---
	return nil

} // --- End resolveElementRecursive ---


// --- Property Adding Helpers ---
// These helpers add STANDARD KRB properties to el.KrbProperties.

// addColorProp parses a color string and adds it as a standard KRB property.
func addColorProp(el *Element, propID uint8, valueStr string, headerFlags *uint16) error {
	col, ok := parseColor(valueStr) // Uses helper from utils.go
	if !ok {
		return fmt.Errorf("invalid color format '%s'", valueStr)
	}
	err := el.addKrbProperty(propID, ValTypeColor, col[:]) // addKrbProperty is method on Element
	if err == nil {
		*headerFlags |= FlagExtendedColor // Ensure flag is set if using RGBA color
	}
	return err
}

// addByteProp parses a uint8 string and adds it as a standard KRB property.
func addByteProp(el *Element, propID uint8, cleanVal string) error {
	v, err := strconv.ParseUint(cleanVal, 10, 8)
	if err != nil {
		return fmt.Errorf("invalid uint8 value '%s': %w", cleanVal, err)
	}
	return el.addKrbProperty(propID, ValTypeByte, []byte{uint8(v)})
}

// addShortProp parses a uint16 string and adds it as a standard KRB property.
func addShortProp(el *Element, propID uint8, cleanVal string) error {
	v, err := strconv.ParseUint(cleanVal, 10, 16)
	if err != nil {
		return fmt.Errorf("invalid uint16 value '%s': %w", cleanVal, err)
	}
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(v))
	return el.addKrbProperty(propID, ValTypeShort, buf)
}

// addFixedPointProp parses a float string, converts to 8.8 fixed point, and adds as standard KRB property.
func addFixedPointProp(el *Element, propID uint8, cleanVal string, headerFlags *uint16) error {
	f, err := strconv.ParseFloat(cleanVal, 64)
	if err != nil {
		return fmt.Errorf("invalid float value '%s': %w", cleanVal, err)
	}
	fixedPointVal := uint16(math.Round(f * 256.0)) // Convert float to 8.8 fixed-point uint16
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, fixedPointVal)
	err = el.addKrbProperty(propID, ValTypePercentage, buf)
	if err == nil {
		*headerFlags |= FlagFixedPoint // Ensure flag is set when using fixed-point
	}
	return err
}

// addEdgeInsetsProp handles simple EdgeInsets where one value applies to all sides.
// TODO: Enhance to handle multi-value strings like "10", "5 10", "5 10 15 20".
func addEdgeInsetsProp(el *Element, propID uint8, cleanVal string) error {
	v, err := strconv.ParseUint(cleanVal, 10, 8) // Assuming uint8 for padding/margin values
	if err != nil {
		return fmt.Errorf("invalid uint8 value for edge inset '%s': %w", cleanVal, err)
	}
	valByte := uint8(v)
	// Apply the same value to top, right, bottom, left
	buf := []byte{valByte, valByte, valByte, valByte}
	return el.addKrbProperty(propID, ValTypeEdgeInsets, buf)
}


func (state *CompilerState) getStringIndex(text string) (uint8, bool) {
	cleaned := trimQuotes(strings.TrimSpace(text)) // Ensure consistent lookup
	if cleaned == "" {
		// Ensure index 0 exists if needed (for empty string)
		if len(state.Strings) == 0 {
			state.Strings = append(state.Strings, StringEntry{Text: "", Length: 0, Index: 0})
		}
		return 0, true // Index 0 is always the empty string
	}
	// Search existing strings (skip index 0)
	for i := 1; i < len(state.Strings); i++ {
		if state.Strings[i].Text == cleaned {
			return state.Strings[i].Index, true // Found it
		}
	}
	return 0, false // Not found (and it wasn't the empty string)
}

// --- logUnhandledPropWarning ---
// Logs warnings for properties that weren't mapped to standard KRB props, events, or declared custom component props.
func logUnhandledPropWarning(state *CompilerState, el *Element, key string, lineNum int) {
	// Define keys that are explicitly handled elsewhere (header fields, style resolution, event processing)
	// to avoid logging warnings for them.
	handledKeys := map[string]bool{
		"id":          true, "style": true, "layout": true,
		"pos_x":       true, "pos_y": true, "width": true, "height": true,
		"bar_style":   true, // Consumed during component style resolution
		"orientation": true, // Consumed for component hint/default style
		"onClick":     true, "on_click": true, // Handled as events
		// Add any other keys that are intentionally consumed without becoming KRB props/events
	}

	if handledKeys[key] {
		return // This key was handled intentionally elsewhere.
	}

	// Check if it ended up as a KRB custom property (meaning it was declared in Define->Properties).
	isCustomProp := false
	if el.IsComponentInstance {
		keyIdx, keyExists := state.getStringIndex(key) // Use method on state
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
		return // Don't warn if it successfully became a custom property.
	}

	// If it reaches here, the property was neither standard, nor event, nor custom KRB property.
	if el.IsComponentInstance && el.ComponentDef != nil {
		// Check if it was *declared* in the component definition but still wasn't handled.
		isDeclared := false
		for _, propDef := range el.ComponentDef.Properties {
			if propDef.Name == key { isDeclared = true; break }
		}
		if isDeclared {
			// It was declared in Define->Properties but wasn't processed.
			// This might indicate an internal compiler error or an unsupported type hint.
			log.Printf("L%d: Info: Declared component property '%s' for '%s' was not mapped to standard or custom KRB property. Check type hints and resolver logic. Ignored.\n", lineNum, key, el.ComponentDef.Name)
		} else {
			// Found on a component instance, but not declared in its definition. Likely a typo by the user.
			log.Printf("L%d: Warning: Unhandled/undeclared property '%s' found on component instance '%s'. Ignored.\n", lineNum, key, el.ComponentDef.Name)
		}
	} else {
		// Found on a standard element, but doesn't match any known KRB property or event. Likely a typo.
		log.Printf("L%d: Warning: Unhandled property '%s' found for standard element '%s'. Ignored.\n", lineNum, key, el.SourceElementName)
	}
}


