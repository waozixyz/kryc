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
	defer func() { style.IsResolving = false }() // Ensure flag is reset

	// Map to merge properties (KRB Prop ID -> KrbProperty)
	mergedProps := make(map[uint8]KrbProperty)

	// --- Step 1: Resolve and Apply Base Style Properties ---
	if len(style.ExtendsStyleNames) > 0 {
		for _, baseName := range style.ExtendsStyleNames {
			baseStyle := state.findStyleByName(baseName) // findStyleByName is defined elsewhere (e.g., resolver.go or utils.go)
			if baseStyle == nil {
				extendsLine := 0 // Find line num for error context
				for _, sp := range style.SourceProperties {
					if sp.Key == "extends" {
						extendsLine = sp.LineNum
						break
					}
				}
				return fmt.Errorf("L%d: base style '%s' not found for style '%s'", extendsLine, baseName, style.SourceName)
			}

			// Recursively resolve the base style first.
			err := state.resolveSingleStyle(baseStyle)
			if err != nil {
				return fmt.Errorf("resolving base '%s' for '%s': %w", baseStyle.SourceName, style.SourceName, err)
			}

			// Merge resolved properties from this base style. Later bases overwrite earlier ones.
			for _, baseProp := range baseStyle.Properties {
				mergedProps[baseProp.PropertyID] = baseProp
			}
		}
	}

	// --- Step 2: Apply/Override with Own Source Properties ---
	overrideCount := 0
	addedCount := 0

	for _, sp := range style.SourceProperties {
		key := sp.Key
		valStr := sp.ValueStr
		lineNum := sp.LineNum

		if key == "extends" {
			continue // Already handled
		}

		// *** FIX: Use correct return variable names ***
		cleanedString, _ := cleanAndQuoteValue(valStr) // Use cleanedString, ignore wasQuoted boolean here

		var krbProp *KrbProperty
		var propErr error
		propID := PropIDInvalid
		propAdded := false

		// --- Convert KRY key/value to KRB Property ---
		switch key {
		case "background_color":
			// *** FIX: Use cleanedString for color parsing ***
			if col, ok := parseColor(cleanedString); ok {
				krbProp = &KrbProperty{PropertyID: PropIDBgColor, ValueType: ValTypeColor, Size: 4, Value: col[:]}
				state.HeaderFlags |= FlagExtendedColor
				propID = PropIDBgColor
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid color format '%s'", cleanedString)
			}

		case "text_color", "foreground_color":
			// *** FIX: Use cleanedString for color parsing ***
			if col, ok := parseColor(cleanedString); ok {
				krbProp = &KrbProperty{PropIDFgColor, ValTypeColor, 4, col[:]}
				state.HeaderFlags |= FlagExtendedColor
				propID = PropIDFgColor
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid color format '%s'", cleanedString)
			}

		case "border_color":
			// *** FIX: Use cleanedString for color parsing ***
			if col, ok := parseColor(cleanedString); ok {
				krbProp = &KrbProperty{PropIDBorderColor, ValTypeColor, 4, col[:]}
				state.HeaderFlags |= FlagExtendedColor
				propID = PropIDBorderColor
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid color format '%s'", cleanedString)
			}

		case "border_width":
			if bw, e := strconv.ParseUint(cleanedString, 10, 8); e == nil {
				krbProp = &KrbProperty{PropIDBorderWidth, ValTypeByte, 1, []byte{uint8(bw)}}
				propID = PropIDBorderWidth
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid uint8 for border_width '%s': %w", cleanedString, e)
			}

		case "border_radius":
			// *** FIX: Use cleanedString for number parsing ***
			if br, e := strconv.ParseUint(cleanedString, 10, 8); e == nil {
				krbProp = &KrbProperty{PropIDBorderRadius, ValTypeByte, 1, []byte{uint8(br)}}
				propID = PropIDBorderRadius
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid uint8 for border_radius '%s': %w", cleanedString, e)
			}

		case "padding": // Handles 1, 2, or 4 values
			propID = PropIDPadding
			propAdded = true // Assume success unless parsing fails
			var finalTop, finalRight, finalBottom, finalLeft uint8 = 0, 0, 0, 0
			parts := strings.Fields(cleanedString) // Split by space

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

			// Only create the KRB property if parsing was successful
			if propAdded {
				paddingBuf := []byte{finalTop, finalRight, finalBottom, finalLeft}
				krbProp = &KrbProperty{PropIDPadding, ValTypeEdgeInsets, 4, paddingBuf}
			}

		case "margin": // Handles 1, 2, or 4 values
			propID = PropIDMargin
			propAdded = true // Assume success unless parsing fails
			var finalTop, finalRight, finalBottom, finalLeft uint8 = 0, 0, 0, 0
			parts := strings.Fields(cleanedString) // Split by space

			switch len(parts) {
			case 1: // Single value: applies to all sides.
				v, e := strconv.ParseUint(parts[0], 10, 8)
				if e != nil {
					propErr = fmt.Errorf("invalid uint8 value '%s' for single margin: %w", parts[0], e)
					propAdded = false // Mark as failed
				} else {
					valByte := uint8(v)
					finalTop, finalRight, finalBottom, finalLeft = valByte, valByte, valByte, valByte
				}
			case 2: // Two values: [vertical] [horizontal].
				v1, e1 := strconv.ParseUint(parts[0], 10, 8) // Vertical (Top/Bottom)
				v2, e2 := strconv.ParseUint(parts[1], 10, 8) // Horizontal (Right/Left)
				if e1 != nil || e2 != nil {
					propErr = fmt.Errorf("invalid uint8 values in '%s %s' for margin: %v / %v", parts[0], parts[1], e1, e2)
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
					propErr = fmt.Errorf("invalid uint8 values in '%s %s %s %s' for margin: %v/%v/%v/%v", parts[0], parts[1], parts[2], parts[3], e1, e2, e3, e4)
					propAdded = false // Mark as failed
				} else {
					finalTop, finalRight, finalBottom, finalLeft = uint8(v1), uint8(v2), uint8(v3), uint8(v4)
				}
			default: // Invalid number of values for shorthand.
				propErr = fmt.Errorf("invalid number of values (%d) for margin shorthand '%s', expected 1, 2, or 4", len(parts), cleanedString)
				propAdded = false // Mark as failed
			}

			// Only create the KRB property if parsing was successful
			if propAdded {
				marginBuf := []byte{finalTop, finalRight, finalBottom, finalLeft}
				krbProp = &KrbProperty{PropIDMargin, ValTypeEdgeInsets, 4, marginBuf}
			}
		case "text", "content":
			// *** FIX: Use cleanedString for string table add ***
			strIdx, err := state.addString(cleanedString)
			if err != nil {
				propErr = fmt.Errorf("add string '%s': %w", cleanedString, err)
			} else {
				krbProp = &KrbProperty{PropIDTextContent, ValTypeString, 1, []byte{strIdx}}
				propID = PropIDTextContent
				propAdded = true
			}

		case "font_size":
			// *** FIX: Use cleanedString for number parsing ***
			fs, e := strconv.ParseUint(cleanedString, 10, 16)
			if e == nil && fs > 0 && fs <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(fs))
				krbProp = &KrbProperty{PropIDFontSize, ValTypeShort, 2, buf}
				propID = PropIDFontSize
				propAdded = true
			} else if e != nil {
				propErr = fmt.Errorf("invalid uint16 for font_size '%s': %w", cleanedString, e)
			} else {
				propErr = fmt.Errorf("font_size value '%s' out of range (1-%d)", cleanedString, math.MaxUint16)
			}

		case "font_weight":
			weight := uint8(0) // Default Normal
			// *** FIX: Use cleanedString for comparison ***
			switch strings.ToLower(cleanedString) {
			case "normal", "400":
				weight = 0
			case "bold", "700":
				weight = 1
			default:
				log.Printf("L%d: Warn: Invalid font_weight '%s' in style '%s', using 'normal'.", lineNum, cleanedString, style.SourceName)
			}
			krbProp = &KrbProperty{PropIDFontWeight, ValTypeEnum, 1, []byte{weight}}
			propID = PropIDFontWeight
			propAdded = true

		case "text_alignment":
			align := uint8(0) // Default Start/Left
			// *** FIX: Use cleanedString for comparison ***
			switch cleanedString {
			case "center", "centre":
				align = 1
			case "right", "end":
				align = 2
			case "left", "start":
				align = 0
			default:
				log.Printf("L%d: Warn: Invalid text_alignment '%s' in style '%s', using 'start'.", lineNum, cleanedString, style.SourceName)
			}
			krbProp = &KrbProperty{PropIDTextAlignment, ValTypeEnum, 1, []byte{align}}
			propID = PropIDTextAlignment
			propAdded = true

		case "layout":
			// *** FIX: Use cleanedString for layout parsing ***
			layoutByte := parseLayoutString(cleanedString)
			krbProp = &KrbProperty{PropIDLayoutFlags, ValTypeByte, 1, []byte{layoutByte}}
			propID = PropIDLayoutFlags
			propAdded = true

		case "gap":
			// *** FIX: Use cleanedString for number parsing ***
			g, e := strconv.ParseUint(cleanedString, 10, 16)
			if e == nil && g <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(g))
				krbProp = &KrbProperty{PropIDGap, ValTypeShort, 2, buf}
				propID = PropIDGap
				propAdded = true
			} else if e != nil {
				propErr = fmt.Errorf("invalid uint16 for gap '%s': %w", cleanedString, e)
			} else {
				propErr = fmt.Errorf("gap value '%s' out of range (0-%d)", cleanedString, math.MaxUint16)
			}

		case "overflow":
			ovf := uint8(0) // Default Visible
			// *** FIX: Use cleanedString for comparison ***
			switch cleanedString {
			case "visible":
				ovf = 0
			case "hidden":
				ovf = 1
			case "scroll":
				ovf = 2
			default:
				log.Printf("L%d: Warn: Invalid overflow '%s' in style '%s', using 'visible'.", lineNum, cleanedString, style.SourceName)
			}
			krbProp = &KrbProperty{PropIDOverflow, ValTypeEnum, 1, []byte{ovf}}
			propID = PropIDOverflow
			propAdded = true

		case "width":
			if strings.HasSuffix(cleanedString, "%") {
				// Handle Percentage
				percentStr := strings.TrimSuffix(cleanedString, "%")
				percentF, e := strconv.ParseFloat(percentStr, 64)
				if e == nil && percentF >= 0 {
					// Convert percentage (0-100+) to 8.8 fixed-point representation
					// 100% = 1.0 in float math = 256 in 8.8 fixed point.
					// We store the percentage value itself * 256 for consistency with opacity.
					// Runtime needs to know ValTypePercentage means % for width/height.
					// Example: 50% -> 0.5 * 256 = 128 (uint16)
					// Example: 100% -> 1.0 * 256 = 256 (uint16)
					fixedPointVal := uint16(math.Round(percentF / 100.0 * 256.0)) // Scale 0-100% to 0-256
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, fixedPointVal)

					krbProp = &KrbProperty{PropIDMaxWidth, ValTypePercentage, 2, buf}
					propID = PropIDMaxWidth
					propAdded = true
					state.HeaderFlags |= FlagFixedPoint // Ensure flag is set
				} else {
					propErr = fmt.Errorf("invalid percentage float for width '%s': %w", percentStr, e)
				}
			} else {
				// Handle Pixels (as before)
				v, e := strconv.ParseUint(cleanedString, 10, 16)
				if e == nil && v <= math.MaxUint16 {
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, uint16(v))
					krbProp = &KrbProperty{PropIDMaxWidth, ValTypeShort, 2, buf} // Use ValTypeShort for pixels
					propID = PropIDMaxWidth
					propAdded = true
				} else if e != nil {
					propErr = fmt.Errorf("invalid uint16 for width '%s': %w", cleanedString, e)
				} else {
					propErr = fmt.Errorf("width value '%s' out of range (0-%d)", cleanedString, math.MaxUint16)
				}
			}
		case "height": // Style 'height' -> PropIDMaxHeight (can be pixel or percentage)
			if strings.HasSuffix(cleanedString, "%") {
				// Handle Percentage
				percentStr := strings.TrimSuffix(cleanedString, "%")
				percentF, e := strconv.ParseFloat(percentStr, 64)
				if e == nil && percentF >= 0 {
					// Convert percentage to 8.8 fixed-point representation (0-100% -> 0-25600)
					fixedPointVal := uint16(math.Round(percentF / 100.0 * 256.0))
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, fixedPointVal)

					krbProp = &KrbProperty{PropIDMaxHeight, ValTypePercentage, 2, buf}
					propID = PropIDMaxHeight
					propAdded = true
					state.HeaderFlags |= FlagFixedPoint // Ensure flag is set
				} else {
					propErr = fmt.Errorf("invalid percentage float for height '%s': %w", percentStr, e)
				}
			} else {
				// Handle Pixels (as before)
				v, e := strconv.ParseUint(cleanedString, 10, 16)
				if e == nil && v <= math.MaxUint16 {
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, uint16(v))
					krbProp = &KrbProperty{PropIDMaxHeight, ValTypeShort, 2, buf} // Use ValTypeShort for pixels
					propID = PropIDMaxHeight
					propAdded = true
				} else if e != nil {
					propErr = fmt.Errorf("invalid uint16 for height '%s': %w", cleanedString, e)
				} else {
					propErr = fmt.Errorf("height value '%s' out of range (0-%d)", cleanedString, math.MaxUint16)
				}
			}

		case "min_width": // PropIDMinWidth (can be pixel or percentage)
			if strings.HasSuffix(cleanedString, "%") {
				// Handle Percentage
				percentStr := strings.TrimSuffix(cleanedString, "%")
				percentF, e := strconv.ParseFloat(percentStr, 64)
				if e == nil && percentF >= 0 {
					fixedPointVal := uint16(math.Round(percentF / 100.0 * 256.0))
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, fixedPointVal)

					krbProp = &KrbProperty{PropIDMinWidth, ValTypePercentage, 2, buf}
					propID = PropIDMinWidth
					propAdded = true
					state.HeaderFlags |= FlagFixedPoint
				} else {
					propErr = fmt.Errorf("invalid percentage float for min_width '%s': %w", percentStr, e)
				}
			} else {
				// Handle Pixels
				v, e := strconv.ParseUint(cleanedString, 10, 16)
				if e == nil && v <= math.MaxUint16 {
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, uint16(v))
					krbProp = &KrbProperty{PropIDMinWidth, ValTypeShort, 2, buf}
					propID = PropIDMinWidth
					propAdded = true
				} else if e != nil {
					propErr = fmt.Errorf("invalid uint16 for min_width '%s': %w", cleanedString, e)
				} else {
					propErr = fmt.Errorf("min_width value '%s' out of range (0-%d)", cleanedString, math.MaxUint16)
				}
			}

		case "min_height": // PropIDMinHeight (can be pixel or percentage)
			if strings.HasSuffix(cleanedString, "%") {
				// Handle Percentage
				percentStr := strings.TrimSuffix(cleanedString, "%")
				percentF, e := strconv.ParseFloat(percentStr, 64)
				if e == nil && percentF >= 0 {
					fixedPointVal := uint16(math.Round(percentF / 100.0 * 256.0))
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, fixedPointVal)

					krbProp = &KrbProperty{PropIDMinHeight, ValTypePercentage, 2, buf}
					propID = PropIDMinHeight
					propAdded = true
					state.HeaderFlags |= FlagFixedPoint
				} else {
					propErr = fmt.Errorf("invalid percentage float for min_height '%s': %w", percentStr, e)
				}
			} else {
				// Handle Pixels
				v, e := strconv.ParseUint(cleanedString, 10, 16)
				if e == nil && v <= math.MaxUint16 {
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, uint16(v))
					krbProp = &KrbProperty{PropIDMinHeight, ValTypeShort, 2, buf}
					propID = PropIDMinHeight
					propAdded = true
				} else if e != nil {
					propErr = fmt.Errorf("invalid uint16 for min_height '%s': %w", cleanedString, e)
				} else {
					propErr = fmt.Errorf("min_height value '%s' out of range (0-%d)", cleanedString, math.MaxUint16)
				}
			}

		case "max_width": // PropIDMaxWidth (can be pixel or percentage)
			// NOTE: 'width' already maps here. If both 'width' and 'max_width' are present,
			// the last one processed in the source file for this style will win.
			if strings.HasSuffix(cleanedString, "%") {
				// Handle Percentage
				percentStr := strings.TrimSuffix(cleanedString, "%")
				percentF, e := strconv.ParseFloat(percentStr, 64)
				if e == nil && percentF >= 0 {
					fixedPointVal := uint16(math.Round(percentF / 100.0 * 256.0))
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, fixedPointVal)

					krbProp = &KrbProperty{PropIDMaxWidth, ValTypePercentage, 2, buf}
					propID = PropIDMaxWidth
					propAdded = true
					state.HeaderFlags |= FlagFixedPoint
				} else {
					propErr = fmt.Errorf("invalid percentage float for max_width '%s': %w", percentStr, e)
				}
			} else {
				// Handle Pixels
				v, e := strconv.ParseUint(cleanedString, 10, 16)
				if e == nil && v <= math.MaxUint16 {
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, uint16(v))
					krbProp = &KrbProperty{PropIDMaxWidth, ValTypeShort, 2, buf}
					propID = PropIDMaxWidth
					propAdded = true
				} else if e != nil {
					propErr = fmt.Errorf("invalid uint16 for max_width '%s': %w", cleanedString, e)
				} else {
					propErr = fmt.Errorf("max_width value '%s' out of range (0-%d)", cleanedString, math.MaxUint16)
				}
			}

		case "max_height": // PropIDMaxHeight (can be pixel or percentage)
			// NOTE: 'height' already maps here. If both 'height' and 'max_height' are present,
			// the last one processed in the source file for this style will win.
			if strings.HasSuffix(cleanedString, "%") {
				// Handle Percentage
				percentStr := strings.TrimSuffix(cleanedString, "%")
				percentF, e := strconv.ParseFloat(percentStr, 64)
				if e == nil && percentF >= 0 {
					fixedPointVal := uint16(math.Round(percentF / 100.0 * 256.0))
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, fixedPointVal)

					krbProp = &KrbProperty{PropIDMaxHeight, ValTypePercentage, 2, buf}
					propID = PropIDMaxHeight
					propAdded = true
					state.HeaderFlags |= FlagFixedPoint
				} else {
					propErr = fmt.Errorf("invalid percentage float for max_height '%s': %w", percentStr, e)
				}
			} else {
				// Handle Pixels
				v, e := strconv.ParseUint(cleanedString, 10, 16)
				if e == nil && v <= math.MaxUint16 {
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, uint16(v))
					krbProp = &KrbProperty{PropIDMaxHeight, ValTypeShort, 2, buf}
					propID = PropIDMaxHeight
					propAdded = true
				} else if e != nil {
					propErr = fmt.Errorf("invalid uint16 for max_height '%s': %w", cleanedString, e)
				} else {
					propErr = fmt.Errorf("max_height value '%s' out of range (0-%d)", cleanedString, math.MaxUint16)
				}
			}

		case "aspect_ratio":
			arF, e := strconv.ParseFloat(cleanedString, 64)
			if e == nil && arF >= 0 {
				fixedPointVal := uint16(math.Round(arF * 256.0)) // 8.8 fixed point
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, fixedPointVal)
				krbProp = &KrbProperty{PropIDAspectRatio, ValTypePercentage, 2, buf}
				state.HeaderFlags |= FlagFixedPoint // Ensure global flag is set
				propID = PropIDAspectRatio
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid positive float for aspect_ratio '%s': %w", cleanedString, e)
			}

		case "opacity":
			// Parse as float64 (expecting 0.0 to 1.0 range)
			f, e := strconv.ParseFloat(cleanedString, 64)
			if e == nil && f >= 0.0 && f <= 1.0 {
				// Convert float (0-1) to 8.8 fixed-point (0-256) uint16
				fixedPointVal := uint16(math.Round(f * 256.0))
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, fixedPointVal)

				// Store using ValTypePercentage (KRB type for 8.8 fixed point)
				krbProp = &KrbProperty{PropIDOpacity, ValTypePercentage, 2, buf}
				propID = PropIDOpacity
				propAdded = true
				state.HeaderFlags |= FlagFixedPoint // Ensure global flag is set
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
				krbProp = &KrbProperty{PropIDVisibility, ValTypeByte, 1, []byte{valByte}}
				propID = PropIDVisibility
				propAdded = true
			}
		case "scroll_y":
			scrollBool := false
			switch strings.ToLower(cleanedString) {
			case "true", "1", "yes":
				scrollBool = true
			case "false", "0", "no":
				scrollBool = false
			default:
				propErr = fmt.Errorf("invalid boolean value '%s' for scroll_y", cleanedString)
			}
			if propErr == nil {
				// TODO: Decide how scroll_y is represented in KRB.
				// Option 1: A specific KRB Property ID (e.g., PropIDScrollFlags).
				// Option 2: Map it to PROP_ID_OVERFLOW with a specific enum value?
				// Option 3: Ignore it in styles and handle only on elements?
				// For now, let's just log and skip adding a KRB prop from styles.
				log.Printf("L%d: Info: Property 'scroll_y: %t' found in style '%s'. (KRB mapping TBD)", lineNum, scrollBool, style.SourceName)
				// krbProp = &KrbProperty{PropIDScrollFlags, ValTypeByte, 1, []byte{...}} // Example
				// propID = PropIDScrollFlags
				// propAdded = true
				propAdded = false // Temporarily don't add to KRB from style
			}
		case "z_index":
			z_int, e_int := strconv.ParseInt(cleanedString, 10, 16)
			if e_int == nil {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(z_int)) // Store as uint16 in KRB
				krbProp = &KrbProperty{PropIDZindex, ValTypeShort, 2, buf}
				propID = PropIDZindex
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid int16 for z_index '%s': %w", cleanedString, e_int)
			}

		case "transform":
			// *** FIX: Use cleanedString for string table add ***
			strIdx, err := state.addString(cleanedString)
			if err != nil {
				propErr = fmt.Errorf("add string '%s': %w", cleanedString, err)
			} else {
				krbProp = &KrbProperty{PropIDTransform, ValTypeString, 1, []byte{strIdx}}
				propID = PropIDTransform
				propAdded = true
			}

		case "shadow":
			// *** FIX: Use cleanedString for string table add ***
			strIdx, err := state.addString(cleanedString)
			if err != nil {
				propErr = fmt.Errorf("add string '%s': %w", cleanedString, err)
			} else {
				krbProp = &KrbProperty{PropIDShadow, ValTypeString, 1, []byte{strIdx}}
				propID = PropIDShadow
				propAdded = true
			}

		// Add cases for other standard styleable properties here

		default:
			// Unhandled property key in styles
			log.Printf("L%d: Warning: Unhandled property '%s' in style '%s'. Ignored.", lineNum, key, style.SourceName)

		} // End switch key

		// Handle any error during conversion
		if propErr != nil {
			style.IsResolved = false // Mark style as failed
			return fmt.Errorf("L%d: processing '%s' in style '%s': %w", lineNum, key, style.SourceName, propErr)
		}

		// Merge the successfully converted KRB property
		if propAdded && propID != PropIDInvalid {
			if _, exists := mergedProps[propID]; exists {
				overrideCount++
			} else {
				addedCount++
			}
			mergedProps[propID] = *krbProp
		}
	} // End loop through source properties

	// --- Step 3: Finalize Resolved Properties and Calculate Size ---
	style.Properties = make([]KrbProperty, 0, len(mergedProps))
	propIDs := make([]uint8, 0, len(mergedProps))
	for id := range mergedProps {
		propIDs = append(propIDs, id)
	}
	// Sort properties by ID for canonical output
	sort.Slice(propIDs, func(i, j int) bool { return propIDs[i] < propIDs[j] })

	finalSize := uint32(3) // StyleHeader base size: ID(1) + NameIdx(1) + PropCount(1)
	for _, propID := range propIDs {
		prop := mergedProps[propID]
		style.Properties = append(style.Properties, prop)
		finalSize += 3 + uint32(prop.Size) // PropID(1) + ValType(1) + Size(1) + Data
	}

	style.CalculatedSize = finalSize
	style.IsResolved = true // Mark as successfully resolved

	// Optional detailed logging
	// log.Printf("      Resolved style '%s': %d final props (%d added, %d overrides). Size: %d\n",
	// 	 style.SourceName, len(style.Properties), addedCount, overrideCount, style.CalculatedSize)

	return nil // Success
}
