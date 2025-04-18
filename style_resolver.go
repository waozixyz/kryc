// style_resolver.go
package main

import (
	"encoding/binary" // For number conversions (e.g., font_size)
	"fmt"
	"log"
	"math" // For MaxUint16 etc.
	"sort" // For sorting final properties by ID
	"strconv" // For parsing numbers from strings
	"strings" // For string manipulation (ToLower, Fields etc.)
)

// --- resolveStyleInheritance ---
// This is the main entry point for Pass 1.2. It iterates through all defined styles
// and ensures their inheritance (`extends`) is resolved and their final set of
// KRB properties is calculated. It handles potential dependencies and detects cycles.
func (state *CompilerState) resolveStyleInheritance() error {
	log.Println("Pass 1.2: Resolving style inheritance...")

	// Reset resolution state for all styles before starting the pass.
	// This ensures a clean state, especially if the compiler supports partial recompilation.
	for i := range state.Styles {
		state.Styles[i].IsResolved = false  // Mark as not yet processed in this pass
		state.Styles[i].IsResolving = false // Clear the cycle detection flag
		// Clear out any previously resolved KRB properties. We rebuild this list.
		state.Styles[i].Properties = make([]KrbProperty, 0, len(state.Styles[i].SourceProperties))
	}

	resolvedCount := 0       // Counter for successfully resolved styles
	totalStyles := len(state.Styles)

	// Iteratively attempt to resolve styles. This loop handles dependencies where
	// style A extends B, and B must be resolved before A can be fully processed.
	// We limit iterations to slightly more than the total number of styles
	// to prevent infinite loops in case of unresolvable dependencies (like missing base styles)
	// that don't form a direct cycle detected by `IsResolving`.
	maxIterations := totalStyles*2 + 1 // Generous limit for iterations
	for i := 0; i < maxIterations && resolvedCount < totalStyles; i++ {
		madeProgress := false // Flag to track if any style was successfully resolved in this iteration

		// Iterate through all defined styles
		for j := range state.Styles {
			// Skip styles that have already been successfully resolved in a previous iteration
			if !state.Styles[j].IsResolved {
				// Attempt to resolve the current style using the recursive helper function
				err := state.resolveSingleStyle(&state.Styles[j])
				if err == nil {
					// Success! This style is now fully resolved.
					madeProgress = true
					resolvedCount++ // Increment the counter
				} else if strings.Contains(err.Error(), "cyclic style inheritance detected") {
					// If a cycle was explicitly detected by the helper, fail immediately.
					return err // Propagate the cycle error up
				}
				// Ignore other errors (like "base style not found") for now.
				// The missing base might be resolved later in this iteration or the next one.
			}
		}

		// Optimization: If we went through all styles in an iteration and made no progress,
		// but we still haven't resolved all styles, it implies an unresolvable situation
		// (like a missing base style). We can break early to avoid unnecessary iterations.
		if !madeProgress {
			break
		}
	}

	// Final check: After the loops, verify if all styles were successfully resolved.
	if resolvedCount != totalStyles {
		log.Println("Warning: Style resolution finished but not all styles marked resolved.")
		// Log the names of the styles that remain unresolved.
		for i := range state.Styles {
			if !state.Styles[i].IsResolved {
				// Try one last time to resolve, primarily to get the specific error message for logging.
				err := state.resolveSingleStyle(&state.Styles[i])
				log.Printf("       - Unresolved style: '%s' (Error: %v)", state.Styles[i].SourceName, err)
			}
		}
		// Depending on requirements, you might want to treat unresolved styles as a fatal error.
		return fmt.Errorf("%d styles remain unresolved", totalStyles-resolvedCount)
	}

	log.Printf("   Style inheritance resolution complete. %d styles processed.\n", totalStyles)
	return nil // All styles resolved successfully.
}

