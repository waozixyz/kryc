// utils.go
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"strings"
	"io"
)

// --- String/Value Cleaning ---

// trimQuotes removes leading/trailing double quotes if present
func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// cleanAndQuoteValue removes comments, trims space, and returns both the fully cleaned value
// (for parsing numbers, bools etc.) and a version with only outer quotes trimmed (for string table lookups).
func cleanAndQuoteValue(valStr string) (cleanVal, quotedVal string) {
	valuePart := valStr
	// Trim initial whitespace before checking for comments
	trimmedValueBeforeCommentCheck := strings.TrimSpace(valuePart)

	commentIndex := -1
	inQuotes := false
	// Scan for '#' that marks a comment, ignoring # inside quotes or at the start (hex color)
	for i, r := range trimmedValueBeforeCommentCheck {
		if r == '"' {
			// Basic quote toggling, doesn't handle escaped quotes
			inQuotes = !inQuotes
		}
		// '#' is a comment if not inside quotes and not the very first character
		if r == '#' && !inQuotes && i > 0 {
			commentIndex = i
			break
		}
	}

	// Slice the string if a valid comment marker was found
	if commentIndex > 0 {
		valuePart = trimmedValueBeforeCommentCheck[:commentIndex]
	} else {
		// Use the already trimmed string if no comment found (or comment was invalid)
		valuePart = trimmedValueBeforeCommentCheck
	}

	// Final trim after potentially removing the comment part
	cleanVal = strings.TrimSpace(valuePart)
	// Separately trim just the outer quotes from the cleaned value for string lookup
	quotedVal = trimQuotes(cleanVal)
	return
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
	c := [4]uint8{0, 0, 0, 255} // Default alpha to 255
	cleanVal := strings.TrimSpace(valueStr) // Trim space first

	if !strings.HasPrefix(cleanVal, "#") {
		// Allow named colors in the future? For now, require #
		// log.Printf("Warning: Invalid color format (missing #): '%s'\n", valueStr)
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

func guessResourceType(key string) uint8 {
	lowerKey := strings.ToLower(key)
	if strings.Contains(lowerKey, "image") || strings.Contains(lowerKey, "icon") || strings.Contains(lowerKey, "sprite") || strings.Contains(lowerKey, "texture") || strings.Contains(lowerKey, "background") || strings.Contains(lowerKey, "logo") || strings.Contains(lowerKey, "avatar") {
		return ResTypeImage
	}
	if strings.Contains(lowerKey, "font") {
		return ResTypeFont // Assuming ResTypeFont is defined in constants.go
	}
	if strings.Contains(lowerKey, "sound") || strings.Contains(lowerKey, "audio") || strings.Contains(lowerKey, "music") {
		return ResTypeSound // Assuming ResTypeSound is defined in constants.go
	}
	// Add other guesses if needed based on common key names
	// Default guess if no strong hints found - Image is often a safe default.
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

	// Pass 1: Check for explicit direction and alignment
	for _, part := range parts {
		switch part {
		case "row", "col", "column", "row_rev", "row-rev", "col_rev", "col-rev", "column-rev":
			hasExplicitDirection = true
		case "start", "center", "centre", "end", "space_between", "space-between":
			hasExplicitAlignment = true
		}
	}

	// Apply Direction (last one wins if multiple specified)
	if hasExplicitDirection {
		for _, part := range parts {
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
	} else {
		b |= LayoutDirectionColumn // Default to Column if no direction specified
	}

	// Apply Alignment (last one wins)
	if hasExplicitAlignment {
		for _, part := range parts {
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
	} else {
		b |= LayoutAlignmentStart // Default to Start if no alignment specified
	}

	// Apply Flags (OR them in)
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