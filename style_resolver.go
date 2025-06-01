// style_resolver.go
package main

import (
	"encoding/binary" // For number conversions (e.g., font_size)
	"fmt"
	"log"
	"math"    // For MaxUint16 etc.
	"sort"    // For sorting final properties by ID
	"strconv" // For parsing numbers from strings
	"strings" // For string manipulation (ToLower, Fields etc.)
)

// --- resolveStyleInheritance ---
// Pass 1.2: Resolves style inheritance (`extends`) and calculates the final
// set of KRB properties for each style, detecting cycles.
func (state *CompilerState) resolveStyleInheritance() error {
	log.Println("Pass 1.2: Resolving style inheritance...")

	// Reset resolution state for all styles.
	for i := range state.Styles {
		state.Styles[i].IsResolved = false
		state.Styles[i].IsResolving = false
		// Clear previously resolved KRB properties.
		state.Styles[i].Properties = make([]KrbProperty, 0, len(state.Styles[i].SourceProperties))
	}

	resolvedCount := 0
	totalStyles := len(state.Styles)
	maxIterations := totalStyles*2 + 1 // Generous iteration limit

	// Iteratively resolve styles to handle dependencies.
	for i := 0; i < maxIterations && resolvedCount < totalStyles; i++ {
		madeProgress := false

		for j := range state.Styles {
			if !state.Styles[j].IsResolved {
				err := state.resolveSingleStyle(&state.Styles[j])
				if err == nil {
					madeProgress = true
					resolvedCount++
				} else if strings.Contains(err.Error(), "cyclic style inheritance detected") {
					return err // Fail fast on cycles
				}
				// Ignore other errors (like missing base) for now, might resolve later.
			}
		}
		if !madeProgress {
			break // Break if no progress made in an iteration (likely unresolvable)
		}
	}

	// Final check for unresolved styles.
	if resolvedCount != totalStyles {
		log.Println("Warning: Style resolution finished but not all styles marked resolved.")
		unresolvedCount := 0
		for i := range state.Styles {
			if !state.Styles[i].IsResolved {
				// Attempt one last resolve to get the specific error.
				err := state.resolveSingleStyle(&state.Styles[i])
				log.Printf("       - Unresolved style: '%s' (Error: %v)", state.Styles[i].SourceName, err)
				unresolvedCount++
			}
		}
		return fmt.Errorf("%d styles remain unresolved", unresolvedCount)
	}

	log.Printf("   Style inheritance resolution complete. %d styles processed.\n", totalStyles)
	return nil
}

// --- resolveSingleStyle ---
// Recursively resolves inheritance and properties for one style entry.

