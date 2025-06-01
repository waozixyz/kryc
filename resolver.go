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

// componentNameConventionKey is the key used for a KRB Custom Property
// that stores the name of the KRY `Define`d component being instanced.
const componentNameConventionKey = "_componentName"

// getSourcePropertyValue retrieves the last value string for a given key from an element's source properties.
func (el *Element) getSourcePropertyValue(key string) (string, bool) {
	// Search backwards to respect potential overrides if properties were added multiple times
	// (though the current parser's addSourceProperty overwrites, so this is mainly for robustness).
	for i := len(el.SourceProperties) - 1; i >= 0; i-- {
		if el.SourceProperties[i].Key == key {
			return el.SourceProperties[i].ValueStr, true
		}
	}
	return "", false
}

// isDeclaredProp checks if a property name is declared in a component definition's Properties block.
func isDeclaredProp(propName string, declaredProps []ComponentPropertyDef) bool {
	for _, dp := range declaredProps {
		if dp.Name == propName {
			return true
		}
	}
	return false
}

// resolveComponentsAndProperties is Pass 1.5 of the compilation.
// It iterates through all parsed elements (both main UI tree instances and
// component template elements) and recursively resolves their properties.
func (state *CompilerState) resolveComponentsAndProperties() error {
	log.Println("Pass 1.5: Resolving properties for elements and component instances...")

	// Reset processed flag for all elements before starting this pass.
	for i := range state.Elements {
		state.Elements[i].ProcessedInPass15 = false
	}

	// Resolve each element. The recursion handles dependencies.
	// Both main tree instances and template definition elements need this pass.
	for i := range state.Elements {
		if !state.Elements[i].ProcessedInPass15 {
			if err := state.resolveElementRecursive(i); err != nil {
				elName := fmt.Sprintf("index %d", i)
				if i >= 0 && i < len(state.Elements) {
					elName = fmt.Sprintf("'%s' (index %d, L%d)", state.Elements[i].SourceElementName, i, state.Elements[i].SourceLineNum)
				}
				return fmt.Errorf("error resolving element tree starting at %s: %w", elName, err)
			}
		}
	}

	processedCount := 0
	// unprocessedIndices := []int{} // Only needed for debugging
	for k := range state.Elements {
		if state.Elements[k].ProcessedInPass15 {
			processedCount++
		}
		// else {
		// unprocessedIndices = append(unprocessedIndices, k)
		// }
	}

	// if len(unprocessedIndices) > 0 { // Only needed for debugging
	// 	log.Printf("Warning: %d/%d elements processed in Pass 1.5. Unprocessed indices: %v...", processedCount, len(state.Elements), unprocessedIndices)
	// }

	log.Printf("   Property and Component Resolution Pass Complete. Total elements processed: %d\n", processedCount)
	return nil
}

