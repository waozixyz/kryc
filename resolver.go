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

// --- resolveElementRecursive ---
// Core recursive function for resolving a single element.
// This function:
// 1. Expands component usage into its base element type.
// 2. Merges properties from the component definition, defaults, and usage tag.
// 3. Resolves the final Style ID.
// 4. Processes source properties into KRB Header fields.
// 5. Processes remaining source properties into Standard KRB Properties, Custom KRB Properties, or Events.
// 6. Adds the conventional `_componentName` custom property if it's a component instance.
// 7. Calculates the final Layout byte.
// 8. Recursively calls itself for child elements.
func (state *CompilerState) resolveElementRecursive(elementIndex int) error {
	// Bounds check for safety.
	if elementIndex < 0 || elementIndex >= len(state.Elements) {
		return fmt.Errorf("internal error: invalid element index %d during resolution", elementIndex)
	}
	// Get a pointer to the element in the main slice to modify it directly.
	el := &state.Elements[elementIndex]

	// Avoid processing the same element multiple times in this pass.
	if el.ProcessedInPass15 {
		return nil
	}
	el.ProcessedInPass15 = true // Mark as visited for this pass.

	// Variables to hold component-specific info if this element is an instance.
	var originalSourceProperties []SourceProperty // Stores properties from the usage tag (e.g., <TabBar id="nav">)
	var componentDef *ComponentDefinition = nil    // Points to the component's definition (Define TabBar { ... })
	isComponentInstance := false                   // Local flag to track if expansion happened *in this specific call*.

	// --- Step 1: Expand Component (if this element represents a component usage marker) ---
	if el.Type == ElemTypeInternalComponentUsage {
		if el.ComponentDef == nil {
			return fmt.Errorf("L%d: internal error: component instance '%s' has nil definition", el.SourceLineNum, el.SourceElementName)
		}
		componentDef = el.ComponentDef // Store pointer to the definition.
		isComponentInstance = true     // Mark that expansion is occurring.

		// Store the original properties defined on the usage tag before merging.
		originalSourceProperties = make([]SourceProperty, len(el.SourceProperties))
		copy(originalSourceProperties, el.SourceProperties)

		// Determine the root element type specified in the 'Define' block.
		rootType := componentDef.DefinitionRootType
		if rootType == "" {
			log.Printf("L%d: Warning: Component definition '%s' has no root element type specified. Defaulting to 'Container'.\n", componentDef.DefinitionStartLine, componentDef.Name)
			rootType = "Container"
		}

		// Update the element's type to match the component's defined root type (e.g., Container for TabBar).
		el.Type = getElementTypeFromName(rootType) // Uses helper from utils.go
		if el.Type == ElemTypeUnknown {
			// If the root type isn't standard, mark as custom and store its name.
			el.Type = ElemTypeCustomBase // Use the base custom type ID.
			nameIdx, err := state.addString(rootType)
			if err != nil {
				return fmt.Errorf("L%d: failed adding component root type name '%s' to string table: %w", el.SourceLineNum, rootType, err)
			}
			el.IDStringIndex = nameIdx // Use the ID field to store the custom type name index.
			log.Printf("L%d: Info: Component '%s' expands to unknown root type '%s', using custom type 0x%X with name index %d\n", el.SourceLineNum, componentDef.Name, rootType, el.Type, nameIdx)
		}

		// Keep the flag indicating this element originated from a component.
		el.IsComponentInstance = true

		// --- Merge Properties (Definition Root -> Definition Defaults -> Usage Tag) ---
		mergedPropsMap := make(map[string]SourceProperty)

		// 1. Start with properties defined *on the root element* within the Define block.
		for _, prop := range componentDef.DefinitionRootProperties {
			mergedPropsMap[prop.Key] = prop
		}
		// 2. Add default values for properties declared in `Properties {}`, if not already set.
		for _, propDef := range componentDef.Properties {
			if _, exists := mergedPropsMap[propDef.Name]; !exists && propDef.DefaultValueStr != "" {
				cleanDefaultVal, _ := cleanAndQuoteValue(propDef.DefaultValueStr) // Clean the default value.
				mergedPropsMap[propDef.Name] = SourceProperty{
					Key:      propDef.Name,
					ValueStr: cleanDefaultVal,
					LineNum:  componentDef.DefinitionStartLine, // Attribute line num to definition.
				}
			}
		}
		// 3. Apply properties from the usage tag (e.g., <TabBar id="nav" ...>), overwriting definition/defaults.
		for _, prop := range originalSourceProperties {
			mergedPropsMap[prop.Key] = prop
		}

		// Replace the element's source properties with the final merged result.
		el.SourceProperties = make([]SourceProperty, 0, len(mergedPropsMap))
		for _, prop := range mergedPropsMap {
			el.SourceProperties = append(el.SourceProperties, prop)
		}

		// Store component-specific hints (like position/orientation) if present in merged props.
		// These hints might be used later (e.g., by the writer for simple reordering).
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
	} // End Component Expansion block

	// --- Step 2: Reset Resolved Data & Process Standard Header Fields ---
	// Clear out any previously resolved KRB data for this element.
	el.KrbProperties = el.KrbProperties[:0]       // Standard KRB properties
	el.PropertyCount = 0
	el.KrbCustomProperties = el.KrbCustomProperties[:0] // Custom KRB properties
	el.CustomPropCount = 0
	el.KrbEvents = el.KrbEvents[:0]               // KRB events
	el.EventCount = 0
	el.LayoutFlagsSource = 0                       // Reset layout hint derived directly from source 'layout:'

	// Track which source property keys are processed in this step or by style logic.
	processedSourcePropKeys := make(map[string]bool)

	// --- Determine Final Style ID ---
	// Precedence: Explicit `bar_style` (if component) > Explicit `style` > Default component style > No style.
	el.StyleID = 0 // Reset StyleID for this element.

	// 1. Check for direct 'style:' property.
	directStyleStr, hasDirectStyle := el.getSourcePropertyValue("style")
	if hasDirectStyle {
		cleanDirectStyle, _ := cleanAndQuoteValue(directStyleStr)
		if cleanDirectStyle != "" {
			styleID := state.findStyleIDByName(cleanDirectStyle) // findStyleIDByName is needed here
			if styleID == 0 {
				log.Printf("L%d: Warning: Style %s not found for element '%s'. Check style definitions and includes.\n", el.SourceLineNum, directStyleStr, el.SourceElementName)
			} else {
				el.StyleID = styleID
			}
		}
		processedSourcePropKeys["style"] = true // Mark 'style' as handled.
	}

	// 2. If it's a component, check for specific overrides (like 'bar_style') and defaults.
	if el.IsComponentInstance && componentDef != nil {
		barStyleStr, hasBarStyle := el.getSourcePropertyValue("bar_style")
		if hasBarStyle {
			cleanBarStyle, _ := cleanAndQuoteValue(barStyleStr)
			if cleanBarStyle != "" {
				styleIDFromBar := state.findStyleIDByName(cleanBarStyle)
				if styleIDFromBar == 0 {
					log.Printf("L%d: Warning: Component property bar_style %s not found for '%s'.\n", el.SourceLineNum, barStyleStr, componentDef.Name)
				} else {
					// Override StyleID if bar_style is valid.
					el.StyleID = styleIDFromBar
				}
			}
			processedSourcePropKeys["bar_style"] = true // Mark 'bar_style' as handled.
		}

		// 3. Apply default component style ONLY if no style was set above.
		if el.StyleID == 0 {
			if componentDef.Name == "TabBar" { // Example for TabBar
				baseStyleName := "tab_bar_style_base_row" // Default
				if el.OrientationHint == "column" || el.OrientationHint == "col" {
					baseStyleName = "tab_bar_style_base_column"
				}
				defaultStyleID := state.findStyleIDByName(baseStyleName)
				if defaultStyleID == 0 {
					log.Printf("L%d: Warning: Default component style '%s' not found for '%s'.\n", el.SourceLineNum, baseStyleName, componentDef.Name)
				} else {
					el.StyleID = defaultStyleID
				}
			}
			// Add other component default style logic here...
		}
	}
	// Final el.StyleID is now determined.

	// --- Process Header Fields from Source Properties ---
	// Map source properties like `id`, `pos_x`, `width` directly to Element struct fields.
	for _, sp := range el.SourceProperties {
		key := sp.Key
		valStr := sp.ValueStr
		lineNum := sp.LineNum

		if processedSourcePropKeys[key] {
			continue // Already handled (likely 'style' or 'bar_style').
		}

		cleanVal, quotedVal := cleanAndQuoteValue(valStr) // Use helpers from utils.go
		handledAsHeader := true
		var parseErr error

		switch key {
		case "id":
			if quotedVal != "" {
				idIdx, err := state.addString(quotedVal) // addString is method on state
				if err == nil {
					el.IDStringIndex = idIdx    // Store 0-based index for KRB header.
					el.SourceIDName = quotedVal // Keep original string for debugging.
				} else {
					parseErr = err
				}
			} // Ignore empty id.
		case "pos_x":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.PosX = uint16(v) } else { parseErr = fmt.Errorf("pos_x uint16 '%s': %w", cleanVal, err) }
		case "pos_y":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.PosY = uint16(v) } else { parseErr = fmt.Errorf("pos_y uint16 '%s': %w", cleanVal, err) }
		case "width":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.Width = uint16(v) } else { parseErr = fmt.Errorf("width uint16 '%s': %w", cleanVal, err) }
		case "height":
			if v, err := strconv.ParseUint(cleanVal, 10, 16); err == nil { el.Height = uint16(v) } else { parseErr = fmt.Errorf("height uint16 '%s': %w", cleanVal, err) }
		case "layout":
			// Store the parsed byte from 'layout: ...' string as a hint.
			// The final Layout byte calculation happens later (Step 4).
			el.LayoutFlagsSource = parseLayoutString(cleanVal) // Uses helper from utils.go
		default:
			handledAsHeader = false // Not a direct header field.
		}

		if parseErr != nil {
			return fmt.Errorf("L%d: error processing header field '%s': %w", lineNum, key, parseErr)
		}
		if handledAsHeader {
			processedSourcePropKeys[key] = true
		}
	}

	// --- Step 3: Resolve Remaining Properties (Standard KRB, Custom KRB, Events) ---
	// Process properties not handled as header fields.
	for _, sp := range el.SourceProperties {
		key := sp.Key
		valStr := sp.ValueStr
		lineNum := sp.LineNum

		if processedSourcePropKeys[key] {
			continue // Skip already processed keys.
		}

		cleanVal, quotedVal := cleanAndQuoteValue(valStr)
		propProcessed := false // Track if this property was handled in this step.
		var err error          // Accumulate errors for this property.

		// A. Check for Event Handlers
		if key == "onClick" || key == "on_click" {
			propProcessed = true
			processedSourcePropKeys[key] = true
			if len(el.KrbEvents) < MaxEvents {
				if quotedVal != "" {
					cbIdx, addErr := state.addString(quotedVal)
					if addErr == nil {
						el.KrbEvents = append(el.KrbEvents, KrbEvent{EventTypeClick, cbIdx})
						// Final EventCount set later.
					} else {
						err = fmt.Errorf("add onClick callback name '%s' to string table: %w", quotedVal, addErr)
					}
				} else {
					log.Printf("L%d: Warn: Empty callback name for '%s' ignored.\n", lineNum, key)
				}
			} else {
				err = fmt.Errorf("maximum events (%d) reached for element '%s'", MaxEvents, el.SourceElementName)
			}
		} // End event check.

		// B. Check Standard KRB Properties (if not an event)
		if !propProcessed {
			propAddedAsStandard := true // Assume standard unless key doesn't match.
			// Switch maps source keys to standard KRB Property IDs and adds them to el.KrbProperties.
			switch key {
			case "background_color": err = addColorProp(el, PropIDBgColor, quotedVal, &state.HeaderFlags)
			case "text_color", "foreground_color": err = addColorProp(el, PropIDFgColor, quotedVal, &state.HeaderFlags)
			case "border_color": err = addColorProp(el, PropIDBorderColor, quotedVal, &state.HeaderFlags)
			case "border_width": err = addByteProp(el, PropIDBorderWidth, cleanVal)
			case "border_radius": err = addByteProp(el, PropIDBorderRadius, cleanVal)
			case "opacity": err = addByteProp(el, PropIDOpacity, cleanVal)
			case "visibility", "visible":
				vis := uint8(1); switch strings.ToLower(cleanVal) { case "true", "visible", "1": vis = 1; case "false", "hidden", "0": vis = 0; default: err = fmt.Errorf("invalid boolean value '%s'", cleanVal) }; if err == nil { err = el.addKrbProperty(PropIDVisibility, ValTypeByte, []byte{vis}) }
			case "z_index": err = addShortProp(el, PropIDZindex, cleanVal)
			case "transform": err = state.addKrbStringProperty(el, PropIDTransform, quotedVal)
			case "shadow": err = state.addKrbStringProperty(el, PropIDShadow, quotedVal)
			case "text", "content": err = state.addKrbStringProperty(el, PropIDTextContent, quotedVal)
			case "font_size": err = addShortProp(el, PropIDFontSize, cleanVal)
			case "font_weight": weight := uint8(0); if cleanVal == "bold" || cleanVal == "700" { weight = 1 }; err = el.addKrbProperty(PropIDFontWeight, ValTypeEnum, []byte{weight})
			case "text_alignment": align := uint8(0); switch cleanVal { case "center", "centre": align = 1; case "right", "end": align = 2 }; err = el.addKrbProperty(PropIDTextAlignment, ValTypeEnum, []byte{align})
			case "gap": err = addShortProp(el, PropIDGap, cleanVal)
			case "padding": err = addEdgeInsetsProp(el, PropIDPadding, cleanVal)
			case "margin": err = addEdgeInsetsProp(el, PropIDMargin, cleanVal)
			case "min_width": err = addShortProp(el, PropIDMinWidth, cleanVal)
			case "min_height": err = addShortProp(el, PropIDMinHeight, cleanVal)
			case "max_width": err = addShortProp(el, PropIDMaxWidth, cleanVal)
			case "max_height": err = addShortProp(el, PropIDMaxHeight, cleanVal)
			case "aspect_ratio": err = addFixedPointProp(el, PropIDAspectRatio, cleanVal, &state.HeaderFlags)
			case "overflow": ovf := uint8(0); switch cleanVal { case "hidden": ovf = 1; case "scroll": ovf = 2 }; err = el.addKrbProperty(PropIDOverflow, ValTypeEnum, []byte{ovf})
			case "image_source", "source": if el.Type == ElemTypeImage || el.Type == ElemTypeButton || el.IsComponentInstance { err = state.addKrbResourceProperty(el, PropIDImageSource, ResTypeImage, quotedVal) } else { propAddedAsStandard = false }
			// App-specific properties (only added if element type is App)
			case "window_width": if el.Type == ElemTypeApp { err = addShortProp(el, PropIDWindowWidth, cleanVal) } else { propAddedAsStandard = false }
			case "window_height": if el.Type == ElemTypeApp { err = addShortProp(el, PropIDWindowHeight, cleanVal) } else { propAddedAsStandard = false }
			case "window_title": if el.Type == ElemTypeApp { err = state.addKrbStringProperty(el, PropIDWindowTitle, quotedVal) } else { propAddedAsStandard = false }
			case "resizable": if el.Type == ElemTypeApp { b := uint8(0); if cleanVal == "true" || cleanVal == "1" { b = 1 }; err = el.addKrbProperty(PropIDResizable, ValTypeByte, []byte{b}) } else { propAddedAsStandard = false }
			case "icon": if el.Type == ElemTypeApp { err = state.addKrbResourceProperty(el, PropIDIcon, ResTypeImage, quotedVal) } else { propAddedAsStandard = false }
			case "version": if el.Type == ElemTypeApp { err = state.addKrbStringProperty(el, PropIDVersion, quotedVal) } else { propAddedAsStandard = false }
			case "author": if el.Type == ElemTypeApp { err = state.addKrbStringProperty(el, PropIDAuthor, quotedVal) } else { propAddedAsStandard = false }
			case "keep_aspect": if el.Type == ElemTypeApp { b := uint8(0); if cleanVal == "true" || cleanVal == "1" { b = 1 }; err = el.addKrbProperty(PropIDKeepAspect, ValTypeByte, []byte{b}) } else { propAddedAsStandard = false }
			case "scale_factor": if el.Type == ElemTypeApp { err = addFixedPointProp(el, PropIDScaleFactor, cleanVal, &state.HeaderFlags) } else { propAddedAsStandard = false }
			default: propAddedAsStandard = false // Key didn't match any standard property.
			} // End switch key for standard properties

			if propAddedAsStandard {
				propProcessed = true
				processedSourcePropKeys[key] = true
				if err != nil {
					return fmt.Errorf("L%d: error processing standard property '%s': %w", lineNum, key, err)
				}
			}
		} // End standard property check.

		// C. Check Custom Component Properties (if not standard/event AND it's a component instance)
		// This adds properties declared in the component's `Properties {}` block to the KRB custom properties section.
		if !propProcessed && isComponentInstance && componentDef != nil {
			isDeclaredComponentProp := false
			var propDefHint uint8 = ValTypeCustom // Default type hint.

			// Check if this key was declared in the component's Properties block.
			for _, defProp := range componentDef.Properties {
				if defProp.Name == key {
					isDeclaredComponentProp = true
					propDefHint = defProp.ValueTypeHint // Use the hint from the definition (e.g., String, Int, Color).
					break
				}
			}

			// If it IS a declared property, process it as a KRB Custom Property.
			if isDeclaredComponentProp {
				propProcessed = true
				processedSourcePropKeys[key] = true

				keyIdx, keyErr := state.addString(key) // Get index for the property key name.
				if keyErr != nil {
					err = fmt.Errorf("failed to add custom property key '%s' to string table: %w", key, keyErr)
				}

				// Convert the property value string based on the type hint from the definition.
				if err == nil {
					var customValue []byte    // Final binary value data.
					var customValueType uint8 // KRB Value Type code.
					var customValueSize uint8 // Size of binary value data.
					parseValErr := error(nil)

					// Convert source string value (valStr) to binary based on hint.
					switch propDefHint {
					case ValTypeString, ValTypeStyleID, ValTypeResource: // Store value as string table index.
						sIdx, sErr := state.addString(quotedVal)
						if sErr != nil { parseValErr = sErr } else { customValue = []byte{sIdx}; customValueType = ValTypeString; customValueSize = 1 }
						if propDefHint == ValTypeResource { _, _ = state.addResource(guessResourceType(key), quotedVal) } // Also add to resources if hinted.
					case ValTypeColor: // Store as RGBA byte array.
						col, ok := parseColor(quotedVal); if !ok { parseValErr = fmt.Errorf("invalid Color format '%s'", quotedVal) } else { customValue = col[:]; customValueType = ValTypeColor; customValueSize = 4; state.HeaderFlags |= FlagExtendedColor }
					case ValTypeInt, ValTypeShort: // Store as KRB Short (int16).
						v, pErr := strconv.ParseInt(cleanVal, 10, 16); if pErr != nil { parseValErr = fmt.Errorf("invalid Int '%s': %w", cleanVal, pErr) } else { if v < math.MinInt16 || v > math.MaxInt16 { parseValErr = fmt.Errorf("int value '%s' out of range for Short", cleanVal) } else { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(v)); customValue = buf; customValueType = ValTypeShort; customValueSize = 2 } }
					case ValTypeBool, ValTypeByte: // Store as KRB Byte (0 or 1).
						b := uint8(0); lc := strings.ToLower(cleanVal); if lc == "true" || lc == "1" { b = 1 } else if lc != "false" && lc != "0" && lc != "" { log.Printf("L%d: Warn: Invalid Bool value '%s' for component property '%s', using false.", lineNum, cleanVal, key) }; customValue = []byte{b}; customValueType = ValTypeByte; customValueSize = 1
					case ValTypeFloat: // Store as KRB Percentage (8.8 fixed point).
						f, pErr := strconv.ParseFloat(cleanVal, 64); if pErr != nil { parseValErr = fmt.Errorf("invalid Float '%s': %w", cleanVal, pErr) } else { fp := uint16(math.Round(f * 256.0)); buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, fp); customValue = buf; customValueType = ValTypePercentage; customValueSize = 2; state.HeaderFlags |= FlagFixedPoint }
					default: // Unknown hint or explicit ValTypeCustom - store as string index.
						log.Printf("L%d: Info: Treating custom prop '%s' with unknown/custom hint %d as String index.", lineNum, key, propDefHint)
						sIdx, sErr := state.addString(quotedVal); if sErr != nil { parseValErr = sErr } else { customValue = []byte{sIdx}; customValueType = ValTypeString; customValueSize = 1 }
					} // End switch propDefHint

					// If value parsing succeeded, add the custom property to the element's list.
					if parseValErr == nil {
						if len(el.KrbCustomProperties) >= MaxProperties {
							err = fmt.Errorf("maximum custom properties (%d) reached for element '%s'", MaxProperties, el.SourceElementName)
						} else {
							customProp := KrbCustomProperty{KeyIndex: keyIdx, ValueType: customValueType, ValueSize: customValueSize, Value: customValue}
							el.KrbCustomProperties = append(el.KrbCustomProperties, customProp)
							// Final CustomPropCount set later.
						}
					} else {
						// Error occurred during value conversion.
						err = parseValErr
					}
				} // End if keyErr == nil
			} // End if isDeclaredComponentProp
		} // End custom property check.

		// D. Log Unhandled Property Warning (if not processed by any step above)
		if !propProcessed {
			logUnhandledPropWarning(state, el, key, lineNum) // Use helper function.
		}
		// Handle any error accumulated during this property's processing.
		if err != nil {
			return fmt.Errorf("L%d: error processing property '%s': %w", lineNum, key, err)
		}
	} // End loop through remaining source properties.

	// --- Step 3.5: ADD THE _componentName CONVENTION PROPERTY ---
	// Add the special _componentName property *only* if this element
	// was directly expanded from a component definition (`isComponentInstance` flag is true).
	// This allows the runtime to reliably identify the original component type.
	if isComponentInstance && componentDef != nil {
		var conventionErr error
		// log.Printf("   Adding convention property '_componentName: %s' for Elem %d ('%s')\n", componentDef.Name, el.SelfIndex, el.SourceElementName)

		// Get string index for the convention key "_componentName".
		keyIdx, keyErr := state.addString(componentNameConventionKey)
		if keyErr != nil {
			conventionErr = fmt.Errorf("failed to add convention key '%s' to string table: %w", componentNameConventionKey, keyErr)
		}

		// Get string index for the component's actual name (e.g., "TabBar").
		valIdx, valErr := state.addString(componentDef.Name)
		if valErr != nil && conventionErr == nil { // Combine errors if needed.
			conventionErr = fmt.Errorf("failed to add component name '%s' to string table: %w", componentDef.Name, valErr)
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
				// Final CustomPropCount set later.
			}
		}

		// Handle any error from adding the convention property.
		if conventionErr != nil {
			return fmt.Errorf("L%d: error adding component name convention property for '%s': %w", el.SourceLineNum, componentDef.Name, conventionErr)
		}
	} // --- END Step 3.5 ---

	// --- Step 4: Finalize Layout Byte ---
	// Determine the final layout byte based on precedence: Explicit 'layout:' > Style Layout > Default.
	finalLayout := uint8(0)
	layoutSource := "Default" // For debugging, track the source.
	styleLayoutByte := uint8(0)
	styleLayoutFound := false

	// Check if the resolved style defines layout flags.
	if el.StyleID > 0 {
		style := state.findStyleByID(el.StyleID) // findStyleByID defined elsewhere
		if style != nil && style.IsResolved {
			// Look for PropIDLayoutFlags within the style's resolved KRB properties.
			for _, prop := range style.Properties {
				if prop.PropertyID == PropIDLayoutFlags && prop.ValueType == ValTypeByte && prop.Size == 1 {
					styleLayoutByte = prop.Value[0]
					styleLayoutFound = true
					break
				}
			}
		} else if style != nil { // Style found but wasn't resolved (shouldn't happen ideally).
			log.Printf("CRITICAL WARN: Style %d ('%s') for Elem %d ('%s') was not marked as resolved when checking layout flags!", el.StyleID, style.SourceName, el.SelfIndex, el.SourceElementName)
		}
	}

	// Determine final layout based on precedence.
	if el.LayoutFlagsSource != 0 { // 1. Explicit 'layout:' property on element/component usage.
		finalLayout = el.LayoutFlagsSource
		layoutSource = "Explicit"
	} else if styleLayoutFound { // 2. Layout defined in the applied style.
		finalLayout = styleLayoutByte
		layoutSource = "Style"
	} else { // 3. Default layout if none specified.
		finalLayout = LayoutDirectionColumn | LayoutAlignmentStart // Default: Column, Align Start.
		layoutSource = "Default"
	}

	// Store the final calculated layout byte on the element struct.
	el.Layout = finalLayout

	// Optional logging for layout debugging.
	if layoutSource != "Default" || finalLayout != (LayoutDirectionColumn|LayoutAlignmentStart) {
		log.Printf("   >> Elem %d ('%s') Final Layout Byte: 0x%02X (Source: %s)\n", el.SelfIndex, el.SourceElementName, el.Layout, layoutSource)
	}

	// --- Step 5: Recursively Resolve Children ---
	// Prepare the runtime Children slice (pointers to other elements in the main state.Elements slice).
	el.Children = make([]*Element, 0, len(el.SourceChildrenIndices))
	el.ChildCount = 0 // Final count set later.

	// Iterate through the indices of children identified during the parsing phase.
	for _, childIndex := range el.SourceChildrenIndices {
		// Basic bounds check.
		if childIndex < 0 || childIndex >= len(state.Elements) {
			return fmt.Errorf("L%d: invalid child index %d found for parent element '%s'", el.SourceLineNum, childIndex, el.SourceElementName)
		}

		// Recursively call this function for the child element.
		err := state.resolveElementRecursive(childIndex)
		if err != nil {
			// Provide context in the error message if child resolution fails.
			childName := fmt.Sprintf("index %d", childIndex)
			if childIndex >= 0 && childIndex < len(state.Elements) {
				childName = fmt.Sprintf("'%s' (index %d)", state.Elements[childIndex].SourceElementName, childIndex)
			}
			return fmt.Errorf("error resolving child %s of element '%s': %w", childName, el.SourceElementName, err)
		}

		// If resolution succeeded, add a pointer to the resolved child element struct.
		if childIndex < len(state.Elements) { // Double check bounds after recursion (shouldn't change)
			el.Children = append(el.Children, &state.Elements[childIndex])
		} else {
			return fmt.Errorf("internal error: child index %d became invalid after recursive resolve for parent '%s'", childIndex, el.SourceElementName)
		}
	}
	// Final el.ChildCount is set later before writing based on len(el.Children).

	return nil // Success for resolving this element and its subtree.
}


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


