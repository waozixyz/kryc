package main

import (
	"encoding/binary" // Make sure this is imported if needed below
	"fmt"
	"log"
	"strconv"
	"strings"
	"math"
)

func (state *CompilerState) resolveStyleInheritance() error {
	log.Println("Pass 1.2: Resolving style inheritance...")

	for i := range state.Styles {
		state.Styles[i].IsResolved = false
		state.Styles[i].IsResolving = false
		state.Styles[i].Properties = make([]KrbProperty, 0, len(state.Styles[i].SourceProperties))
	}

	resolvedCount := 0
	for i := range state.Styles {
		if !state.Styles[i].IsResolved {
			if err := state.resolveSingleStyle(&state.Styles[i]); err != nil {
				return fmt.Errorf("failed to resolve style inheritance: %w", err)
			}
		}
		currentResolved := 0
		for k := range state.Styles {
			if state.Styles[k].IsResolved {
				currentResolved++
			}
		}
		resolvedCount = currentResolved
		if resolvedCount == len(state.Styles) {
			break
		}
	}

	if resolvedCount != len(state.Styles) {
		log.Println("Warning: Style resolution loop finished but not all styles marked resolved.")
		for i := range state.Styles {
			if !state.Styles[i].IsResolved {
				log.Printf("       - Unresolved style: '%s'\n", state.Styles[i].SourceName)
				if err := state.resolveSingleStyle(&state.Styles[i]); err != nil {
					return fmt.Errorf("failed to resolve remaining style '%s': %w", state.Styles[i].SourceName, err)
				}
			}
		}
	}

	log.Printf("   Style inheritance resolution complete. %d styles processed.\n", len(state.Styles))
	return nil
}