// resolveElementRecursive processes a single element, resolving its properties
// according to its type (standard element, component instance, or template element part).
func (state *CompilerState) resolveElementRecursive(elementIndex int) error {
	if elementIndex < 0 || elementIndex >= len(state.Elements) {
		return fmt.Errorf("internal: invalid element index %d for resolution", elementIndex)
	}

	el := &state.Elements[elementIndex]
	if el.ProcessedInPass15 {
		return nil // Already processed
	}
	el.ProcessedInPass15 = true

	// Reset resolved KRB data structures for this element
	el.KrbProperties = el.KrbProperties[:0]
	el.KrbCustomProperties = el.KrbCustomProperties[:0]
	el.KrbEvents = el.KrbEvents[:0]
	el.PropertyCount, el.CustomPropCount, el.EventCount = 0, 0, 0
	el.StyleID = 0 // Will be determined from source properties or component defaults

	processedSourcePropKeys := make(map[string]bool)

	// --- Step 1: Handle Component Instance Specifics (Placeholder Setup) ---
	if el.IsComponentInstance {
		if el.ComponentDef == nil {
			return fmt.Errorf("L%d: internal: component instance '%s' (idx %d) has nil ComponentDef", el.SourceLineNum, el.SourceElementName, el.SelfIndex)
		}
		componentDef := el.ComponentDef

		if componentDef.DefinitionRootElementIndex < 0 || componentDef.DefinitionRootElementIndex >= len(state.Elements) {
			return fmt.Errorf("L%d: internal: component def '%s' has invalid root element index %d for template", el.SourceLineNum, componentDef.Name, componentDef.DefinitionRootElementIndex)
		}
		// The placeholder element's Type should match the Type of the component definition's root element.
		defRootTemplateElement := &state.Elements[componentDef.DefinitionRootElementIndex]
		el.Type = defRootTemplateElement.Type // This makes the placeholder have the same base type.

		// Add the mandatory _componentName custom property
		compNameKeyIdx, keyErr := state.addString(componentNameConventionKey)
		if keyErr != nil {
			return fmt.Errorf("L%d: error adding string for _componentName key '%s': %w", el.SourceLineNum, componentNameConventionKey, keyErr)
		}
		compNameValIdx, valErr := state.addString(componentDef.Name)
		if valErr != nil {
			return fmt.Errorf("L%d: error adding string for _componentName value '%s': %w", el.SourceLineNum, componentDef.Name, valErr)
		}

		if len(el.KrbCustomProperties) < MaxCustomProperties {
			el.KrbCustomProperties = append(el.KrbCustomProperties, KrbCustomProperty{
				KeyIndex:  compNameKeyIdx,
				ValueType: ValTypeString, // String index for the component name
				Size:      1,             // Size of the string index (1 byte)
				Value:     []byte{compNameValIdx},
			})
		} else {
			return fmt.Errorf("L%d: max custom KRB properties (%d) reached for '%s', cannot add _componentName", el.SourceLineNum, MaxCustomProperties, el.SourceElementName)
		}
		// IMPORTANT: We DO NOT merge SourceProperties from the template into the instance here.
		// The instance's `el.SourceProperties` are *only* from its KRY usage tag.
		// Template defaults are handled by the runtime during instantiation, using the KRB Component Definition.
	}

	// --- Step 2: Process KRY Source Properties for Header Fields & Style ---
	// This applies to all element types (standard, instance placeholders, template elements)

	// Determine StyleID for this element
	// For component instances, `style` or `bar_style` (if applicable) from the KRY usage tag
	// sets the StyleID on the placeholder. The runtime will then apply this to the
	// instantiated component's root.
	// For template elements, this sets the default style of that template part.
	if directStyleStr, hasDirectStyle := el.getSourcePropertyValue("style"); hasDirectStyle {
		err := state.applyStyleStringToElement(el, directStyleStr, el.SourceLineNum)
		if err != nil {
			return fmt.Errorf("L%d: error applying style string '%s' to element '%s': %w", el.SourceLineNum, directStyleStr, el.SourceElementName, err)
		}
		processedSourcePropKeys["style"] = true
	}

	// Component-specific style property handling (e.g., bar_style for TabBar instances)
	if el.IsComponentInstance && el.ComponentDef != nil {
		componentDef := el.ComponentDef
		// Example for 'bar_style', adapt for other conventional style properties
		if barStyleIsDeclared(componentDef) { // Checks if 'bar_style' is in Define.Properties
			if barStyleStr, hasBarStyle := el.getSourcePropertyValue("bar_style"); hasBarStyle {
				cleanedBarStyleName, _ := cleanAndQuoteValue(barStyleStr)
				if cleanedBarStyleName != "" { // User provided a value for bar_style
					styleID := state.findStyleIDByName(cleanedBarStyleName)
					if styleID == 0 && cleanedBarStyleName != "" { // Only warn if a non-empty name was not found
						log.Printf("L%d: Warn: Style '%s' (for 'bar_style' property) on instance '%s' of component '%s' not found.\n", el.SourceLineNum, cleanedBarStyleName, el.SourceElementName, componentDef.Name)
					}
					el.StyleID = styleID // This overrides any `style:` on the instance placeholder
				} else if el.StyleID == 0 { // bar_style: "" was provided, and no `style:` was set, check for bar_style default from Define.Properties
					defaultStyleName := getDefaultBarStyleName(componentDef, "") // Get default from Define.Properties
					if defaultStyleName != "" {
						styleID := state.findStyleIDByName(defaultStyleName)
						if styleID == 0 && defaultStyleName != "" {
							log.Printf("L%d: Warn: Default style '%s' (from Define.Properties for 'bar_style') on instance '%s' of component '%s' not found.\n", el.SourceLineNum, defaultStyleName, el.SourceElementName, componentDef.Name)
						}
						el.StyleID = styleID
					}
				}
				processedSourcePropKeys["bar_style"] = true
			} else if el.StyleID == 0 { // No 'bar_style' in KRY usage, and no 'style:' in KRY usage. Check Define.Properties for 'bar_style' default.
				defaultStyleName := getDefaultBarStyleName(componentDef, "")
				if defaultStyleName != "" {
					styleID := state.findStyleIDByName(defaultStyleName)
					if styleID == 0 && defaultStyleName != "" {
						log.Printf("L%d: Warn: Default style '%s' (from Define.Properties for bar_style) on instance '%s' of component '%s' not found.\n", el.SourceLineNum, defaultStyleName, el.SourceElementName, componentDef.Name)
					}
					el.StyleID = styleID
				}
			}
		}
		// Apply component-type-specific fallback default style if NO style has been set yet by any means
		if el.StyleID == 0 {
			if componentDef.Name == "TabBar" { // Example hardcoded fallback default for TabBar
				// Determine orientation for TabBar (could be from a custom prop or a hint)
				// For simplicity, assuming el.OrientationHint is set if applicable.
				baseStyleName := "tab_bar_style_base_row" // Default for row orientation
				if el.OrientationHint == "column" || el.OrientationHint == "col" {
					baseStyleName = "tab_bar_style_base_column"
				}
				styleID := state.findStyleIDByName(baseStyleName)
				if styleID == 0 && baseStyleName != "" {
					log.Printf("L%d: Warn: TabBar fallback default style '%s' not found for instance '%s'.\n", el.SourceLineNum, baseStyleName, el.SourceElementName)
				}
				el.StyleID = styleID
			}
			// Add other component type specific default style logic here if needed
		}
	}

	// Process KRY source properties that map to KRB Element Header fields
	// `el.SourceProperties` here are from the KRY usage tag for instances,
	// or from the KRY definition for template elements.
	for _, sp := range el.SourceProperties {
		key, valStr, lineNum := sp.Key, sp.ValueStr, sp.LineNum
		if processedSourcePropKeys[key] { // Skip if already handled (e.g., style, bar_style)
			continue
		}

		cleanedString, _ := cleanAndQuoteValue(valStr)
		handledAsHeaderField := false
		var parseErr error

		switch key {
		case "id":
			handledAsHeaderField = true
			if cleanedString != "" {
				idx, err := state.addString(cleanedString)
				if err == nil {
					el.IDStringIndex = idx
					el.SourceIDName = cleanedString // Store the actual ID string for debugging/logging
				} else {
					parseErr = fmt.Errorf("adding id string '%s': %w", cleanedString, err)
				}
			}
		case "pos_x":
			handledAsHeaderField = true
			v, err := strconv.ParseUint(cleanedString, 10, 16)
			if err == nil {
				el.PosX = uint16(v)
			} else {
				parseErr = fmt.Errorf("pos_x '%s': %w", cleanedString, err)
			}
		case "pos_y":
			handledAsHeaderField = true
			v, err := strconv.ParseUint(cleanedString, 10, 16)
			if err == nil {
				el.PosY = uint16(v)
			} else {
				parseErr = fmt.Errorf("pos_y '%s': %w", cleanedString, err)
			}
		case "width": // KRY 'width' for direct element header field (pixel value)
			handledAsHeaderField = true
			v, err := strconv.ParseUint(cleanedString, 10, 16)
			if err == nil { // Successfully parsed as integer (pixels for header field)
				el.Width = uint16(v)
			} else { // Could not parse as uint; check if it's a percentage string
				if !strings.HasSuffix(cleanedString, "%") { // Not a percentage, so it's a parse error for the header field
					parseErr = fmt.Errorf("header width '%s' (expected pixel value): %w", cleanedString, err)
				} else {
					// It IS a percentage string. This means it's NOT for the direct header field `el.Width`.
					// It will be handled by `PropIDMaxWidth` with `ValTypePercentage` later in Step 3.
					handledAsHeaderField = false // Unmark, so it gets processed in Step 3
				}
			}
		case "height": // KRY 'height' for direct element header field
			handledAsHeaderField = true
			v, err := strconv.ParseUint(cleanedString, 10, 16)
			if err == nil { // Successfully parsed as integer (pixels for header field)
				el.Height = uint16(v)
			} else { // Could not parse as uint; check if it's a percentage string
				if !strings.HasSuffix(cleanedString, "%") { // Not a percentage, so it's a parse error for the header field
					parseErr = fmt.Errorf("header height '%s' (expected pixel value): %w", cleanedString, err)
				} else {
					// It IS a percentage string. Not for direct `el.Height`.
					// Will be handled by `PropIDMaxHeight` later.
					handledAsHeaderField = false // Unmark
				}
			}
		case "layout": // KRY `layout` property string is parsed into el.LayoutFlagsSource
			handledAsHeaderField = true
			el.LayoutFlagsSource = parseLayoutString(cleanedString) // parseLayoutString is from utils.go
		}

		if parseErr != nil {
			return fmt.Errorf("L%d: error parsing header field '%s: %s' for element '%s': %w", lineNum, key, valStr, el.SourceElementName, parseErr)
		}
		if handledAsHeaderField {
			processedSourcePropKeys[key] = true
		}
	}

	// --- Step 3: Resolve Remaining SourceProperties to KRB Standard Properties, KRB Events, or KRB Custom Properties ---
	var parsedPaddingTop, parsedPaddingRight, parsedPaddingBottom, parsedPaddingLeft *uint8
	var parsedPaddingShort string
	var foundPaddingShort, foundPaddingLong bool = false, false

	for _, sp := range el.SourceProperties {
		key, valStr, lineNum := sp.Key, sp.ValueStr, sp.LineNum
		if processedSourcePropKeys[key] { // Skip if already handled as a header field or specific style key
			continue
		}

		cleanedString, _ := cleanAndQuoteValue(valStr)
		propProcessedThisIteration := false
		var handleErr error

		// A. Events
		if isEventKey(key) { // isEventKey checks "onClick", "onSubmit", etc.
			propProcessedThisIteration = true
			// For component instances, events are attached to the placeholder. Runtime may re-target.
			// For template elements, events are generally NOT part of the static template definition.
			if el.IsDefinitionRoot && !el.IsComponentInstance { // If it's an element *within* a Define block's template
				log.Printf("L%d: Warn: Event handler '%s' defined on template element '%s'. Events are typically instance-specific and should be on the component usage or handled by runtime logic. Ignored for template element.", lineNum, key, el.SourceElementName)
			} else { // Standard element or Component Instance Placeholder
				if len(el.KrbEvents) < MaxEvents {
					if cleanedString != "" { // Callback name should not be empty
						cbIdx, addErr := state.addString(cleanedString)
						if addErr == nil {
							// Map KRY event key to KRB EventType
							var eventType uint8 = EventTypeClick // Default
							switch key {
							case "onClick", "on_click":
								eventType = EventTypeClick
								// Add other mappings: onSubmit -> EventTypeSubmit, etc.
								// case "onSubmit", "on_submit": eventType = EventTypeSubmit
							}
							el.KrbEvents = append(el.KrbEvents, KrbEvent{EventType: eventType, CallbackID: cbIdx})
						} else {
							handleErr = fmt.Errorf("adding event callback string '%s' for event '%s': %w", cleanedString, key, addErr)
						}
					} else {
						log.Printf("L%d: Warn: Empty callback string for event '%s' on element '%s'. Ignored.\n", lineNum, key, el.SourceElementName)
					}
				} else { // Max events reached
					handleErr = fmt.Errorf("maximum events (%d) reached for element '%s' when trying to add '%s'", MaxEvents, el.SourceElementName, key)
				}
			}
		}

		// B. Standard KRB Properties (for placeholder or standard element or template default)
		// & Padding/Margin temporary storage
		if !propProcessedThisIteration {
			switch key {
			case "padding":
				propProcessedThisIteration = true
				parsedPaddingShort = cleanedString
				foundPaddingShort = true
			case "padding_top":
				propProcessedThisIteration = true
				v, e := strconv.ParseUint(cleanedString, 10, 8)
				if e == nil {
					b := uint8(v)
					parsedPaddingTop = &b
					foundPaddingLong = true
				} else {
					handleErr = fmt.Errorf("parsing padding_top value '%s': %w", cleanedString, e)
				}
			case "padding_right":
				propProcessedThisIteration = true
				v, e := strconv.ParseUint(cleanedString, 10, 8)
				if e == nil {
					b := uint8(v)
					parsedPaddingRight = &b
					foundPaddingLong = true
				} else {
					handleErr = fmt.Errorf("parsing padding_right value '%s': %w", cleanedString, e)
				}
			case "padding_bottom":
				propProcessedThisIteration = true
				v, e := strconv.ParseUint(cleanedString, 10, 8)
				if e == nil {
					b := uint8(v)
					parsedPaddingBottom = &b
					foundPaddingLong = true
				} else {
					handleErr = fmt.Errorf("parsing padding_bottom value '%s': %w", cleanedString, e)
				}
			case "padding_left":
				propProcessedThisIteration = true
				v, e := strconv.ParseUint(cleanedString, 10, 8)
				if e == nil {
					b := uint8(v)
					parsedPaddingLeft = &b
					foundPaddingLong = true
				} else {
					handleErr = fmt.Errorf("parsing padding_left value '%s': %w", cleanedString, e)
				}
			case "margin":
				propProcessedThisIteration = true
				handleErr = handleSimpleMarginProperty(el, PropIDMargin, cleanedString)
			case "background_color":
				propProcessedThisIteration = true
				handleErr = addColorProp(el, PropIDBgColor, cleanedString, &state.HeaderFlags)
			case "text_color", "foreground_color":
				propProcessedThisIteration = true
				handleErr = addColorProp(el, PropIDFgColor, cleanedString, &state.HeaderFlags)
			case "border_color":
				propProcessedThisIteration = true
				handleErr = addColorProp(el, PropIDBorderColor, cleanedString, &state.HeaderFlags)
			case "border_width":
				propProcessedThisIteration = true
				handleErr = addByteProp(el, PropIDBorderWidth, cleanedString)
			case "border_radius":
				propProcessedThisIteration = true
				handleErr = addByteProp(el, PropIDBorderRadius, cleanedString)
			case "opacity":
				propProcessedThisIteration = true
				handleErr = addFixedPointProp(el, PropIDOpacity, cleanedString, &state.HeaderFlags)
			case "visibility", "visible":
				propProcessedThisIteration = true
				visVal := uint8(0) // Default to hidden/false
				lc := strings.ToLower(cleanedString)
				if lc == "true" || lc == "visible" || lc == "1" {
					visVal = 1
				} else if lc != "false" && lc != "hidden" && lc != "0" && lc != "" { // Allow empty to be hidden
					handleErr = fmt.Errorf("invalid boolean/visibility value '%s'", cleanedString)
				}
				if handleErr == nil {
					handleErr = el.addKrbProperty(PropIDVisibility, ValTypeByte, []byte{visVal})
				}
			case "z_index":
				propProcessedThisIteration = true
				zIndexVal, e := strconv.ParseInt(cleanedString, 10, 16) // int16
				if e == nil {
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, uint16(zIndexVal))
					handleErr = el.addKrbProperty(PropIDZindex, ValTypeShort, buf)
				} else {
					handleErr = fmt.Errorf("parsing z_index value '%s': %w", cleanedString, e)
				}
			case "transform":
				propProcessedThisIteration = true
				handleErr = state.addKrbStringProperty(el, PropIDTransform, cleanedString)
			case "shadow":
				propProcessedThisIteration = true
				handleErr = state.addKrbStringProperty(el, PropIDShadow, cleanedString)
			case "text", "content": // For Text, Button, etc.
				propProcessedThisIteration = true
				handleErr = state.addKrbStringProperty(el, PropIDTextContent, cleanedString)
			case "font_size":
				propProcessedThisIteration = true
				handleErr = addShortProp(el, PropIDFontSize, cleanedString)
			case "font_weight":
				propProcessedThisIteration = true
				weightVal := uint8(0)
				lc := strings.ToLower(cleanedString)
				if lc == "bold" || lc == "700" {
					weightVal = 1
				} else if lc != "normal" && lc != "400" && lc != "" {
					log.Printf("L%d: Warn: Invalid font_weight '%s' for '%s'. Using 'normal'.", lineNum, cleanedString, el.SourceElementName)
				}
				handleErr = el.addKrbProperty(PropIDFontWeight, ValTypeEnum, []byte{weightVal})
			case "text_alignment":
				propProcessedThisIteration = true
				alignVal := uint8(0)
				switch strings.ToLower(cleanedString) {
				case "center", "centre":
					alignVal = 1
				case "right", "end":
					alignVal = 2
				case "left", "start", "":
					alignVal = 0
				default:
					log.Printf("L%d: Warn: Invalid text_alignment '%s' for '%s'. Using 'start'.", lineNum, cleanedString, el.SourceElementName)
				}
				handleErr = el.addKrbProperty(PropIDTextAlignment, ValTypeEnum, []byte{alignVal})
			case "gap":
				propProcessedThisIteration = true
				handleErr = addShortProp(el, PropIDGap, cleanedString)
			case "min_width":
				propProcessedThisIteration = true
				handleErr = addSizeDimensionProp(state, el, PropIDMinWidth, cleanedString)
			case "min_height":
				propProcessedThisIteration = true
				handleErr = addSizeDimensionProp(state, el, PropIDMinHeight, cleanedString)
			case "width": // This KRY 'width' maps to PropIDMaxWidth if not handled as header field (i.e., if it's a percentage)
				propProcessedThisIteration = true
				handleErr = addSizeDimensionProp(state, el, PropIDMaxWidth, cleanedString)
			case "height": // This KRY 'height' maps to PropIDMaxHeight if not handled as header field
				propProcessedThisIteration = true
				handleErr = addSizeDimensionProp(state, el, PropIDMaxHeight, cleanedString)
			case "max_width": // Explicit KRY 'max_width'
				propProcessedThisIteration = true
				handleErr = addSizeDimensionProp(state, el, PropIDMaxWidth, cleanedString)
			case "max_height": // Explicit KRY 'max_height'
				propProcessedThisIteration = true
				handleErr = addSizeDimensionProp(state, el, PropIDMaxHeight, cleanedString)
			case "aspect_ratio":
				propProcessedThisIteration = true
				handleErr = addFixedPointProp(el, PropIDAspectRatio, cleanedString, &state.HeaderFlags)
			case "overflow":
				propProcessedThisIteration = true
				overflowVal := uint8(0)
				switch strings.ToLower(cleanedString) {
				case "hidden":
					overflowVal = 1
				case "scroll":
					overflowVal = 2
				case "visible", "":
					overflowVal = 0
				default:
					log.Printf("L%d: Warn: Invalid overflow '%s' for '%s'. Using 'visible'.", lineNum, cleanedString, el.SourceElementName)
				}
				handleErr = el.addKrbProperty(PropIDOverflow, ValTypeEnum, []byte{overflowVal})
			case "image_source", "source":
				if el.Type == ElemTypeImage || el.Type == ElemTypeButton { // Or if el is a component placeholder whose root can take an image
					propProcessedThisIteration = true
					handleErr = state.addKrbResourceProperty(el, PropIDImageSource, ResTypeImage, cleanedString)
				}
				// If not handled here, it might be a custom property for a component instance.
			// App-specific props (only apply if el.Type is ElemTypeApp)
			case "window_width":
				if el.Type == ElemTypeApp {
					propProcessedThisIteration = true
					handleErr = addShortProp(el, PropIDWindowWidth, cleanedString)
				}
			case "window_height":
				if el.Type == ElemTypeApp {
					propProcessedThisIteration = true
					handleErr = addShortProp(el, PropIDWindowHeight, cleanedString)
				}
			case "window_title":
				if el.Type == ElemTypeApp {
					propProcessedThisIteration = true
					handleErr = state.addKrbStringProperty(el, PropIDWindowTitle, cleanedString)
				}
			case "resizable":
				if el.Type == ElemTypeApp {
					propProcessedThisIteration = true
					b := uint8(0)
					if cleanedString == "true" || cleanedString == "1" {
						b = 1
					}
					handleErr = el.addKrbProperty(PropIDResizable, ValTypeByte, []byte{b})
				}
			case "icon":
				if el.Type == ElemTypeApp {
					propProcessedThisIteration = true
					handleErr = state.addKrbResourceProperty(el, PropIDIcon, ResTypeImage, cleanedString)
				}
			case "version":
				if el.Type == ElemTypeApp {
					propProcessedThisIteration = true
					handleErr = state.addKrbStringProperty(el, PropIDVersion, cleanedString)
				}
			case "author":
				if el.Type == ElemTypeApp {
					propProcessedThisIteration = true
					handleErr = state.addKrbStringProperty(el, PropIDAuthor, cleanedString)
				}
			case "keep_aspect":
				if el.Type == ElemTypeApp {
					propProcessedThisIteration = true
					b := uint8(0)
					if cleanedString == "true" || cleanedString == "1" {
						b = 1
					}
					handleErr = el.addKrbProperty(PropIDKeepAspect, ValTypeByte, []byte{b})
				}
			case "scale_factor":
				if el.Type == ElemTypeApp {
					propProcessedThisIteration = true
					handleErr = addFixedPointProp(el, PropIDScaleFactor, cleanedString, &state.HeaderFlags)
				}
			}
		}

		// C. Custom Properties for Component Instances
		// This is for properties from the KRY usage tag that are declared in `Define.Properties`
		// and are NOT standard KRY element properties handled above.
		if !propProcessedThisIteration && el.IsComponentInstance && el.ComponentDef != nil {
			if declaredPropDef := findDeclaredProperty(key, el.ComponentDef.Properties); declaredPropDef != nil {
				// Check if 'key' is a standard element property (like 'width', 'height', 'text')
				// If it IS a standard property, it should have been handled in section B and applied
				// to the placeholder's KrbProperties. It should NOT become a custom property.
				// We need a robust way to distinguish. For now, assume if it wasn't processed in B,
				// and it's declared in Define.Properties, it's a true custom prop for the component's logic.
				// This relies on section B comprehensively handling all KRY keys that map to *standard* KRB props.

				isStandardKeyAlreadyHandled := false // This check might be redundant if section B is exhaustive.
				// (Potentially add a check here if 'key' is a known standard KRY property name)

				if !isStandardKeyAlreadyHandled {
					propProcessedThisIteration = true
					keyIdx, keyErr := state.addString(key)
					if keyErr != nil {
						handleErr = fmt.Errorf("adding custom prop key '%s' to string table: %w", key, keyErr)
					} else {
						customValueBytes, customValueType, customValueSize, parseValErr :=
							parseKryValueToKrbBytes(state, cleanedString, declaredPropDef.ValueTypeHint, key, lineNum)
						if parseValErr == nil {
							if len(el.KrbCustomProperties) < MaxCustomProperties {
								el.KrbCustomProperties = append(el.KrbCustomProperties, KrbCustomProperty{
									KeyIndex:  keyIdx,
									ValueType: customValueType,
									Size:      customValueSize,
									Value:     customValueBytes,
								})
							} else {
								handleErr = fmt.Errorf("max custom KRB properties (%d) reached for instance '%s', cannot add '%s'", MaxCustomProperties, el.SourceElementName, key)
							}
						} else {
							handleErr = fmt.Errorf("parsing value for custom prop '%s: %s' on instance '%s': %w", key, cleanedString, el.SourceElementName, parseValErr)
						}
					}
				}
			}
		}

		// D. Log Unhandled Property
		if !propProcessedThisIteration {
			// This warning will trigger if a KRY property isn't an event,
			// isn't a standard KRB property mapping, and (if it's a component instance)
			// isn't a declared property in its Define.Properties block.
			logUnhandledPropWarning(state, el, key, lineNum)
		}

		if handleErr != nil {
			if isRecoverablePropError(key, handleErr) {
				log.Printf("L%d: Recoverable error processing property '%s: %s' for element '%s': %v. Continuing.", lineNum, key, valStr, el.SourceElementName, handleErr)
			} else {
				return fmt.Errorf("L%d: error processing property '%s: %s' for element '%s': %w", lineNum, key, valStr, el.SourceElementName, handleErr)
			}
		}
		processedSourcePropKeys[key] = true // Mark this source property key as considered/attempted.
	}

	// --- Step 3.1: Finalize Padding Property ---
	if foundPaddingShort || foundPaddingLong {
		var finalTop, finalRight, finalBottom, finalLeft uint8
		var paddingErr error
		if foundPaddingLong { // Long form (padding_top etc.) takes precedence if mixed
			if parsedPaddingTop != nil {
				finalTop = *parsedPaddingTop
			}
			if parsedPaddingRight != nil {
				finalRight = *parsedPaddingRight
			}
			if parsedPaddingBottom != nil {
				finalBottom = *parsedPaddingBottom
			}
			if parsedPaddingLeft != nil {
				finalLeft = *parsedPaddingLeft
			}
			if foundPaddingShort {
				log.Printf("L%d: Info: Padding shorthand for '%s' overridden by specific padding_* properties.", el.SourceLineNum, el.SourceElementName)
			}
		} else if foundPaddingShort { // Only shorthand 'padding: "v1 v2 v3 v4"' was used
			parts := strings.Fields(parsedPaddingShort)
			switch len(parts) {
			case 1:
				v, e := strconv.ParseUint(parts[0], 10, 8)
				if e == nil {
					b := uint8(v)
					finalTop, finalRight, finalBottom, finalLeft = b, b, b, b
				} else {
					paddingErr = fmt.Errorf("parsing single padding value '%s': %w", parts[0], e)
				}
			case 2:
				v1, e1 := strconv.ParseUint(parts[0], 10, 8)
				v2, e2 := strconv.ParseUint(parts[1], 10, 8)
				if e1 == nil && e2 == nil {
					vt, hz := uint8(v1), uint8(v2)
					finalTop, finalBottom = vt, vt
					finalRight, finalLeft = hz, hz
				} else {
					paddingErr = fmt.Errorf("parsing 2-value padding '%s %s': e1=%v, e2=%v", parts[0], parts[1], e1, e2)
				}
			case 4:
				v1, e1 := strconv.ParseUint(parts[0], 10, 8)
				v2, e2 := strconv.ParseUint(parts[1], 10, 8)
				v3, e3 := strconv.ParseUint(parts[2], 10, 8)
				v4, e4 := strconv.ParseUint(parts[3], 10, 8)
				if e1 == nil && e2 == nil && e3 == nil && e4 == nil {
					finalTop, finalRight, finalBottom, finalLeft = uint8(v1), uint8(v2), uint8(v3), uint8(v4)
				} else {
					paddingErr = fmt.Errorf("parsing 4-value padding: e1=%v..e4=%v", e1, e2, e3, e4)
				}
			default:
				paddingErr = fmt.Errorf("invalid number of values (%d) for 'padding' shorthand: '%s'. Expected 1, 2, or 4.", len(parts), parsedPaddingShort)
			}
		}
		if paddingErr != nil {
			return fmt.Errorf("L%d: error finalizing padding for element '%s': %w", el.SourceLineNum, el.SourceElementName, paddingErr)
		}
		if addErr := el.addKrbProperty(PropIDPadding, ValTypeEdgeInsets, []byte{finalTop, finalRight, finalBottom, finalLeft}); addErr != nil {
			return fmt.Errorf("L%d: error adding KRB padding property for element '%s': %w", el.SourceLineNum, el.SourceElementName, addErr)
		}
	}
	// (Similar finalization for margin if complex margin parsing is implemented)

	// --- Step 4: Finalize Layout Byte ---
	// `el.LayoutFlagsSource` was set from the KRY `layout:` string earlier.
	// Now, incorporate style's layout. Element's direct `layout:` wins over style's.
	finalLayoutByte := el.LayoutFlagsSource // Start with explicit layout from element if any
	// layoutSourceReason := "" // For debugging

	if el.LayoutFlagsSource != 0 {
		// layoutSourceReason = "Explicit on Element"
	}

	if el.StyleID > 0 { // If a style is applied
		if style := state.findStyleByID(el.StyleID); style != nil && style.IsResolved {
			styleLayoutByteValue := uint8(0)
			foundLayoutInStyle := false
			for _, p := range style.Properties { // Check for PropIDLayoutFlags in the style's *resolved KRB properties*
				if p.PropertyID == PropIDLayoutFlags && p.ValueType == ValTypeByte && p.Size == 1 {
					styleLayoutByteValue = p.Value[0]
					foundLayoutInStyle = true
					break
				}
			}

			if foundLayoutInStyle && el.LayoutFlagsSource == 0 { // If element had NO explicit layout, use style's
				finalLayoutByte = styleLayoutByteValue
				// layoutSourceReason = "From Style"
			}
			// If element HAS explicit layout (LayoutFlagsSource != 0), it already won.
		}
	}

	if finalLayoutByte == 0 { // If still no layout defined by element or style, apply default
		finalLayoutByte = LayoutDirectionColumn | LayoutAlignmentStart // Default: column, start
		// layoutSourceReason = "Default (Column|Start)"
	}
	el.Layout = finalLayoutByte // Set the final layout byte on the element

	// --- Step 5: Recursively Resolve Children ---
	// This logic applies to ALL elements (standard, placeholders, template parts).
	// For placeholders, el.SourceChildrenIndices are children from the KRY *usage tag*.
	// For template parts, el.SourceChildrenIndices are children *within* that template definition.
	el.Children = make([]*Element, 0, len(el.SourceChildrenIndices)) // Reset for this resolution pass
	for _, childIndexInState := range el.SourceChildrenIndices {
		if childIndexInState < 0 || childIndexInState >= len(state.Elements) {
			return fmt.Errorf("L%d: element '%s' (idx %d) has invalid child index %d in SourceChildrenIndices", el.SourceLineNum, el.SourceElementName, el.SelfIndex, childIndexInState)
		}

		childEl := &state.Elements[childIndexInState]

		// Propagate template context: If current element `el` is part of a component's definition template
		// (i.e., it's a "definition root" itself OR its parent was one), then its direct children
		// from the KRY source are also considered part of that same template structure.
		if el.IsDefinitionRoot { // This check implies el itself is part of *some* template
			childEl.IsDefinitionRoot = true
		}

		if err := state.resolveElementRecursive(childIndexInState); err != nil {
			childName := fmt.Sprintf("index %d", childIndexInState)
			if childIndexInState < len(state.Elements) {
				childName = fmt.Sprintf("'%s'(idx %d L%d)", state.Elements[childIndexInState].SourceElementName, childIndexInState, state.Elements[childIndexInState].SourceLineNum)
			}
			return fmt.Errorf("error resolving child %s of element '%s' (L%d): %w", childName, el.SourceElementName, el.SourceLineNum, err)
		}
		el.Children = append(el.Children, childEl)
	}

	// --- Finalize Counts for KRB Header ---
	el.PropertyCount = uint8(len(el.KrbProperties))
	el.CustomPropCount = uint8(len(el.KrbCustomProperties))
	el.EventCount = uint8(len(el.KrbEvents))
	// el.ChildCount is based on the el.Children slice populated above,
	// which reflects the correct context (instance children vs. template children).
	el.ChildCount = uint8(len(el.Children))
	el.AnimationCount = 0 // Not implemented

	return nil
}