func (state *CompilerState) resolveSingleStyle(style *StyleEntry) error {
	// Base cases for recursion
	if style.IsResolved {
		return nil
	}
	if style.IsResolving {
		return fmt.Errorf("cyclic style inheritance detected involving style '%s'", style.SourceName)
	}

	style.IsResolving = true
	defer func() {
		style.IsResolving = false
	}() // Ensure flag is reset

	// Map to merge properties (KRB Prop ID -> KrbProperty)
	mergedProps := make(map[uint8]KrbProperty)

	// --- Step 1: Resolve and Apply Base Style Properties ---
	// If style.ExtendsStyleNames has multiple entries, they are processed in order.
	// Properties from later base styles in the list will overwrite those from earlier ones
	// due to the nature of map assignment.
	if len(style.ExtendsStyleNames) > 0 {
		for _, baseName := range style.ExtendsStyleNames { // Iterate in order
			baseStyle := state.findStyleByName(baseName)
			if baseStyle == nil {
				extendsLine := 0 // Find line num for error context
				// Look for the 'extends' source property to get its line number
				for _, sp := range style.SourceProperties {
					if sp.Key == "extends" {
						extendsLine = sp.LineNum
						break
					}
				}
				// Fallback if 'extends' prop not found yet (e.g. during initial processing error)
				if extendsLine == 0 && len(style.SourceProperties) > 0 {
					extendsLine = style.SourceProperties[0].LineNum
				} else if extendsLine == 0 {
					extendsLine = 0 // Or some other indicator for "unknown line"
				}

				return fmt.Errorf("L%d: base style '%s' (part of 'extends' for '%s') not found", extendsLine, baseName, style.SourceName)
			}

			// Recursively resolve the base style first.
			err := state.resolveSingleStyle(baseStyle)
			if err != nil {
				return fmt.Errorf("error resolving base style '%s' (needed by '%s'): %w", baseStyle.SourceName, style.SourceName, err)
			}

			// Merge resolved properties from this base style.
			// Later bases' properties will overwrite earlier ones in mergedProps.
			for _, baseProp := range baseStyle.Properties {
				mergedProps[baseProp.PropertyID] = baseProp
			}
		}
	}

	// --- Step 2: Apply/Override with Own Source Properties ---
	// Properties defined directly in 'style' will override anything from any base style.
	overrideCount := 0 // For logging/debugging, not essential for logic
	addedCount := 0    // For logging/debugging

	for _, sp := range style.SourceProperties {
		key := sp.Key
		valStr := sp.ValueStr
		lineNum := sp.LineNum

		if key == "extends" {
			continue // Already handled
		}

		cleanedString, _ := cleanAndQuoteValue(valStr)

		var krbProp *KrbProperty
		var propErr error
		propID := PropIDInvalid // Default to invalid, set on successful conversion
		propAdded := false      // Flag to track if krbProp was successfully created

		// --- Convert KRY key/value to KRB Property (same logic as before but formatted) ---
		switch key {
		case "background_color":
			if col, ok := parseColor(cleanedString); ok {
				krbProp = &KrbProperty{PropertyID: PropIDBgColor, ValueType: ValTypeColor, Size: 4, Value: col[:]}
				state.HeaderFlags |= FlagExtendedColor
				propID = PropIDBgColor
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid color format '%s'", cleanedString)
			}

		case "text_color", "foreground_color":
			if col, ok := parseColor(cleanedString); ok {
				krbProp = &KrbProperty{PropertyID: PropIDFgColor, ValueType: ValTypeColor, Size: 4, Value: col[:]}
				state.HeaderFlags |= FlagExtendedColor
				propID = PropIDFgColor
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid color format '%s'", cleanedString)
			}

		case "border_color":
			if col, ok := parseColor(cleanedString); ok {
				krbProp = &KrbProperty{PropertyID: PropIDBorderColor, ValueType: ValTypeColor, Size: 4, Value: col[:]}
				state.HeaderFlags |= FlagExtendedColor
				propID = PropIDBorderColor
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid color format '%s'", cleanedString)
			}

		case "border_width":
			if bw, e := strconv.ParseUint(cleanedString, 10, 8); e == nil {
				krbProp = &KrbProperty{PropertyID: PropIDBorderWidth, ValueType: ValTypeByte, Size: 1, Value: []byte{uint8(bw)}}
				propID = PropIDBorderWidth
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid uint8 for border_width '%s': %w", cleanedString, e)
			}

		case "border_radius":
			if br, e := strconv.ParseUint(cleanedString, 10, 8); e == nil {
				krbProp = &KrbProperty{PropertyID: PropIDBorderRadius, ValueType: ValTypeByte, Size: 1, Value: []byte{uint8(br)}}
				propID = PropIDBorderRadius
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid uint8 for border_radius '%s': %w", cleanedString, e)
			}

		case "padding":
			propID = PropIDPadding
			propAdded = true // Assume success unless parsing fails
			var finalTop, finalRight, finalBottom, finalLeft uint8 = 0, 0, 0, 0
			parts := strings.Fields(cleanedString)

			switch len(parts) {
			case 1: // Single value: applies to all sides.
				v, e := strconv.ParseUint(parts[0], 10, 8)
				if e != nil {
					propErr = fmt.Errorf("invalid uint8 value '%s' for single padding: %w", parts[0], e)
					propAdded = false // Mark as failed
				} else {
					valByte := uint8(v)
					finalTop, finalRight, finalBottom, finalLeft = valByte, valByte, valByte, valByte
				}
			case 2: // Two values: [vertical] [horizontal].
				v1, e1 := strconv.ParseUint(parts[0], 10, 8) // Vertical (Top/Bottom)
				v2, e2 := strconv.ParseUint(parts[1], 10, 8) // Horizontal (Right/Left)
				if e1 != nil || e2 != nil {
					propErr = fmt.Errorf("invalid uint8 values in '%s %s' for padding: %v / %v", parts[0], parts[1], e1, e2)
					propAdded = false // Mark as failed
				} else {
					vert, horiz := uint8(v1), uint8(v2)
					finalTop, finalBottom = vert, vert
					finalRight, finalLeft = horiz, horiz
				}
			case 4: // Four values: [top] [right] [bottom] [left].
				v1, e1 := strconv.ParseUint(parts[0], 10, 8) // Top
				v2, e2 := strconv.ParseUint(parts[1], 10, 8) // Right
				v3, e3 := strconv.ParseUint(parts[2], 10, 8) // Bottom
				v4, e4 := strconv.ParseUint(parts[3], 10, 8) // Left
				if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
					propErr = fmt.Errorf("invalid uint8 values in '%s %s %s %s' for padding: %v/%v/%v/%v", parts[0], parts[1], parts[2], parts[3], e1, e2, e3, e4)
					propAdded = false // Mark as failed
				} else {
					finalTop, finalRight, finalBottom, finalLeft = uint8(v1), uint8(v2), uint8(v3), uint8(v4)
				}
			default: // Invalid number of values for shorthand.
				propErr = fmt.Errorf("invalid number of values (%d) for padding shorthand '%s', expected 1, 2, or 4", len(parts), cleanedString)
				propAdded = false // Mark as failed
			}

			if propAdded { // Only create the KRB property if parsing was successful
				paddingBuf := []byte{finalTop, finalRight, finalBottom, finalLeft}
				krbProp = &KrbProperty{PropertyID: PropIDPadding, ValueType: ValTypeEdgeInsets, Size: 4, Value: paddingBuf}
			}

		case "margin":
			propID = PropIDMargin
			propAdded = true // Assume success unless parsing fails
			var finalTop, finalRight, finalBottom, finalLeft uint8 = 0, 0, 0, 0
			parts := strings.Fields(cleanedString)
			switch len(parts) {
			case 1:
				v, e := strconv.ParseUint(parts[0], 10, 8)
				if e != nil {
					propErr = fmt.Errorf("invalid uint8 value '%s' for single margin: %w", parts[0], e)
					propAdded = false
				} else {
					valByte := uint8(v)
					finalTop, finalRight, finalBottom, finalLeft = valByte, valByte, valByte, valByte
				}
			case 2:
				v1, e1 := strconv.ParseUint(parts[0], 10, 8)
				v2, e2 := strconv.ParseUint(parts[1], 10, 8)
				if e1 != nil || e2 != nil {
					propErr = fmt.Errorf("invalid uint8 values in '%s %s' for margin: %v / %v", parts[0], parts[1], e1, e2)
					propAdded = false
				} else {
					vert, horiz := uint8(v1), uint8(v2)
					finalTop, finalBottom = vert, vert
					finalRight, finalLeft = horiz, horiz
				}
			case 4:
				v1, e1 := strconv.ParseUint(parts[0], 10, 8)
				v2, e2 := strconv.ParseUint(parts[1], 10, 8)
				v3, e3 := strconv.ParseUint(parts[2], 10, 8)
				v4, e4 := strconv.ParseUint(parts[3], 10, 8)
				if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
					propErr = fmt.Errorf("invalid uint8 values in '%s %s %s %s' for margin: %v/%v/%v/%v", parts[0], parts[1], parts[2], parts[3], e1, e2, e3, e4)
					propAdded = false
				} else {
					finalTop, finalRight, finalBottom, finalLeft = uint8(v1), uint8(v2), uint8(v3), uint8(v4)
				}
			default:
				propErr = fmt.Errorf("invalid number of values (%d) for margin shorthand '%s', expected 1, 2, or 4", len(parts), cleanedString)
				propAdded = false
			}
			if propAdded {
				marginBuf := []byte{finalTop, finalRight, finalBottom, finalLeft}
				krbProp = &KrbProperty{PropertyID: PropIDMargin, ValueType: ValTypeEdgeInsets, Size: 4, Value: marginBuf}
			}

		case "text", "content":
			strIdx, err := state.addString(cleanedString)
			if err != nil {
				propErr = fmt.Errorf("failed to add string '%s' to table: %w", cleanedString, err)
			} else {
				krbProp = &KrbProperty{PropertyID: PropIDTextContent, ValueType: ValTypeString, Size: 1, Value: []byte{strIdx}}
				propID = PropIDTextContent
				propAdded = true
			}

		case "font_size":
			fs, e := strconv.ParseUint(cleanedString, 10, 16)
			if e == nil && fs > 0 && fs <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(fs))
				krbProp = &KrbProperty{PropertyID: PropIDFontSize, ValueType: ValTypeShort, Size: 2, Value: buf}
				propID = PropIDFontSize
				propAdded = true
			} else if e != nil {
				propErr = fmt.Errorf("invalid uint16 for font_size '%s': %w", cleanedString, e)
			} else { // fs == 0 or fs > MaxUint16
				propErr = fmt.Errorf("font_size value '%s' out of range (1-%d)", cleanedString, math.MaxUint16)
			}

		case "font_weight":
			weight := uint8(0) // Default Normal
			switch strings.ToLower(cleanedString) {
			case "normal", "400":
				weight = 0
			case "bold", "700":
				weight = 1
			default:
				log.Printf("L%d: Warning: Invalid font_weight '%s' in style '%s', using 'normal'.", lineNum, cleanedString, style.SourceName)
			}
			krbProp = &KrbProperty{PropertyID: PropIDFontWeight, ValueType: ValTypeEnum, Size: 1, Value: []byte{weight}}
			propID = PropIDFontWeight
			propAdded = true

		case "text_alignment":
			align := uint8(0) // Default Start/Left
			switch strings.ToLower(cleanedString) {
			case "center", "centre":
				align = 1
			case "right", "end":
				align = 2
			case "left", "start":
				align = 0
			default:
				log.Printf("L%d: Warning: Invalid text_alignment '%s' in style '%s', using 'start'.", lineNum, cleanedString, style.SourceName)
			}
			krbProp = &KrbProperty{PropertyID: PropIDTextAlignment, ValueType: ValTypeEnum, Size: 1, Value: []byte{align}}
			propID = PropIDTextAlignment
			propAdded = true

		case "layout":
			layoutByte := parseLayoutString(cleanedString) // Assumes parseLayoutString is defined elsewhere
			krbProp = &KrbProperty{PropertyID: PropIDLayoutFlags, ValueType: ValTypeByte, Size: 1, Value: []byte{layoutByte}}
			propID = PropIDLayoutFlags
			propAdded = true

		case "gap":
			g, e := strconv.ParseUint(cleanedString, 10, 16)
			if e == nil && g <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(g))
				krbProp = &KrbProperty{PropertyID: PropIDGap, ValueType: ValTypeShort, Size: 2, Value: buf}
				propID = PropIDGap
				propAdded = true
			} else if e != nil {
				propErr = fmt.Errorf("invalid uint16 for gap '%s': %w", cleanedString, e)
			} else { // g > MaxUint16
				propErr = fmt.Errorf("gap value '%s' out of range (0-%d)", cleanedString, math.MaxUint16)
			}

		case "overflow":
			ovf := uint8(0) // Default Visible
			switch strings.ToLower(cleanedString) {
			case "visible":
				ovf = 0
			case "hidden":
				ovf = 1
			case "scroll":
				ovf = 2
			default:
				log.Printf("L%d: Warning: Invalid overflow '%s' in style '%s', using 'visible'.", lineNum, cleanedString, style.SourceName)
			}
			krbProp = &KrbProperty{PropertyID: PropIDOverflow, ValueType: ValTypeEnum, Size: 1, Value: []byte{ovf}}
			propID = PropIDOverflow
			propAdded = true

		case "width", "min_width", "max_width", "height", "min_height", "max_height":
			var targetPropID uint8
			switch key {
			case "width":
				targetPropID = PropIDMaxWidth
			case "min_width":
				targetPropID = PropIDMinWidth
			case "max_width":
				targetPropID = PropIDMaxWidth
			case "height":
				targetPropID = PropIDMaxHeight
			case "min_height":
				targetPropID = PropIDMinHeight
			case "max_height":
				targetPropID = PropIDMaxHeight
			}

			if strings.HasSuffix(cleanedString, "%") {
				percentStr := strings.TrimSuffix(cleanedString, "%")
				percentF, e := strconv.ParseFloat(percentStr, 64)
				if e == nil && percentF >= 0 {
					fixedPointVal := uint16(math.Round(percentF / 100.0 * 256.0))
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, fixedPointVal)
					krbProp = &KrbProperty{PropertyID: targetPropID, ValueType: ValTypePercentage, Size: 2, Value: buf}
					propID = targetPropID
					propAdded = true
					state.HeaderFlags |= FlagFixedPoint
				} else {
					propErr = fmt.Errorf("invalid percentage float for %s '%s': %w", key, percentStr, e)
				}
			} else {
				v, e := strconv.ParseUint(cleanedString, 10, 16)
				if e == nil && v <= math.MaxUint16 {
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, uint16(v))
					krbProp = &KrbProperty{PropertyID: targetPropID, ValueType: ValTypeShort, Size: 2, Value: buf}
					propID = targetPropID
					propAdded = true
				} else if e != nil {
					propErr = fmt.Errorf("invalid uint16 for %s '%s': %w", key, cleanedString, e)
				} else {
					propErr = fmt.Errorf("%s value '%s' out of range (0-%d)", key, cleanedString, math.MaxUint16)
				}
			}

		case "aspect_ratio":
			arF, e := strconv.ParseFloat(cleanedString, 64)
			if e == nil && arF >= 0 {
				fixedPointVal := uint16(math.Round(arF * 256.0)) // 8.8 fixed point
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, fixedPointVal)
				krbProp = &KrbProperty{PropertyID: PropIDAspectRatio, ValueType: ValTypePercentage, Size: 2, Value: buf}
				state.HeaderFlags |= FlagFixedPoint
				propID = PropIDAspectRatio
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid positive float for aspect_ratio '%s': %w", cleanedString, e)
			}

		case "opacity":
			f, e := strconv.ParseFloat(cleanedString, 64)
			if e == nil && f >= 0.0 && f <= 1.0 {
				fixedPointVal := uint16(math.Round(f * 256.0))
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, fixedPointVal)
				krbProp = &KrbProperty{PropertyID: PropIDOpacity, ValueType: ValTypePercentage, Size: 2, Value: buf}
				propID = PropIDOpacity
				propAdded = true
				state.HeaderFlags |= FlagFixedPoint
			} else if e != nil {
				propErr = fmt.Errorf("invalid float for opacity '%s': %w", cleanedString, e)
			} else { // Out of range
				propErr = fmt.Errorf("opacity value '%s' out of range (0.0-1.0)", cleanedString)
			}

		case "visibility", "visible":
			visBool := false // Default to false if parsing fails
			switch strings.ToLower(cleanedString) {
			case "true", "visible", "1":
				visBool = true
			case "false", "hidden", "0":
				visBool = false
			default:
				propErr = fmt.Errorf("invalid boolean value '%s' for visibility", cleanedString)
			}
			if propErr == nil {
				valByte := uint8(0)
				if visBool {
					valByte = 1
				}
				krbProp = &KrbProperty{PropertyID: PropIDVisibility, ValueType: ValTypeByte, Size: 1, Value: []byte{valByte}}
				propID = PropIDVisibility
				propAdded = true
			}

		case "z_index":
			zIntValue, eInt := strconv.ParseInt(cleanedString, 10, 16) // int16 for z-index
			if eInt == nil {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(zIntValue)) // Store as uint16 in KRB
				krbProp = &KrbProperty{PropertyID: PropIDZindex, ValueType: ValTypeShort, Size: 2, Value: buf}
				propID = PropIDZindex
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid int16 for z_index '%s': %w", cleanedString, eInt)
			}

		case "transform":
			strIdx, err := state.addString(cleanedString)
			if err != nil {
				propErr = fmt.Errorf("failed to add transform string '%s': %w", cleanedString, err)
			} else {
				krbProp = &KrbProperty{PropertyID: PropIDTransform, ValueType: ValTypeString, Size: 1, Value: []byte{strIdx}}
				propID = PropIDTransform
				propAdded = true
			}

		case "shadow":
			strIdx, err := state.addString(cleanedString)
			if err != nil {
				propErr = fmt.Errorf("failed to add shadow string '%s': %w", cleanedString, err)
			} else {
				krbProp = &KrbProperty{PropertyID: PropIDShadow, ValueType: ValTypeString, Size: 1, Value: []byte{strIdx}}
				propID = PropIDShadow
				propAdded = true
			}

		default:
			// Unhandled property key in styles
			log.Printf("L%d: Warning: Unhandled property '%s' in style '%s'. Ignored.", lineNum, key, style.SourceName)
		} // End switch key

		// Handle any error during conversion
		if propErr != nil {
			style.IsResolved = false // Mark style as failed to prevent partial resolution
			return fmt.Errorf("L%d: error processing property '%s: %s' in style '%s': %w", lineNum, key, valStr, style.SourceName, propErr)
		}

		// Merge the successfully converted KRB property
		if propAdded && propID != PropIDInvalid {
			if _, exists := mergedProps[propID]; exists {
				overrideCount++
			} else {
				addedCount++
			}
			mergedProps[propID] = *krbProp // Direct properties override anything inherited
		}
	} // End loop through source properties

	// --- Step 3: Finalize Resolved Properties and Calculate Size ---
	style.Properties = make([]KrbProperty, 0, len(mergedProps))
	propIDs := make([]uint8, 0, len(mergedProps))
	for id := range mergedProps {
		propIDs = append(propIDs, id)
	}
	// Sort properties by ID for canonical output and consistent sizing
	sort.Slice(propIDs, func(i, j int) bool {
		return propIDs[i] < propIDs[j]
	})

	finalSize := uint32(3) // StyleHeader base size: ID(1) + NameIdx(1) + PropCount(1)
	for _, propID := range propIDs {
		prop := mergedProps[propID]
		style.Properties = append(style.Properties, prop)
		finalSize += 3 + uint32(prop.Size) // PropHeader: PropID(1) + ValType(1) + Size(1) + Data(prop.Size)
	}

	style.CalculatedSize = finalSize
	style.IsResolved = true // Mark as successfully resolved

	return nil // Success
}