// --- resolveSingleStyle ---
// This recursive helper function resolves inheritance and properties for *one* specific style entry.
// It merges properties from its base style (if any) and then applies/overrides them
// with properties defined directly in its own source block.
func (state *CompilerState) resolveSingleStyle(style *StyleEntry) error {
	// Base Case 1: If the style is already marked as resolved, we're done.
	if style.IsResolved {
		return nil
	}
	// Base Case 2: If we encounter this style while it's already in the process of being
	// resolved (IsResolving is true), it means we've detected a cycle (e.g., A extends B, B extends A).
	if style.IsResolving {
		return fmt.Errorf("cyclic style inheritance detected involving style '%s'", style.SourceName)
	}

	// Mark this style as currently being processed. This flag is used for cycle detection.
	style.IsResolving = true
	// `defer` ensures the flag is reset to false when this function returns,
	// regardless of whether it returns normally or with an error.
	defer func() { style.IsResolving = false }()

	// Use a map to merge properties. Key: KRB Property ID (uint8), Value: KrbProperty struct.
	// This allows properties defined later (in the current style) to naturally overwrite
	// properties with the same ID inherited from a base style.
	mergedProps := make(map[uint8]KrbProperty)

	// --- Step 1: Resolve and Apply Base Style Properties (if 'extends' is used) ---
	if style.ExtendsStyleName != "" {
		// Find the base style definition using the name specified in 'extends'.
		baseStyle := state.findStyleByName(style.ExtendsStyleName)
		if baseStyle == nil {
			// The specified base style name was not found in the defined styles.
			extendsLine := 0 // Try to find the line number for better error context
			for _, sp := range style.SourceProperties {
				if sp.Key == "extends" {
					extendsLine = sp.LineNum
					break
				}
			}
			// Return an error indicating the missing base style.
			return fmt.Errorf("L%d: base style '%s' not found for style '%s'", extendsLine, style.ExtendsStyleName, style.SourceName)
		}

		// Recursively call this function to ensure the base style is resolved first.
		// This handles chained inheritance (A extends B extends C). If the base fails
		// to resolve (due to its own missing base or a cycle further down), the error is propagated up.
		if err := state.resolveSingleStyle(baseStyle); err != nil {
			return fmt.Errorf("error resolving base style '%s' needed by style '%s': %w", baseStyle.SourceName, style.SourceName, err)
		}

		// If we get here, the baseStyle is now resolved and its `Properties` slice contains the final KRB properties.
		style.BaseStyleID = baseStyle.ID // Store the 1-based ID of the resolved base style, might be useful later.

		// Copy all resolved properties from the base style into our merge map.
		for _, baseProp := range baseStyle.Properties {
			mergedProps[baseProp.PropertyID] = baseProp // Add base property to the map
		}
		// Optional log:
		// log.Printf("      Style '%s' inherited %d props from '%s'\n", style.SourceName, len(mergedProps), baseStyle.SourceName)
	}

	// --- Step 2: Apply/Override with Own Source Properties ---
	// Now process the properties defined directly within this style's block in the .kry file.
	overrideCount := 0 // Debug counter: properties overriding a base value
	addedCount := 0    // Debug counter: properties newly added by this style

	for _, sp := range style.SourceProperties {
		key := sp.Key        // e.g., "background_color"
		valStr := sp.ValueStr  // e.g., "\"#FFAA00FF\" # Orange comment"
		lineNum := sp.LineNum

		// Skip the 'extends' directive itself, it was handled in Step 1.
		if key == "extends" {
			continue
		}

		// Clean the value string: removes comments, trims whitespace, handles outer quotes.
		// Uses helper from utils.go (or wherever it's defined).
		cleanVal, quotedVal := cleanAndQuoteValue(valStr)

		var krbProp *KrbProperty // Pointer to hold the resulting KRB property struct if conversion is successful
		var propErr error        // Holds any error during parsing or conversion of this property
		propID := PropIDInvalid  // The KRB standard property ID corresponding to the source key
		propAdded := false       // Flag: True if this source line results in a valid KrbProperty

		// --- Convert KRY key/value to a standard KRB Property struct ---
		// This switch maps .kry source property names (like "border_width") to their
		// corresponding KRB Property IDs (like PropIDBorderWidth) and parses the
		// string value (like "2") into the correct KRB binary format and type.
		switch key {
		case "background_color":
			if col, ok := parseColor(quotedVal); ok { // Use quotedVal as parseColor expects quotes/hash
				krbProp = &KrbProperty{PropertyID: PropIDBgColor, ValueType: ValTypeColor, Size: 4, Value: col[:]}
				state.HeaderFlags |= FlagExtendedColor // Ensure global flag is set if RGBA color used
				propID = PropIDBgColor
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid color format '%s'", quotedVal)
			}

		case "text_color", "foreground_color":
			if col, ok := parseColor(quotedVal); ok {
				krbProp = &KrbProperty{PropIDFgColor, ValTypeColor, 4, col[:]}
				state.HeaderFlags |= FlagExtendedColor
				propID = PropIDFgColor
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid color format '%s'", quotedVal)
			}

		case "border_color":
			if col, ok := parseColor(quotedVal); ok {
				krbProp = &KrbProperty{PropIDBorderColor, ValTypeColor, 4, col[:]}
				state.HeaderFlags |= FlagExtendedColor
				propID = PropIDBorderColor
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid color format '%s'", quotedVal)
			}

		case "border_width":
			if bw, e := strconv.ParseUint(cleanVal, 10, 8); e == nil { // Use cleanVal for numbers
				krbProp = &KrbProperty{PropIDBorderWidth, ValTypeByte, 1, []byte{uint8(bw)}}
				propID = PropIDBorderWidth
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid uint8 for border_width '%s': %w", cleanVal, e)
			}

		case "border_radius":
			if br, e := strconv.ParseUint(cleanVal, 10, 8); e == nil {
				krbProp = &KrbProperty{PropIDBorderRadius, ValTypeByte, 1, []byte{uint8(br)}}
				propID = PropIDBorderRadius
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid uint8 for border_radius '%s': %w", cleanVal, e)
			}

		case "padding": // Simple version: single value applies to T, R, B, L
			if p, e := strconv.ParseUint(cleanVal, 10, 8); e == nil {
				buf := []byte{uint8(p), uint8(p), uint8(p), uint8(p)} // T, R, B, L
				krbProp = &KrbProperty{PropIDPadding, ValTypeEdgeInsets, 4, buf}
				propID = PropIDPadding
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid uint8 for padding '%s': %w", cleanVal, e)
			}

		case "margin": // Simple version: single value applies to T, R, B, L
			if m, e := strconv.ParseUint(cleanVal, 10, 8); e == nil {
				buf := []byte{uint8(m), uint8(m), uint8(m), uint8(m)}
				krbProp = &KrbProperty{PropIDMargin, ValTypeEdgeInsets, 4, buf}
				propID = PropIDMargin
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid uint8 for margin '%s': %w", cleanVal, e)
			}

		case "text", "content": // Store text content as string table index
			strIdx, err := state.addString(quotedVal) // Use quotedVal for strings
			if err != nil {
				propErr = fmt.Errorf("add string '%s': %w", quotedVal, err)
			} else {
				krbProp = &KrbProperty{PropIDTextContent, ValTypeString, 1, []byte{strIdx}}
				propID = PropIDTextContent
				propAdded = true
			}

		case "font_size":
			if fs, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && fs > 0 && fs <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(fs))
				krbProp = &KrbProperty{PropIDFontSize, ValTypeShort, 2, buf}
				propID = PropIDFontSize
				propAdded = true
			} else if e != nil {
				propErr = fmt.Errorf("invalid uint16 for font_size '%s': %w", cleanVal, e)
			} else { // Value was 0 or > MaxUint16
				propErr = fmt.Errorf("font_size value '%s' out of range (1-%d)", cleanVal, math.MaxUint16)
			}

		case "font_weight":
			weight := uint8(0) // Default Normal
			switch strings.ToLower(cleanVal) {
			case "normal", "400":
				weight = 0
			case "bold", "700":
				weight = 1
			default:
				log.Printf("L%d: Warn: Invalid font_weight '%s' in style '%s', using 'normal'.", lineNum, cleanVal, style.SourceName)
			}
			krbProp = &KrbProperty{PropIDFontWeight, ValTypeEnum, 1, []byte{weight}}
			propID = PropIDFontWeight
			propAdded = true

		case "text_alignment":
			align := uint8(0) // Default Start/Left
			switch cleanVal {
			case "center", "centre":
				align = 1
			case "right", "end":
				align = 2
			case "left", "start":
				align = 0
			default:
				log.Printf("L%d: Warn: Invalid text_alignment '%s' in style '%s', using 'start'.", lineNum, cleanVal, style.SourceName)
			}
			krbProp = &KrbProperty{PropIDTextAlignment, ValTypeEnum, 1, []byte{align}}
			propID = PropIDTextAlignment
			propAdded = true

		case "layout": // Converts layout string (e.g., "row center wrap") to the KRB Layout byte
			layoutByte := parseLayoutString(cleanVal) // Uses helper from utils.go
			krbProp = &KrbProperty{PropIDLayoutFlags, ValTypeByte, 1, []byte{layoutByte}}
			propID = PropIDLayoutFlags
			propAdded = true

		case "gap": // Space between flow layout children
			if g, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && g <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(g))
				krbProp = &KrbProperty{PropIDGap, ValTypeShort, 2, buf}
				propID = PropIDGap
				propAdded = true
			} else if e != nil {
				propErr = fmt.Errorf("invalid uint16 for gap '%s': %w", cleanVal, e)
			} else {
				propErr = fmt.Errorf("gap value '%s' out of range (0-%d)", cleanVal, math.MaxUint16)
			}

		case "overflow":
			ovf := uint8(0) // Default Visible
			switch cleanVal {
			case "visible":
				ovf = 0
			case "hidden":
				ovf = 1
			case "scroll":
				ovf = 2
			default:
				log.Printf("L%d: Warn: Invalid overflow '%s' in style '%s', using 'visible'.", lineNum, cleanVal, style.SourceName)
			}
			krbProp = &KrbProperty{PropIDOverflow, ValTypeEnum, 1, []byte{ovf}}
			propID = PropIDOverflow
			propAdded = true

		case "width": // Style 'width' maps to KRB 'MaxWidth' constraint
			if v, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && v <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(v))
				krbProp = &KrbProperty{PropIDMaxWidth, ValTypeShort, 2, buf}
				propID = PropIDMaxWidth
				propAdded = true
			} else if e != nil {
				propErr = fmt.Errorf("invalid uint16 for width '%s': %w", cleanVal, e)
			} else {
				propErr = fmt.Errorf("width value '%s' out of range (0-%d)", cleanVal, math.MaxUint16)
			}

		case "height": // Style 'height' maps to KRB 'MaxHeight' constraint
			if v, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && v <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(v))
				krbProp = &KrbProperty{PropIDMaxHeight, ValTypeShort, 2, buf}
				propID = PropIDMaxHeight
				propAdded = true
			} else if e != nil {
				propErr = fmt.Errorf("invalid uint16 for height '%s': %w", cleanVal, e)
			} else {
				propErr = fmt.Errorf("height value '%s' out of range (0-%d)", cleanVal, math.MaxUint16)
			}

		case "min_width":
			if v, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && v <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(v))
				krbProp = &KrbProperty{PropIDMinWidth, ValTypeShort, 2, buf}
				propID = PropIDMinWidth
				propAdded = true
			} else if e != nil {
				propErr = fmt.Errorf("invalid uint16 for min_width '%s': %w", cleanVal, e)
			} else {
				propErr = fmt.Errorf("min_width value '%s' out of range (0-%d)", cleanVal, math.MaxUint16)
			}

		case "min_height":
			if v, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && v <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(v))
				krbProp = &KrbProperty{PropIDMinHeight, ValTypeShort, 2, buf}
				propID = PropIDMinHeight
				propAdded = true
			} else if e != nil {
				propErr = fmt.Errorf("invalid uint16 for min_height '%s': %w", cleanVal, e)
			} else {
				propErr = fmt.Errorf("min_height value '%s' out of range (0-%d)", cleanVal, math.MaxUint16)
			}

		case "aspect_ratio":
			if arF, e := strconv.ParseFloat(cleanVal, 64); e == nil && arF >= 0 {
				fixedPointVal := uint16(math.Round(arF * 256.0)) // 8.8 fixed point
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, fixedPointVal)
				krbProp = &KrbProperty{PropIDAspectRatio, ValTypePercentage, 2, buf}
				state.HeaderFlags |= FlagFixedPoint // Ensure global flag is set
				propID = PropIDAspectRatio
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid positive float for aspect_ratio '%s': %w", cleanVal, e)
			}

		case "opacity": // 0-255
			if op, e := strconv.ParseUint(cleanVal, 10, 8); e == nil {
				krbProp = &KrbProperty{PropIDOpacity, ValTypeByte, 1, []byte{uint8(op)}}
				propID = PropIDOpacity
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid uint8 for opacity '%s': %w", cleanVal, e)
			}

		case "visibility", "visible":
			visBool := false // Default to false if parsing fails
			switch strings.ToLower(cleanVal) {
			case "true", "visible", "1":
				visBool = true
			case "false", "hidden", "0":
				visBool = false
			default:
				propErr = fmt.Errorf("invalid boolean value '%s' for visibility", cleanVal)
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

		case "z_index":
			// Parse as signed int16 first to allow negative values conceptually
			if z_int, e_int := strconv.ParseInt(cleanVal, 10, 16); e_int == nil {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(z_int)) // Store as uint16 in KRB
				krbProp = &KrbProperty{PropIDZindex, ValTypeShort, 2, buf}
				propID = PropIDZindex
				propAdded = true
			} else {
				propErr = fmt.Errorf("invalid int16 for z_index '%s': %w", cleanVal, e_int)
			}

		case "transform": // Store transform string index
			strIdx, err := state.addString(quotedVal)
			if err != nil {
				propErr = fmt.Errorf("add string '%s': %w", quotedVal, err)
			} else {
				krbProp = &KrbProperty{PropIDTransform, ValTypeString, 1, []byte{strIdx}}
				propID = PropIDTransform
				propAdded = true
			}

		case "shadow": // Store shadow string index
			strIdx, err := state.addString(quotedVal)
			if err != nil {
				propErr = fmt.Errorf("add string '%s': %w", quotedVal, err)
			} else {
				krbProp = &KrbProperty{PropIDShadow, ValTypeString, 1, []byte{strIdx}}
				propID = PropIDShadow
				propAdded = true
			}

		// Add cases for other standard styleable properties here

		default:
			// This source property key doesn't map to a known standard KRB property applicable to styles.
			log.Printf("L%d: Warning: Unhandled property '%s' in style '%s'. Ignored.", lineNum, key, style.SourceName)

		} // End switch key

		// Handle any error that occurred during the conversion of this specific property.
		if propErr != nil {
			// If conversion failed, mark the style as unresolved and return the error.
			style.IsResolved = false
			return fmt.Errorf("L%d: error processing property '%s' in style '%s': %w", lineNum, key, style.SourceName, propErr)
		}

		// --- Merge the successfully converted KRB property into the map ---
		if propAdded && propID != PropIDInvalid {
			// Check if this property ID already exists in the map (i.e., it's overriding a base style property)
			if _, exists := mergedProps[propID]; exists {
				overrideCount++ // Increment debug counter
			} else {
				addedCount++ // Increment debug counter for new properties
			}
			// Add or overwrite the property in the merge map.
			mergedProps[propID] = *krbProp
		}
	} // End loop through source properties defined in this style block

	// --- Step 3: Finalize Resolved Properties and Calculate Size ---
	// Clear the style's final Properties slice and populate it from the merged map,
	// ensuring the properties are sorted by KRB PropertyID for consistent output.
	style.Properties = make([]KrbProperty, 0, len(mergedProps))

	// Get a slice of the property IDs that are present in the final merged map.
	propIDs := make([]uint8, 0, len(mergedProps))
	for id := range mergedProps {
		propIDs = append(propIDs, id)
	}
	// Sort the property IDs numerically. This ensures a canonical order in the output KRB.
	sort.Slice(propIDs, func(i, j int) bool { return propIDs[i] < propIDs[j] })

	// Add the properties to the final slice in sorted order and simultaneously calculate the total size.
	finalSize := uint32(3) // StyleHeader base size: ID(1) + NameIdx(1) + PropCount(1)
	for _, propID := range propIDs {
		prop := mergedProps[propID]
		style.Properties = append(style.Properties, prop)
		// Add size of this property block: PropID(1) + ValType(1) + Size(1) + ValueData(Size bytes)
		finalSize += 3 + uint32(prop.Size)
	}

	// Store the final calculated size for this style block (needed for Pass 2 offset calculation).
	style.CalculatedSize = finalSize
	style.IsResolved = true // Mark this style as successfully resolved.

	// Optional detailed logging for debugging:
	// log.Printf("      Resolved style '%s': %d final props (%d added, %d overrides). BaseID: %d. Size: %d\n",
	// 	 style.SourceName, len(style.Properties), addedCount, overrideCount, style.BaseStyleID, style.CalculatedSize)

	return nil // Success! This style is resolved.
}