// applyStyleStringToElement attempts to parse a style string (single or array)
// and set the element's StyleID.
// For KRB's single StyleID, if an array is given, it currently warns and uses the first valid one.
func (state *CompilerState) applyStyleStringToElement(el *Element, styleStr string, lineNum int) error {
	cleanedFullStyleString, _ := cleanAndQuoteValue(styleStr) // Clean the whole string "value" part

	if strings.HasPrefix(cleanedFullStyleString, "[") && strings.HasSuffix(cleanedFullStyleString, "]") {
		// It's an array string, e.g., ["style1", "style2"]
		content := cleanedFullStyleString[1 : len(cleanedFullStyleString)-1] // Remove brackets
		parts := strings.Split(content, ",")
		firstStyleNameFound := ""

		if len(parts) > 0 {
			for _, partStr := range parts {
				// Each part inside the array should be a quoted string like "style_name"
				individualName, wasQuoted := cleanAndQuoteValue(strings.TrimSpace(partStr))
				if wasQuoted && individualName != "" {
					firstStyleNameFound = individualName
					break // Found the first valid style name in the array
				}
			}
		}

		if firstStyleNameFound != "" {
			if len(parts) > 1 { // Only log warning if there actually were multiple styles listed
				log.Printf("L%d: Warn: Element '%s' uses array style '%s'. KRB supports only one StyleID per element. Applying first valid style '%s'. True multi-style application needs runtime support.", lineNum, el.SourceElementName, styleStr, firstStyleNameFound)
			}
			styleID := state.findStyleIDByName(firstStyleNameFound)
			if styleID == 0 { // First style name from array not found
				log.Printf("L%d: Warn: Style '%s' (from array style for element '%s') not found.", lineNum, firstStyleNameFound, el.SourceElementName)
			}
			el.StyleID = styleID
		} else { // Array was empty "[]" or contained no valid quoted style names
			log.Printf("L%d: Warn: Element '%s' has empty or invalid array style definition: '%s'. No style applied from this definition.", lineNum, el.SourceElementName, styleStr)
			el.StyleID = 0 // Explicitly no style if array is invalid/empty
		}
		return nil
	}

	// Single style name (not an array format)
	if cleanedFullStyleString != "" {
		styleID := state.findStyleIDByName(cleanedFullStyleString)
		if styleID == 0 {
			// This is the warning for `style: "non_existent_style"`
			// And also for the previous error `style: ["s1","s2"]` if it wasn't caught by the array check above
			log.Printf("L%d: Warn: Style '%s' not found for element '%s'.\n", lineNum, cleanedFullStyleString, el.SourceElementName)
		}
		el.StyleID = styleID
	} else {
		el.StyleID = 0 // Empty style string e.g. style: "" means no style
	}
	return nil
}

