// utils.go
package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"strings"
	"unicode"
)

// --- String/Value Cleaning ---

// trimQuotes removes leading/trailing double quotes if present
func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func cleanAndQuoteValue(valStr string) (cleanedString string, wasQuoted bool) {
	trimmed := strings.TrimSpace(valStr) // Initial trim

	// Check for full-line comment first
	if strings.HasPrefix(trimmed, "#") {
		return "", false // Treat as empty if line starts with #
	}

	valuePart := trimmed // Work with the trimmed version
	commentIndex := -1
	inQuotes := false

	// Find the first '#' that signifies a comment (not inside quotes)
	// *after* the first character (to allow # for hex colors)
	for i, r := range valuePart {
		if r == '"' {
			inQuotes = !inQuotes
		}
		if r == '#' && !inQuotes && i > 0 { // Only consider '#' after index 0
			commentIndex = i
			break
		}
	}

	// If a comment was found, slice the string up to the comment start
	if commentIndex != -1 {
		valuePart = valuePart[:commentIndex]
	}

	// Trim trailing space AGAIN after potentially removing the comment
	finalTrimmed := strings.TrimRightFunc(valuePart, unicode.IsSpace)

	// Check if the result was originally quoted
	wasQuoted = len(finalTrimmed) >= 2 && finalTrimmed[0] == '"' && finalTrimmed[len(finalTrimmed)-1] == '"'

	// Remove outer quotes if they existed to get the final cleaned string
	if wasQuoted {
		cleanedString = finalTrimmed[1 : len(finalTrimmed)-1]
	} else {
		cleanedString = finalTrimmed
	}

	// Special check: If the result STILL starts with # but has internal spaces, it's likely bad
	// (e.g., "#FF00FF comment_without_leading_hash") - Treat as invalid to avoid issues.
	// This is a heuristic.
	if strings.HasPrefix(cleanedString, "#") && strings.Contains(cleanedString, " ") {
		log.Printf("Warning: Potential invalid value detected after cleaning '%s', resulting in '%s'. Treating as empty.", valStr, cleanedString)
		return "", wasQuoted // Return empty to cause parsing error downstream if needed
	}

	return // Use named return values
}

// --- Binary Writing Helpers ---

// Helper for writing binary data
func writeUint8(w io.Writer, value uint8) error {
	return binary.Write(w, binary.LittleEndian, value)
}

func writeUint16(w io.Writer, value uint16) error {
	return binary.Write(w, binary.LittleEndian, value)
}

func writeUint32(w io.Writer, value uint32) error {
	return binary.Write(w, binary.LittleEndian, value)
}

// --- Parsing Helpers ---

// parseColor converts "#RRGGBBAA" or "#RGB" etc. to [4]uint8 {R, G, B, A}
func parseColor(valueStr string) ([4]uint8, bool) {
	c := [4]uint8{0, 0, 0, 255}             // Default alpha to 255
	cleanVal := strings.TrimSpace(valueStr) // Trim space first

	if !strings.HasPrefix(cleanVal, "#") {
		return c, false
	}
	hexStr := cleanVal[1:]
	var r, g, b, a uint64
	var err error

	switch len(hexStr) {
	case 8: // RRGGBBAA
		_, err = fmt.Sscanf(hexStr, "%02x%02x%02x%02x", &r, &g, &b, &a)
		if err == nil {
			c[0], c[1], c[2], c[3] = uint8(r), uint8(g), uint8(b), uint8(a)
			return c, true
		}
	case 6: // RRGGBB
		_, err = fmt.Sscanf(hexStr, "%02x%02x%02x", &r, &g, &b)
		if err == nil {
			c[0], c[1], c[2] = uint8(r), uint8(g), uint8(b)
			return c, true
		}
	case 4: // RGBA shorthand
		_, err = fmt.Sscanf(hexStr, "%1x%1x%1x%1x", &r, &g, &b, &a)
		if err == nil {
			c[0], c[1], c[2], c[3] = uint8(r*16+r), uint8(g*16+g), uint8(b*16+b), uint8(a*16+a)
			return c, true
		}
	case 3: // RGB shorthand
		_, err = fmt.Sscanf(hexStr, "%1x%1x%1x", &r, &g, &b)
		if err == nil {
			c[0], c[1], c[2] = uint8(r*16+r), uint8(g*16+g), uint8(b*16+b)
			return c, true
		}
	}

	log.Printf("Warning: Invalid color format '%s', Error: %v\n", valueStr, err)
	return c, false // Return default black on error
}

// guessResourceType provides a basic guess for resource type based on common keywords in the property key.
func guessResourceType(key string) uint8 {
	lowerKey := strings.ToLower(key)
	if strings.Contains(lowerKey, "image") || strings.Contains(lowerKey, "icon") || strings.Contains(lowerKey, "sprite") || strings.Contains(lowerKey, "texture") || strings.Contains(lowerKey, "background") || strings.Contains(lowerKey, "logo") || strings.Contains(lowerKey, "avatar") {
		return ResTypeImage
	}
	if strings.Contains(lowerKey, "font") {
		return ResTypeFont
	}
	if strings.Contains(lowerKey, "sound") || strings.Contains(lowerKey, "audio") || strings.Contains(lowerKey, "music") {
		return ResTypeSound
	}
	// Default guess if no strong hints found
	log.Printf("Debug: Could not guess resource type for key '%s', defaulting to Image.", key)
	return ResTypeImage
}