// --- Style Lookup Helpers ---

// findStyleByName finds a StyleEntry by its source name (e.g., "my_button_style").
// Used during 'extends' lookup and potentially by element style resolution.
func (state *CompilerState) findStyleByName(name string) *StyleEntry {
	// Clean the input name consistently (trim space, remove quotes)
	cleaned := trimQuotes(strings.TrimSpace(name))
	if cleaned == "" {
		return nil // Empty name is invalid
	}
	// Search through the defined styles in the compiler state
	for i := range state.Styles {
		if state.Styles[i].SourceName == cleaned {
			return &state.Styles[i] // Return pointer to the found style entry
		}
	}
	return nil // Style name not found
}

// findStyleIDByName finds a style's 1-based ID by its source name.
// Returns 0 if the style name is not found. Used by element resolver.
func (state *CompilerState) findStyleIDByName(name string) uint8 {
	style := state.findStyleByName(name) // Reuse the name lookup logic
	if style != nil {
		return style.ID // Return the 1-based ID stored in the StyleEntry
	}
	return 0 // Return 0 (invalid ID) if not found
}

func (state *CompilerState) findStyleByID(styleID uint8) *StyleEntry {
	if styleID == 0 || int(styleID) > len(state.Styles) {
		return nil // Invalid ID (0 means no style)
	}
	// Style array is 0-based, StyleID is 1-based
	return &state.Styles[styleID-1]
}