// isEventKey checks if a KRY property key is an event handler.
func isEventKey(key string) bool {
	// Add more event keys as your KRY spec defines them
	switch key {
	case "onClick", "on_click",
		"onSubmit", "on_submit",
		"onChange", "on_change":
		// Add more cases here: "onHover", "onFocus", etc.
		return true
	default:
		return false
	}
}

// --- Helper Functions ---

func barStyleIsDeclared(def *ComponentDefinition) bool {
	for _, p := range def.Properties {
		if p.Name == "bar_style" {
			return true
		}
	}
	return false
}

func getDefaultBarStyleName(def *ComponentDefinition, fallback string) string {
	for _, p := range def.Properties {
		if p.Name == "bar_style" && p.DefaultValueStr != "" {
			name, _ := cleanAndQuoteValue(p.DefaultValueStr)
			return name
		}
	}
	return fallback
}

func findDeclaredProperty(name string, props []ComponentPropertyDef) *ComponentPropertyDef {
	for i := range props {
		if props[i].Name == name {
			return &props[i]
		}
	}
	return nil
}

func isRecoverablePropError(key string, err error) bool {
	// Example: Allow continuation if padding/margin parsing fails but other props might be okay
	if strings.HasPrefix(key, "padding") || strings.HasPrefix(key, "margin") {
		if err != nil && strings.Contains(err.Error(), "invalid") { // Basic check
			return true
		}
	}
	return false
}

