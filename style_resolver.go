package main

import (
	"encoding/binary" // Make sure this is imported if needed below
	"fmt"
	"log"
	"strconv"
	"strings"
	"math"
	"sort"
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
		return nil
	}
	if style.IsResolving {
		return fmt.Errorf("cyclic style inheritance detected involving style '%s'", style.SourceName)
	}

	style.IsResolving = true
	defer func() { style.IsResolving = false }()

	mergedProps := make(map[uint8]KrbProperty) // Use PropertyID as key

	// --- 1. Resolve and Apply Base Style (if any) ---
	if style.ExtendsStyleName != "" {
		baseStyle := state.findStyleByName(style.ExtendsStyleName)
		if baseStyle == nil {
			extendsLine := 0
			for _, sp := range style.SourceProperties { if sp.Key == "extends" { extendsLine = sp.LineNum; break } }
			return fmt.Errorf("L%d: base style '%s' not found for style '%s'", extendsLine, style.ExtendsStyleName, style.SourceName)
		}

		if err := state.resolveSingleStyle(baseStyle); err != nil {
			return fmt.Errorf("error resolving base style '%s' needed by style '%s': %w", baseStyle.SourceName, style.SourceName, err)
		}

		style.BaseStyleID = baseStyle.ID
		// *** Copy ALL resolved properties from base ***
		for _, baseProp := range baseStyle.Properties {
			mergedProps[baseProp.PropertyID] = baseProp // Copy by value
		}
		log.Printf("      Style '%s' inherited %d props from '%s'\n", style.SourceName, len(mergedProps), baseStyle.SourceName)
	}

	// --- 2. Apply/Override with Own Source Properties ---
	overrideCount := 0
	addedCount := 0

	for _, sp := range style.SourceProperties {
		key := sp.Key; valStr := sp.ValueStr; lineNum := sp.LineNum
		if key == "extends" { continue } // Skip extends directive itself

		cleanVal, quotedVal := cleanAndQuoteValue(valStr)
		var krbProp *KrbProperty
		var propErr error

		propID := PropIDInvalid // Determine the KRB PropID for the KRY key
		propAdded := false      // Flag if this source property resulted in a KRB prop

		// --- Convert KRY key/value to KRB Property ---
		// (Keep the existing switch statement here, it populates krbProp and propErr)
		switch key {
		case "background_color":
			if col, ok := parseColor(quotedVal); ok { krbProp = &KrbProperty{PropIDBgColor, ValTypeColor, 4, col[:]}; state.HeaderFlags |= FlagExtendedColor; propID = PropIDBgColor; propAdded = true } else { propErr = fmt.Errorf("invalid color '%s'", quotedVal) }
		case "text_color", "foreground_color":
             if col, ok := parseColor(quotedVal); ok { krbProp = &KrbProperty{PropIDFgColor, ValTypeColor, 4, col[:]}; state.HeaderFlags |= FlagExtendedColor; propID = PropIDFgColor; propAdded = true } else { propErr = fmt.Errorf("invalid color '%s'", quotedVal) }
		case "border_color":
             if col, ok := parseColor(quotedVal); ok { krbProp = &KrbProperty{PropIDBorderColor, ValTypeColor, 4, col[:]}; state.HeaderFlags |= FlagExtendedColor; propID = PropIDBorderColor; propAdded = true } else { propErr = fmt.Errorf("invalid color '%s'", quotedVal) }
		case "border_width":
			if bw, e := strconv.ParseUint(cleanVal, 10, 8); e == nil { krbProp = &KrbProperty{PropIDBorderWidth, ValTypeByte, 1, []byte{uint8(bw)}}; propID = PropIDBorderWidth; propAdded = true } else { propErr = fmt.Errorf("invalid uint8 '%s': %w", cleanVal, e) }
		case "border_radius":
            if br, e := strconv.ParseUint(cleanVal, 10, 8); e == nil { krbProp = &KrbProperty{PropIDBorderRadius, ValTypeByte, 1, []byte{uint8(br)}}; propID = PropIDBorderRadius; propAdded = true } else { propErr = fmt.Errorf("invalid uint8 '%s': %w", cleanVal, e) }
        case "padding":
            if p, e := strconv.ParseUint(cleanVal, 10, 8); e == nil { buf := []byte{uint8(p), uint8(p), uint8(p), uint8(p)}; krbProp = &KrbProperty{PropIDPadding, ValTypeEdgeInsets, 4, buf}; propID = PropIDPadding; propAdded = true } else { propErr = fmt.Errorf("invalid uint8 '%s': %w", cleanVal, e) }
        case "margin":
             if m, e := strconv.ParseUint(cleanVal, 10, 8); e == nil { buf := []byte{uint8(m), uint8(m), uint8(m), uint8(m)}; krbProp = &KrbProperty{PropIDMargin, ValTypeEdgeInsets, 4, buf}; propID = PropIDMargin; propAdded = true } else { propErr = fmt.Errorf("invalid uint8 '%s': %w", cleanVal, e) }
        case "text", "content":
			strIdx, err := state.addString(quotedVal); if err != nil { propErr = fmt.Errorf("add string: %w", err) } else { krbProp = &KrbProperty{PropIDTextContent, ValTypeString, 1, []byte{strIdx}}; propID = PropIDTextContent; propAdded = true }
		case "font_size":
            if fs, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && fs > 0 && fs <= math.MaxUint16 { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(fs)); krbProp = &KrbProperty{PropIDFontSize, ValTypeShort, 2, buf}; propID = PropIDFontSize; propAdded = true } else if e != nil { propErr = fmt.Errorf("invalid uint16 '%s': %w", cleanVal, e) } else { propErr = fmt.Errorf("value '%s' out of range (1-%d)", cleanVal, math.MaxUint16) }
        case "font_weight":
            weight := uint8(0); switch strings.ToLower(cleanVal) { case "normal", "400": weight = 0; case "bold", "700": weight = 1; default: log.Printf("L%d: Warn: Invalid font_weight '%s'", lineNum, cleanVal) }; krbProp = &KrbProperty{PropIDFontWeight, ValTypeEnum, 1, []byte{weight}}; propID = PropIDFontWeight; propAdded = true
        case "text_alignment":
             align := uint8(0); switch cleanVal { case "center", "centre": align = 1; case "right", "end": align = 2; case "left", "start": align = 0; default: log.Printf("L%d: Warn: Invalid text_alignment '%s'", lineNum, cleanVal) }; krbProp = &KrbProperty{PropIDTextAlignment, ValTypeEnum, 1, []byte{align}}; propID = PropIDTextAlignment; propAdded = true
        case "layout": // *** Crucial: Generate PropIDLayoutFlags ***
			layoutByte := parseLayoutString(cleanVal); krbProp = &KrbProperty{PropIDLayoutFlags, ValTypeByte, 1, []byte{layoutByte}}; propID = PropIDLayoutFlags; propAdded = true
		case "gap":
             if g, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && g <= math.MaxUint16 { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(g)); krbProp = &KrbProperty{PropIDGap, ValTypeShort, 2, buf}; propID = PropIDGap; propAdded = true } else if e != nil { propErr = fmt.Errorf("invalid uint16 '%s': %w", cleanVal, e) } else { propErr = fmt.Errorf("value '%s' out of range (0-%d)", cleanVal, math.MaxUint16) }
		case "overflow":
            ovf := uint8(0); switch cleanVal { case "visible": ovf = 0; case "hidden": ovf = 1; case "scroll": ovf = 2; default: log.Printf("L%d: Warn: Invalid overflow '%s'", lineNum, cleanVal) }; krbProp = &KrbProperty{PropIDOverflow, ValTypeEnum, 1, []byte{ovf}}; propID = PropIDOverflow; propAdded = true
        case "width": // Map to MaxWidth
			if v, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && v <= math.MaxUint16 { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(v)); krbProp = &KrbProperty{PropIDMaxWidth, ValTypeShort, 2, buf}; propID = PropIDMaxWidth; propAdded = true } else if e != nil { propErr = fmt.Errorf("invalid uint16 '%s': %w", cleanVal, e) } else { propErr = fmt.Errorf("value '%s' out of range (0-%d)", cleanVal, math.MaxUint16) }
		case "height": // Map to MaxHeight
			if v, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && v <= math.MaxUint16 { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(v)); krbProp = &KrbProperty{PropIDMaxHeight, ValTypeShort, 2, buf}; propID = PropIDMaxHeight; propAdded = true } else if e != nil { propErr = fmt.Errorf("invalid uint16 '%s': %w", cleanVal, e) } else { propErr = fmt.Errorf("value '%s' out of range (0-%d)", cleanVal, math.MaxUint16) }
        case "min_width":
            if v, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && v <= math.MaxUint16 { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(v)); krbProp = &KrbProperty{PropIDMinWidth, ValTypeShort, 2, buf}; propID = PropIDMinWidth; propAdded = true } else if e != nil { propErr = fmt.Errorf("invalid uint16 '%s': %w", cleanVal, e) } else { propErr = fmt.Errorf("value '%s' out of range (0-%d)", cleanVal, math.MaxUint16) }
		case "min_height":
            if v, e := strconv.ParseUint(cleanVal, 10, 16); e == nil && v <= math.MaxUint16 { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(v)); krbProp = &KrbProperty{PropIDMinHeight, ValTypeShort, 2, buf}; propID = PropIDMinHeight; propAdded = true } else if e != nil { propErr = fmt.Errorf("invalid uint16 '%s': %w", cleanVal, e) } else { propErr = fmt.Errorf("value '%s' out of range (0-%d)", cleanVal, math.MaxUint16) }
		case "aspect_ratio":
            if arF, e := strconv.ParseFloat(cleanVal, 64); e == nil && arF >= 0 { fixedPointVal := uint16(arF * 256.0); buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, fixedPointVal); krbProp = &KrbProperty{PropIDAspectRatio, ValTypePercentage, 2, buf}; state.HeaderFlags |= FlagFixedPoint; propID = PropIDAspectRatio; propAdded = true } else { propErr = fmt.Errorf("invalid positive float '%s': %w", cleanVal, e) }
        case "opacity":
             if op, e := strconv.ParseUint(cleanVal, 10, 8); e == nil { krbProp = &KrbProperty{PropIDOpacity, ValTypeByte, 1, []byte{uint8(op)}}; propID = PropIDOpacity; propAdded = true } else { propErr = fmt.Errorf("invalid uint8 '%s': %w", cleanVal, e) }
        case "visibility", "visible":
			 visBool := false; switch strings.ToLower(cleanVal) { case "true", "visible", "1": visBool = true; case "false", "hidden", "0": visBool = false; default: propErr = fmt.Errorf("invalid boolean '%s'", cleanVal) }; if propErr == nil { valByte := uint8(0); if visBool { valByte = 1 }; krbProp = &KrbProperty{PropIDVisibility, ValTypeByte, 1, []byte{valByte}}; propID = PropIDVisibility; propAdded = true }
		case "z_index":
             if z_uint, e_uint := strconv.ParseUint(cleanVal, 10, 16); e_uint == nil { buf := make([]byte, 2); binary.LittleEndian.PutUint16(buf, uint16(z_uint)); krbProp = &KrbProperty{PropIDZindex, ValTypeShort, 2, buf}; propID = PropIDZindex; propAdded = true } else { propErr = fmt.Errorf("invalid uint16 '%s': %w", cleanVal, e_uint) }
        case "transform":
			strIdx, err := state.addString(quotedVal); if err != nil { propErr = fmt.Errorf("add string: %w", err) } else { krbProp = &KrbProperty{PropIDTransform, ValTypeString, 1, []byte{strIdx}}; propID = PropIDTransform; propAdded = true }
		case "shadow":
            strIdx, err := state.addString(quotedVal); if err != nil { propErr = fmt.Errorf("add string: %w", err) } else { krbProp = &KrbProperty{PropIDShadow, ValTypeString, 1, []byte{strIdx}}; propID = PropIDShadow; propAdded = true }
        default:
			log.Printf("L%d: Warning: Unhandled property '%s' in style '%s'. Ignored.", lineNum, key, style.SourceName)
		} // End switch key

		if propErr != nil {
			return fmt.Errorf("L%d: error processing property '%s' in style '%s': %w", lineNum, key, style.SourceName, propErr)
		}

		// *** Merge the property: Overwrite if it exists, add if new ***
		if propAdded && propID != PropIDInvalid {
			if _, exists := mergedProps[propID]; exists {
				overrideCount++
			} else {
				addedCount++
			}
			mergedProps[propID] = *krbProp // Overwrite or add
		}
	} // End loop through source properties

	// --- 3. Finalize Properties and Size ---
	style.Properties = make([]KrbProperty, 0, len(mergedProps))
	finalSize := uint32(3) // StyleHeader: ID(1) + NameIdx(1) + PropCount(1)
	propIDs := make([]uint8, 0, len(mergedProps))
	for id := range mergedProps { propIDs = append(propIDs, id) }
	sort.Slice(propIDs, func(i, j int) bool { return propIDs[i] < propIDs[j] })

	for _, propID := range propIDs {
		prop := mergedProps[propID]
		style.Properties = append(style.Properties, prop)
		finalSize += 3 + uint32(prop.Size)
	}
	style.CalculatedSize = finalSize

	style.IsResolved = true
	// log.Printf("      Resolved style '%s': %d final props (%d added, %d overrides). Size: %d\n", style.SourceName, len(style.Properties), addedCount, overrideCount, style.CalculatedSize)

	return nil
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