// getElementTypeFromName maps standard KRY element names to KRB type IDs
func getElementTypeFromName(name string) uint8 {
	switch name {
	case "App":
		return ElemTypeApp
	case "Container":
		return ElemTypeContainer
	case "Text":
		return ElemTypeText
	case "Image":
		return ElemTypeImage
	case "Canvas":
		return ElemTypeCanvas
	case "Button":
		return ElemTypeButton
	case "Input":
		return ElemTypeInput
	case "List":
		return ElemTypeList
	case "Grid":
		return ElemTypeGrid
	case "Scrollable":
		return ElemTypeScrollable
	case "Video":
		return ElemTypeVideo
	default:
		// It might be a component name or a truly unknown type.
		// The parser handles component lookup; resolver handles unknown standard types.
		return ElemTypeUnknown
	}
}

// parseLayoutString converts a space-separated layout string (e.g., "row center wrap grow")
// into the final KRB Layout byte.
func parseLayoutString(layoutStr string) uint8 {
	var b uint8 = 0 // Start with 0
	parts := strings.Fields(layoutStr)
	hasExplicitDirection := false
	hasExplicitAlignment := false

	// Determine if explicit direction/alignment are set
	for _, part := range parts {
		switch part {
		case "row", "col", "column", "row_rev", "row-rev", "col_rev", "col-rev", "column-rev":
			hasExplicitDirection = true
		case "start", "center", "centre", "end", "space_between", "space-between":
			hasExplicitAlignment = true
		}
	}

	// Apply Direction (default to Column)
	if !hasExplicitDirection {
		b |= LayoutDirectionColumn
	}
	for _, part := range parts { // Last one specified wins
		switch part {
		case "row":
			b = (b &^ LayoutDirectionMask) | LayoutDirectionRow
		case "col", "column":
			b = (b &^ LayoutDirectionMask) | LayoutDirectionColumn
		case "row_rev", "row-rev":
			b = (b &^ LayoutDirectionMask) | LayoutDirectionRowRev
		case "col_rev", "col-rev", "column-rev":
			b = (b &^ LayoutDirectionMask) | LayoutDirectionColRev
		}
	}

	// Apply Alignment (default to Start)
	if !hasExplicitAlignment {
		b |= LayoutAlignmentStart
	}
	for _, part := range parts { // Last one specified wins
		switch part {
		case "start":
			b = (b &^ LayoutAlignmentMask) | LayoutAlignmentStart
		case "center", "centre":
			b = (b &^ LayoutAlignmentMask) | LayoutAlignmentCenter
		case "end":
			b = (b &^ LayoutAlignmentMask) | LayoutAlignmentEnd
		case "space_between", "space-between":
			b = (b &^ LayoutAlignmentMask) | LayoutAlignmentSpaceBtn
		}
	}

	// Apply Flags
	for _, part := range parts {
		switch part {
		case "wrap":
			b |= LayoutWrapBit
		case "grow":
			b |= LayoutGrowBit
		case "absolute":
			b |= LayoutAbsoluteBit
		}
	}
	return b
}

// --- Misc Helpers ---

// min helper for integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Helper to check if SourceProperties already contains a specific key
func (style *StyleEntry) SourcePropertiesContainsKey(key string) bool {
	for _, sp := range style.SourceProperties {
		if sp.Key == key {
			return true
		}
	}
	return false
}

// addKrbProperty adds a resolved KRB property to an element.
func (el *Element) addKrbProperty(propID, valType uint8, data []byte) error {
	if len(el.KrbProperties) >= MaxProperties {
		return fmt.Errorf("L%d: maximum KRB properties (%d) exceeded for element '%s'", el.SourceLineNum, MaxProperties, el.SourceElementName)
	}
	if len(data) > 255 { // KRB Property.Size is 1 byte
		return fmt.Errorf("L%d: property data size (%d) exceeds maximum (255) for element '%s', prop ID 0x%X", el.SourceLineNum, len(data), el.SourceElementName, propID)
	}
	prop := KrbProperty{PropertyID: propID, ValueType: valType, Size: uint8(len(data)), Value: data}
	el.KrbProperties = append(el.KrbProperties, prop)
	return nil
}

// addKrbStringProperty adds a string property (looks up/adds string, stores index).
// This should be a method of CompilerState because it needs to access state.addString
func (state *CompilerState) addKrbStringProperty(el *Element, propID uint8, valueStr string) error {
	idx, err := state.addString(valueStr) // addString is a method of *CompilerState
	if err != nil {
		return fmt.Errorf("L%d: failed adding string for property 0x%X ('%s'): %w", el.SourceLineNum, propID, valueStr, err)
	}
	return el.addKrbProperty(propID, ValTypeString, []byte{idx}) // Calls Element's method
}

// addKrbResourceProperty adds a resource property (looks up/adds resource, stores index).
// This should be a method of CompilerState
func (state *CompilerState) addKrbResourceProperty(el *Element, propID, resType uint8, pathStr string) error {
	idx, err := state.addResource(resType, pathStr) // addResource is a method of *CompilerState
	if err != nil {
		return fmt.Errorf("L%d: failed adding resource for property 0x%X ('%s'): %w", el.SourceLineNum, propID, pathStr, err)
	}
	return el.addKrbProperty(propID, ValTypeResource, []byte{idx}) // Calls Element's method
}