func parseKryValueToKrbBytes(state *CompilerState, valStr string, hint uint8, propKey string, lineNum int) (data []byte, valType uint8, size uint8, err error) {
	// valStr is assumed to be already cleaned by the caller (via cleanAndQuoteValue)
	switch hint {
	case ValTypeString, ValTypeStyleID, ValTypeResource, ValTypeEnum: // These all become string indices in KRB Custom Props
		idx, e := state.addString(valStr)
		if e != nil {
			return nil, 0, 0, fmt.Errorf("adding string for custom prop '%s' ('%s'): %w", propKey, valStr, e)
		}
		if hint == ValTypeResource && valStr != "" {
			// guessResourceType needs to be robust or replaced with more explicit KRY syntax for resource types
			if _, resErr := state.addResource(guessResourceType(propKey), valStr); resErr != nil {
				log.Printf("L%d: Warn: Failed to add resource '%s' (hinted for custom prop '%s'): %v. Storing as string index only.", lineNum, valStr, propKey, resErr)
			}
		}
		return []byte{idx}, ValTypeString, 1, nil // KRB ValueType for custom prop is String Index

	case ValTypeColor:
		colBytes, ok := parseColor(valStr) // parseColor expects "#RRGGBBAA" etc.
		if !ok {
			return nil, 0, 0, fmt.Errorf("invalid Color string '%s' for custom prop '%s'", valStr, propKey)
		}
		state.HeaderFlags |= FlagExtendedColor // Assume custom colors passed as strings are RGBA
		return colBytes[:], ValTypeColor, 4, nil

	case ValTypeInt, ValTypeShort: // KRY "Int" or "Short" hint
		v, e := strconv.ParseInt(valStr, 10, 16) // Try to parse as int16
		if e != nil {
			return nil, 0, 0, fmt.Errorf("invalid Int/Short string '%s' for custom prop '%s': %w", valStr, propKey, e)
		}
		// No explicit range check here for custom props, assumed to fit short
		buf := make([]byte, 2)
		binary.LittleEndian.PutUint16(buf, uint16(v)) // Store as KRB Short
		return buf, ValTypeShort, 2, nil

	case ValTypeBool, ValTypeByte: // KRY "Bool" or "Byte" hint
		lc := strings.ToLower(valStr)
		byteVal := uint8(0)
		parsed := false
		if lc == "true" || lc == "1" {
			byteVal = 1
			parsed = true
		} else if lc == "false" || lc == "0" || lc == "" { // Empty string often defaults to false/0
			byteVal = 0
			parsed = true
		} else if hint == ValTypeByte { // If it's explicitly a Byte hint, try parsing as uint8
			if bv, bErr := strconv.ParseUint(valStr, 10, 8); bErr == nil {
				byteVal = uint8(bv)
				parsed = true
			}
		}
		if !parsed { // If still not parsed (e.g. invalid bool/byte string)
			log.Printf("L%d: Warn: Invalid Bool/Byte string '%s' for custom prop '%s'. Using default 0/false.", lineNum, valStr, propKey)
			byteVal = 0 // Default to 0/false on parse error for custom prop
		}
		return []byte{byteVal}, ValTypeByte, 1, nil

	case ValTypeFloat, ValTypePercentage: // KRY "Float" hint, or KRY value like "50%"
		f, pErr := strconv.ParseFloat(valStr, 64)
		if pErr != nil { // If direct float parse failed, check for percentage string
			if strings.HasSuffix(valStr, "%") {
				percentOnlyStr := strings.TrimSuffix(valStr, "%")
				if f2, pErr2 := strconv.ParseFloat(percentOnlyStr, 64); pErr2 == nil {
					f = f2 / 100.0 // Convert "50" from "50%" to 0.5
					pErr = nil     // Clear previous parsing error
				} else {
					return nil, 0, 0, fmt.Errorf("invalid Percentage string value '%s' for custom prop '%s': %w", valStr, propKey, pErr2)
				}
			} else { // Not a float, not a percentage string
				return nil, 0, 0, fmt.Errorf("invalid Float string value '%s' for custom prop '%s': %w", valStr, propKey, pErr)
			}
		}
		// Convert float (e.g., 0.0 to 1.0 for opacity, or 0.5 for 50%) to 8.8 fixed point
		fpVal := uint16(math.Round(f * 256.0))
		buf := make([]byte, 2)
		binary.LittleEndian.PutUint16(buf, fpVal)
		state.HeaderFlags |= FlagFixedPoint   // Mark that fixed point values are used in the file
		return buf, ValTypePercentage, 2, nil // KRB ValueType is Percentage (which means 8.8 fixed point)

	default: // ValTypeCustom hint from KRY or other unhandled KRY type hints for custom props
		log.Printf("L%d: Info: Custom prop '%s' (KRY hint %d) storing raw value '%s' as KRB String Index.", lineNum, propKey, hint, valStr)
		idx, e := state.addString(valStr)
		if e != nil {
			return nil, 0, 0, fmt.Errorf("adding string for custom prop '%s' (unknown KRY hint %d, value '%s'): %w", propKey, hint, valStr, e)
		}
		return []byte{idx}, ValTypeString, 1, nil // Default to storing as a string index
	}
}