func (state *CompilerState) resolveSingleStyle(style *StyleEntry) error {
	if style.IsResolved {
		return nil // Already done
	}
	if style.IsResolving {
		return fmt.Errorf("cyclic style inheritance detected involving style '%s'", style.SourceName)
	}

	style.IsResolving = true
	defer func() { style.IsResolving = false }()

	mergedProps := make(map[uint8]KrbProperty) // Key: PropID

	// --- 1. Resolve and Apply Base Style (if any) ---
	if style.ExtendsStyleName != "" {
		baseStyle := state.findStyleByName(style.ExtendsStyleName)
		if baseStyle == nil {
			extendsLine := 0 // Find line where extends was defined for better error
			for _, sp := range style.SourceProperties {
				if sp.Key == "extends" {
					extendsLine = sp.LineNum
					break
				}
			}
			return fmt.Errorf("L%d: base style '%s' not found for style '%s'", extendsLine, style.ExtendsStyleName, style.SourceName)
		}

		// Recursively resolve the base style first
		if err := state.resolveSingleStyle(baseStyle); err != nil {
			// Add context about which style was trying to extend the failed base
			return fmt.Errorf("error resolving base style '%s' needed by style '%s': %w", baseStyle.SourceName, style.SourceName, err)
		}

		// Copy resolved properties from the base
		style.BaseStyleID = baseStyle.ID // Store resolved base ID
		for _, baseProp := range baseStyle.Properties {
			mergedProps[baseProp.PropertyID] = baseProp // Add base properties to the map
		}
		// log.Printf("      '%s' inherits %d props from '%s'\n", style.SourceName, len(baseStyle.Properties), baseStyle.SourceName) // Optional verbose logging
	}

	// --- 2. Apply/Override with Own Source Properties ---
	overrideCount := 0
	addedCount := 0
	for _, sp := range style.SourceProperties {
		key := sp.Key
		valStr := sp.ValueStr // Original value string (might have quotes, comments)
		lineNum := sp.LineNum

		if key == "extends" {
			continue // Skip the extends directive itself
		}

		// --- Strip inline comments and trim whitespace (Revised Logic) ---
		valuePart := valStr
		trimmedValueBeforeCommentCheck := strings.TrimSpace(valuePart) // Trim leading/trailing space first

		commentIndex := -1
		// Find the first '#' that is NOT inside quotes (simple check, assumes no '#' in quoted strings)
		inQuotes := false
		for i, r := range trimmedValueBeforeCommentCheck {
			if r == '"' {
				inQuotes = !inQuotes
			}
			if r == '#' && !inQuotes {
				commentIndex = i
				break
			}
		}

		// If a comment marker was found...
		if commentIndex != -1 {
			// Check if it's at the very beginning of the trimmed string.
			// If yes, it's likely part of the value (e.g., hex color).
			if commentIndex == 0 {
				// Keep the whole trimmed string as the value
				valuePart = trimmedValueBeforeCommentCheck
			} else {
				// It's not at the beginning, treat it as a comment delimiter.
				// Take the substring before the comment marker.
				valuePart = trimmedValueBeforeCommentCheck[:commentIndex]
			}
		} else {
			// No comment marker found, use the initially trimmed string.
			valuePart = trimmedValueBeforeCommentCheck
		}

		// Final trim after potential slicing to remove any space left before a comment
		trimmedValue := strings.TrimSpace(valuePart)
		// --- End Revised Comment Stripping ---


		// --- Prepare for KRB property conversion ---
		var krbProp *KrbProperty
		var propErr error

		// --- Convert KRY key/value to KRB Property ---
		// Use 'trimmedValue' for parsing numbers, colors, enums, etc.
		// Use original 'valStr' when passing raw strings (like for text or resource paths) that need quote handling later.
		switch key {
		// --- Colors ---
		case "background_color":
			if col, ok := parseColor(trimmedValue); ok { // Pass the processed value
				krbProp = &KrbProperty{PropIDBgColor, ValTypeColor, 4, col[:]}
				state.HeaderFlags |= FlagExtendedColor
			} else { propErr = fmt.Errorf("invalid color value '%s'", trimmedValue) } // Report error with the value tried
		case "text_color", "foreground_color":
			if col, ok := parseColor(trimmedValue); ok {
				krbProp = &KrbProperty{PropIDFgColor, ValTypeColor, 4, col[:]}
				state.HeaderFlags |= FlagExtendedColor
			} else { propErr = fmt.Errorf("invalid color value '%s'", trimmedValue) }
		case "border_color":
			if col, ok := parseColor(trimmedValue); ok {
				krbProp = &KrbProperty{PropIDBorderColor, ValTypeColor, 4, col[:]}
				state.HeaderFlags |= FlagExtendedColor
			} else { propErr = fmt.Errorf("invalid color value '%s'", trimmedValue) }

		// --- Border ---
		case "border_width":
			if bw, e := strconv.ParseUint(trimmedValue, 10, 8); e == nil {
				krbProp = &KrbProperty{PropIDBorderWidth, ValTypeByte, 1, []byte{uint8(bw)}}
			} else { propErr = fmt.Errorf("invalid border_width uint8 value '%s': %w", trimmedValue, e) }
		case "border_radius":
			if br, e := strconv.ParseUint(trimmedValue, 10, 8); e == nil {
				krbProp = &KrbProperty{PropIDBorderRadius, ValTypeByte, 1, []byte{uint8(br)}}
			} else { propErr = fmt.Errorf("invalid border_radius uint8 value '%s': %w", trimmedValue, e) }

		// --- Box Model (Simple Implementation) ---
		case "padding":
			if p, e := strconv.ParseUint(trimmedValue, 10, 8); e == nil {
				buf := []byte{uint8(p), uint8(p), uint8(p), uint8(p)}
				krbProp = &KrbProperty{PropIDPadding, ValTypeEdgeInsets, 4, buf}
			} else { propErr = fmt.Errorf("invalid padding uint8 value '%s' (expects single number): %w", trimmedValue, e) }
		case "margin":
			if m, e := strconv.ParseUint(trimmedValue, 10, 8); e == nil {
				buf := []byte{uint8(m), uint8(m), uint8(m), uint8(m)}
				krbProp = &KrbProperty{PropIDMargin, ValTypeEdgeInsets, 4, buf}
			} else { propErr = fmt.Errorf("invalid margin uint8 value '%s' (expects single number): %w", trimmedValue, e) }

		// --- Text ---
		case "text", "content":
			strIdx, err := state.addString(valStr) // Use original valStr to preserve quotes for addString
			if err != nil { propErr = fmt.Errorf("failed adding string: %w", err)
			} else { krbProp = &KrbProperty{PropIDTextContent, ValTypeString, 1, []byte{strIdx}} }
		case "font_size":
			if fs, e := strconv.ParseUint(trimmedValue, 10, 16); e == nil && fs > 0 && fs <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(fs))
				krbProp = &KrbProperty{PropIDFontSize, ValTypeShort, 2, buf}
			} else if e != nil { propErr = fmt.Errorf("invalid font_size uint16 value '%s': %w", trimmedValue, e)
			} else { propErr = fmt.Errorf("font_size '%s' out of range (1-%d)", trimmedValue, math.MaxUint16) }
		case "font_weight":
			weight := uint8(0)
			switch strings.ToLower(trimmedValue) {
			case "normal", "400": weight = 0
			case "bold", "700": weight = 1
			default: log.Printf("L%d: Warning: Invalid font_weight '%s' in style '%s', defaulting to normal.\n", lineNum, trimmedValue, style.SourceName)
			}
			krbProp = &KrbProperty{PropIDFontWeight, ValTypeEnum, 1, []byte{weight}}
		case "text_alignment":
			align := uint8(0)
			switch trimmedValue {
			case "center", "centre": align = 1
			case "right", "end": align = 2
			case "left", "start": align = 0
			default: log.Printf("L%d: Warning: Invalid text_alignment '%s' in style '%s', defaulting to left.\n", lineNum, trimmedValue, style.SourceName)
			}
			krbProp = &KrbProperty{PropIDTextAlignment, ValTypeEnum, 1, []byte{align}}

		// --- Layout (Within Parent/Self) ---
		case "layout":
			layoutByte := parseLayoutString(trimmedValue)
			krbProp = &KrbProperty{PropIDLayoutFlags, ValTypeByte, 1, []byte{layoutByte}}
		case "gap":
			if g, e := strconv.ParseUint(trimmedValue, 10, 16); e == nil && g <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(g))
				krbProp = &KrbProperty{PropIDGap, ValTypeShort, 2, buf}
			} else if e != nil { propErr = fmt.Errorf("invalid gap uint16 value '%s': %w", trimmedValue, e)
			} else { propErr = fmt.Errorf("gap '%s' out of range (0-%d)", trimmedValue, math.MaxUint16) }
		case "overflow":
			ovf := uint8(0)
			switch trimmedValue {
			case "visible": ovf = 0
			case "hidden": ovf = 1
			case "scroll": ovf = 2
			default: log.Printf("L%d: Warning: Invalid overflow '%s' in style '%s', defaulting to visible.\n", lineNum, trimmedValue, style.SourceName)
			}
			krbProp = &KrbProperty{PropIDOverflow, ValTypeEnum, 1, []byte{ovf}}

		// --- Sizing ---
		case "width":
			if v, e := strconv.ParseUint(trimmedValue, 10, 16); e == nil && v <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(v))
				krbProp = &KrbProperty{PropIDMaxWidth, ValTypeShort, 2, buf}
			} else if e != nil { propErr = fmt.Errorf("invalid width uint16 value '%s': %w", trimmedValue, e)
			} else { propErr = fmt.Errorf("width '%s' out of range (0-%d)", trimmedValue, math.MaxUint16) }
		case "height":
			if v, e := strconv.ParseUint(trimmedValue, 10, 16); e == nil && v <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(v))
				krbProp = &KrbProperty{PropIDMaxHeight, ValTypeShort, 2, buf}
			} else if e != nil { propErr = fmt.Errorf("invalid height uint16 value '%s': %w", trimmedValue, e)
			} else { propErr = fmt.Errorf("height '%s' out of range (0-%d)", trimmedValue, math.MaxUint16) }
		case "min_width":
			if v, e := strconv.ParseUint(trimmedValue, 10, 16); e == nil && v <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(v))
				krbProp = &KrbProperty{PropIDMinWidth, ValTypeShort, 2, buf}
			} else if e != nil { propErr = fmt.Errorf("invalid min_width uint16 value '%s': %w", trimmedValue, e)
			} else { propErr = fmt.Errorf("min_width '%s' out of range (0-%d)", trimmedValue, math.MaxUint16) }
		case "min_height":
			if v, e := strconv.ParseUint(trimmedValue, 10, 16); e == nil && v <= math.MaxUint16 {
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, uint16(v))
				krbProp = &KrbProperty{PropIDMinHeight, ValTypeShort, 2, buf}
			} else if e != nil { propErr = fmt.Errorf("invalid min_height uint16 value '%s': %w", trimmedValue, e)
			} else { propErr = fmt.Errorf("min_height '%s' out of range (0-%d)", trimmedValue, math.MaxUint16) }
		case "aspect_ratio":
			if arF, e := strconv.ParseFloat(trimmedValue, 64); e == nil && arF >= 0 {
				fixedPointVal := uint16(arF * 256.0)
				buf := make([]byte, 2)
				binary.LittleEndian.PutUint16(buf, fixedPointVal)
				krbProp = &KrbProperty{PropIDAspectRatio, ValTypePercentage, 2, buf}
				state.HeaderFlags |= FlagFixedPoint
			} else {
				propErr = fmt.Errorf("invalid positive float aspect_ratio '%s': %w", trimmedValue, e)
			}

		// --- Visual Effects ---
		case "opacity":
			if op, e := strconv.ParseUint(trimmedValue, 10, 8); e == nil {
				krbProp = &KrbProperty{PropIDOpacity, ValTypeByte, 1, []byte{uint8(op)}}
			} else { propErr = fmt.Errorf("invalid opacity uint8 value (0-255) '%s': %w", trimmedValue, e) }
		case "visibility", "visible":
			visBool := false
			switch strings.ToLower(trimmedValue) {
			case "true", "visible", "1": visBool = true
			case "false", "hidden", "0": visBool = false
			default: propErr = fmt.Errorf("invalid boolean visibility value '%s'", trimmedValue)
			}
			if propErr == nil {
				valByte := uint8(0)
				if visBool { valByte = 1 }
				krbProp = &KrbProperty{PropIDVisibility, ValTypeByte, 1, []byte{valByte}}
			}
		case "z_index":
			if z_uint, e_uint := strconv.ParseUint(trimmedValue, 10, 16); e_uint == nil {
					buf := make([]byte, 2)
					binary.LittleEndian.PutUint16(buf, uint16(z_uint))
					krbProp = &KrbProperty{PropIDZindex, ValTypeShort, 2, buf}
				} else {
					propErr = fmt.Errorf("invalid z_index uint16 value '%s': %w", trimmedValue, e_uint)
				}


		// --- Complex / String-based ---
		case "transform":
			strIdx, err := state.addString(valStr) // Use original valStr
			if err != nil { propErr = fmt.Errorf("failed adding transform string: %w", err)
			} else { krbProp = &KrbProperty{PropIDTransform, ValTypeString, 1, []byte{strIdx}} }
		case "shadow":
			strIdx, err := state.addString(valStr) // Use original valStr
			if err != nil { propErr = fmt.Errorf("failed adding shadow string: %w", err)
			} else { krbProp = &KrbProperty{PropIDShadow, ValTypeString, 1, []byte{strIdx}} }

		// --- Ignored / Unhandled ---
		default:
			log.Printf("L%d: Warning: Unhandled property '%s' in style '%s'. Ignored.", lineNum, key, style.SourceName)

		} // End switch key

		// --- Handle Errors and Merge ---
		if propErr != nil {
			return fmt.Errorf("L%d: error processing property '%s' in style '%s': %w", lineNum, key, style.SourceName, propErr)
		}

		if krbProp != nil {
			if _, exists := mergedProps[krbProp.PropertyID]; exists {
				overrideCount++
			} else {
				addedCount++
			}
			mergedProps[krbProp.PropertyID] = *krbProp
		}
	} // End loop through source properties

	// --- 3. Finalize Properties and Size ---
	style.Properties = make([]KrbProperty, 0, len(mergedProps))
	finalSize := uint32(3) // StyleHeader: ID(1) + NameIdx(1) + PropCount(1)
	for _, prop := range mergedProps {
		style.Properties = append(style.Properties, prop)
		finalSize += 3 + uint32(prop.Size)
	}
	style.CalculatedSize = finalSize

	style.IsResolved = true
	// log.Printf("      Resolved style '%s': %d final props (%d added, %d overrides). Size: %d\n", style.SourceName, len(style.Properties), addedCount, overrideCount, style.CalculatedSize)

	return nil // Success for this style
}

// findStyleByName finds a StyleEntry by its source name.
func (state *CompilerState) findStyleByName(name string) *StyleEntry {
	cleaned := trimQuotes(strings.TrimSpace(name))
	if cleaned == "" {
		return nil
	}
	for i := range state.Styles {
		if state.Styles[i].SourceName == cleaned {
			return &state.Styles[i]
		}
	}
	return nil
}

// findStyleIDByName finds a style ID (1-based) by its source name.
func (state *CompilerState) findStyleIDByName(name string) uint8 {
	style := state.findStyleByName(name)
	if style != nil {
		return style.ID
	}
	return 0
}