func addSizeDimensionProp(state *CompilerState, el *Element, propID uint8, valStr string) error {
	// valStr is assumed to be cleaned already
	if strings.HasSuffix(valStr, "%") {
		percentStr := strings.TrimSuffix(valStr, "%")
		percentF, err := strconv.ParseFloat(percentStr, 64)
		if err != nil {
			return fmt.Errorf("prop ID 0x%X: invalid percentage value in '%s': %w", propID, valStr, err)
		}
		if percentF < 0 { // Percentages should not be negative for dimensions
			percentF = 0 // Clamp to 0
			log.Printf("L%d: Warn: Negative percentage '%s' for prop ID 0x%X treated as 0%%.", el.SourceLineNum, valStr, propID)
		}
		// Convert 0-100 (or more for >100%) percent range to 0.0-1.0 float, then to 8.8 fixed point
		// Example: "50%" -> 50.0 -> 0.5 -> 0.5 * 256 = 128
		fixedPointVal := uint16(math.Round((percentF / 100.0) * 256.0))
		buf := make([]byte, 2)
		binary.LittleEndian.PutUint16(buf, fixedPointVal)
		state.HeaderFlags |= FlagFixedPoint // Ensure fixed point flag is set
		return el.addKrbProperty(propID, ValTypePercentage, buf)
	} else { // Pixel value
		pixels, err := strconv.ParseUint(valStr, 10, 16) // uint16 for pixel dimensions
		if err != nil {
			return fmt.Errorf("prop ID 0x%X: invalid pixel value '%s': %w", propID, valStr, err)
		}
		// KRB spec uses uint16 for these pixel values
		buf := make([]byte, 2)
		binary.LittleEndian.PutUint16(buf, uint16(pixels))
		return el.addKrbProperty(propID, ValTypeShort, buf)
	}
}

func addColorProp(el *Element, propID uint8, cleanValStr string, headerFlags *uint16) error {
	col, ok := parseColor(cleanValStr) // parseColor expects "#RRGGBBAA" format
	if !ok {
		return fmt.Errorf("prop ID 0x%X: invalid color format string '%s'", propID, cleanValStr)
	}
	err := el.addKrbProperty(propID, ValTypeColor, col[:])
	if err == nil {
		// KRB spec: VAL_TYPE_COLOR size/format depends on FLAG_EXTENDED_COLOR.
		// If we are parsing #RRGGBBAA (or similar that implies RGBA), we set extended color.
		*headerFlags |= FlagExtendedColor
	}
	return err
}

func addByteProp(el *Element, propID uint8, cleanValStr string) error {
	v, err := strconv.ParseUint(cleanValStr, 10, 8)
	if err != nil {
		return fmt.Errorf("prop ID 0x%X: invalid uint8 value '%s': %w", propID, cleanValStr, err)
	}
	return el.addKrbProperty(propID, ValTypeByte, []byte{uint8(v)})
}

func addShortProp(el *Element, propID uint8, cleanValStr string) error {
	v, err := strconv.ParseUint(cleanValStr, 10, 16) // uint16
	if err != nil {
		return fmt.Errorf("prop ID 0x%X: invalid uint16 value '%s': %w", propID, cleanValStr, err)
	}
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(v))
	return el.addKrbProperty(propID, ValTypeShort, buf)
}

// addFixedPointProp handles KRY float "0.0-1.0" (e.g. opacity) or percentage strings "N%"
// -> KRB 8.8 Percentage (uint16)
func addFixedPointProp(el *Element, propID uint8, cleanValStr string, headerFlags *uint16) error {
	f, err := strconv.ParseFloat(cleanValStr, 64)
	if err != nil { // If direct float parse failed, check for percentage string
		if strings.HasSuffix(cleanValStr, "%") {
			trimmedPercent := strings.TrimSuffix(cleanValStr, "%")
			if f2, pErr2 := strconv.ParseFloat(trimmedPercent, 64); pErr2 == nil {
				f = f2 / 100.0 // Convert "50" from "50%" to 0.5
				err = nil      // Clear the original parsing error
			} else {
				return fmt.Errorf("prop ID 0x%X: invalid percentage string in fixed point prop '%s': %w", propID, cleanValStr, pErr2)
			}
		} else { // Not a float, and not a percentage string
			return fmt.Errorf("prop ID 0x%X: invalid float string for fixed point prop '%s': %w", propID, cleanValStr, err)
		}
	}

	// For opacity, clamp to 0.0-1.0 range. For other fixed-point props, this might not be necessary.
	if propID == PropIDOpacity {
		if f < 0.0 {
			f = 0.0
		}
		if f > 1.0 {
			f = 1.0
		}
	}
	// For aspect ratio, ensure non-negative
	if propID == PropIDAspectRatio && f < 0.0 {
		f = 0.0
		log.Printf("L%d: Warn: Negative aspect_ratio '%s' treated as 0.0", el.SourceLineNum, cleanValStr)
	}

	fpVal := uint16(math.Round(f * 256.0)) // Convert to 8.8 fixed point
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, fpVal)
	propErr := el.addKrbProperty(propID, ValTypePercentage, buf) // KRB ValTypePercentage is for 8.8
	if propErr == nil {
		*headerFlags |= FlagFixedPoint // Ensure global flag is set if fixed point values are used
	}
	return propErr
}

func handleSimpleMarginProperty(el *Element, propID uint8, cleanVal string) error {
	v, err := strconv.ParseUint(cleanVal, 10, 8) // Assuming uint8 for simple margin values
	if err != nil {
		return fmt.Errorf("prop ID 0x%X: invalid uint8 for simple margin '%s': %w", propID, cleanVal, err)
	}
	valByte := uint8(v)
	buf := []byte{valByte, valByte, valByte, valByte} // top, right, bottom, left
	return el.addKrbProperty(propID, ValTypeEdgeInsets, buf)
}

func logUnhandledPropWarning(state *CompilerState, el *Element, key string, lineNum int) {
	// This map helps avoid warnings for properties that are handled elsewhere (e.g., header fields, style directives).
	knownHandledKryKeys := map[string]bool{
		"id": true, "style": true, "layout": true, "pos_x": true, "pos_y": true, "width": true, "height": true, // Header/structural
		// Standard KRY properties that map to KRB standard properties:
		"background_color": true, "text_color": true, "foreground_color": true, "border_color": true, "border_width": true,
		"border_radius": true, "opacity": true, "visibility": true, "visible": true, "z_index": true, "transform": true,
		"shadow": true, "text": true, "content": true, "font_size": true, "font_weight": true, "text_alignment": true,
		"gap": true, "min_width": true, "min_height": true, "max_width": true, "max_height": true, "aspect_ratio": true,
		"overflow": true, "image_source": true, "source": true, "padding": true, "padding_top": true, "padding_right": true,
		"padding_bottom": true, "padding_left": true, "margin": true, // Add margin_* if handled separately
		// Event handlers
		"onClick": true, "on_click": true, // Add others like onChange
		// App specific properties
		"window_width": true, "window_height": true, "window_title": true, "resizable": true, "icon": true,
		"version": true, "author": true, "keep_aspect": true, "scale_factor": true,
		// Component-specific control properties that might be handled before custom prop stage
		// or are known to become custom properties if declared.
		"bar_style": true, "orientation": true, "position": true,
	}
	if knownHandledKryKeys[key] {
		return // Property is known to be handled or is a direct field.
	}

	if el.IsComponentInstance {
		// Check if it was processed as a custom property for a component instance
		if keyIdx, found := state.getStringIndex(key); found { // getStringIndex expects a cleaned key
			for _, cp := range el.KrbCustomProperties {
				if cp.KeyIndex == keyIdx {
					return // It was successfully processed as a KrbCustomProperty
				}
			}
		}
		// If it's an instance and not a custom prop, check if it was declared
		if el.ComponentDef != nil {
			if findDeclaredProperty(key, el.ComponentDef.Properties) != nil {
				// This means it was declared in Define.Properties but the resolver logic
				// for custom props didn't convert it. This could be an issue.
				log.Printf("L%d: Info: Declared KRY property '%s' for component '%s' was not mapped to a KRB property. Review resolver logic. Ignored for KRB output.", lineNum, key, el.ComponentDef.Name)
			} else {
				// This is an undeclared property on a component instance.
				log.Printf("L%d: Warn: Undeclared KRY property '%s' found on component instance of '%s'. Ignored.", lineNum, key, el.ComponentDef.Name)
			}
			return // Stop further warnings for this key on this instance
		}
	}

	// If it's not an instance, or it's an instance but not component-related,
	// and not a standard KRY/KRB prop, then it's truly unhandled.
	log.Printf("L%d: Warn: Unhandled KRY property '%s: ...' for standard element '%s' (type 0x%X). Ignored for KRB output.", lineNum, key, el.SourceElementName, el.Type)
}

// findStyleByID finds a style entry by its 1-based KRB ID.
func (state *CompilerState) findStyleByID(styleID uint8) *StyleEntry {
	if styleID == 0 { // StyleID 0 means no style
		return nil
	}
	if int(styleID)-1 < 0 || int(styleID)-1 >= len(state.Styles) { // Bounds check
		return nil
	}
	// Assumes state.Styles is 0-indexed and style.ID is 1-based and contiguous after parsing.
	style := &state.Styles[styleID-1]
	if style.ID == styleID { // Quick check for direct mapping
		return style
	}
	// Fallback: linear search if IDs are not perfectly contiguous (should not be needed if IDs are managed well)
	for i := range state.Styles {
		if state.Styles[i].ID == styleID {
			return &state.Styles[i]
		}
	}
	return nil
}

func (state *CompilerState) findStyleIDByName(name string) uint8 {
	if style := state.findStyleByName(name); style != nil {
		return style.ID
	}
	return 0 // 0 is an invalid StyleID, indicating "not found" or "no style"
}

func (state *CompilerState) findStyleByName(name string) *StyleEntry {
	cleanedName, _ := cleanAndQuoteValue(name)
	if cleanedName == "" {
		return nil
	}
	for i := range state.Styles {
		if state.Styles[i].SourceName == cleanedName {
			return &state.Styles[i]
		}
	}
	return nil
}

// getStringIndex finds the 0-based index of a string in the compiler's string table.
// Assumes cleanedText has already been processed by cleanAndQuoteValue if it came from source.
func (state *CompilerState) getStringIndex(cleanedText string) (uint8, bool) {
	if cleanedText == "" {
		// Ensure index 0 "" string exists. addString handles this.
		if len(state.Strings) == 0 || state.Strings[0].Text != "" {
			// This situation should ideally be prevented by addString always ensuring "" is at index 0.
			// For robustness, if "" isn't at index 0, try adding it.
			if _, err := state.addString(""); err != nil {
				log.Printf("Critical: Failed to ensure empty string at index 0 of string table: %v", err)
				return 0, false // Cannot proceed reliably
			}
		}
		return 0, true // Index 0 is conventionally for the empty string
	}
	// Search for non-empty strings starting from index 1 (index 0 is reserved for "")
	for i := 1; i < len(state.Strings); i++ {
		if state.Strings[i].Text == cleanedText {
			return state.Strings[i].Index, true
		}
	}
	return 0, false // String not found (or 0 if it was an empty string search and found at 0)
}
