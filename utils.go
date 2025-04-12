package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"strings"
)

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

// trimQuotes removes leading/trailing double quotes if present
func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// parseColor converts "#RRGGBBAA" or "#RGB" etc. to [4]uint8 {R, G, B, A}
func parseColor(valueStr string) ([4]uint8, bool) {
	c := [4]uint8{0, 0, 0, 255} // Default alpha to 255
	if !strings.HasPrefix(valueStr, "#") {
		log.Printf("Warning: Invalid color format (missing #): '%s'\n", valueStr)
		return c, false
	}
	hexStr := valueStr[1:]
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
	case 4: // RGBA
		_, err = fmt.Sscanf(hexStr, "%1x%1x%1x%1x", &r, &g, &b, &a)
		if err == nil {
			c[0], c[1], c[2], c[3] = uint8(r*16+r), uint8(g*16+g), uint8(b*16+b), uint8(a*16+a)
			return c, true
		}
	case 3: // RGB
		_, err = fmt.Sscanf(hexStr, "%1x%1x%1x", &r, &g, &b)
		if err == nil {
			c[0], c[1], c[2] = uint8(r*16+r), uint8(g*16+g), uint8(b*16+b)
			return c, true
		}
	}

	log.Printf("Warning: Invalid color format '%s', Error: %v\n", valueStr, err)
	return c, false
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
		return ElemTypeUnknown // Indicates potential custom component or unknown type
	}
}
func parseLayoutString(layoutStr string) uint8 {
	var b uint8 = 0
	parts := strings.Fields(layoutStr) // Split by whitespace
	hasExplicitDirection := false

	// Pass 1: Check for explicit direction
	for _, part := range parts {
		switch part {
		case "row", "col", "column", "row_rev", "row-rev", "col_rev", "col-rev", "column-rev":
			hasExplicitDirection = true
			break // Found one, no need to check further in this pass
		}
	}

	// Apply Direction (or default to COLUMN if no explicit direction found)
	dirSet := false
	if hasExplicitDirection {
		for _, part := range parts { // Find the last specified direction
			switch part {
			case "row":				b = (b &^ LayoutDirectionMask) | LayoutDirectionRow; dirSet = true
			case "col", "column": 	b = (b &^ LayoutDirectionMask) | LayoutDirectionColumn; dirSet = true
			case "row_rev", "row-rev": b = (b &^ LayoutDirectionMask) | LayoutDirectionRowRev; dirSet = true
			case "col_rev", "col-rev", "column-rev": b = (b &^ LayoutDirectionMask) | LayoutDirectionColRev; dirSet = true
			}
		}
	}
	if !dirSet { // If no explicit direction was found anywhere
		b |= LayoutDirectionColumn // <<< DEFAULT TO COLUMN
	}

	// Apply Alignment (last one wins)
	alignSet := false
	for _, part := range parts {
		switch part {
		case "start":			b = (b &^ LayoutAlignmentMask) | LayoutAlignmentStart; alignSet = true
		case "center", "centre": b = (b &^ LayoutAlignmentMask) | LayoutAlignmentCenter; alignSet = true
		case "end": 			b = (b &^ LayoutAlignmentMask) | LayoutAlignmentEnd; alignSet = true
		case "space_between", "space-between": b = (b &^ LayoutAlignmentMask) | LayoutAlignmentSpaceBtn; alignSet = true
		}
	}
	if !alignSet {
		b |= LayoutAlignmentStart // Default to start if not specified
	}

	// Apply Flags
	for _, part := range parts {
		switch part {
		case "wrap":		b |= LayoutWrapBit
		case "grow":		b |= LayoutGrowBit
		case "absolute":	b |= LayoutAbsoluteBit
		}
	}
	return b
}

func (style *StyleEntry) addSourceProperty(key, value string, lineNum int) error {
	// Check if extends is specified multiple times or after other properties (optional strictness)
	if key == "extends" {
		if style.ExtendsStyleName != "" {
			return fmt.Errorf("style '%s' specifies 'extends' multiple times", style.SourceName)
		}
		// Optional: Check if it's the first property
		// if len(style.SourceProperties) > 0 {
		// 	 log.Printf("L%d: Warning: 'extends' should ideally be the first property in style '%s'\n", lineNum, style.SourceName)
		// }
	} else if style.ExtendsStyleName != "" && len(style.SourceProperties) == 0 {
		// If extends was set, but this is the first non-extends property
		// This helps enforce the "extends first" convention slightly
	}

	// Overwrite if key exists? Or just append? Let's append for now.
	// Resolution pass will handle overrides.
	if len(style.SourceProperties) >= MaxProperties { // Use MaxProperties as a general limit
		return fmt.Errorf("L%d: maximum source properties (%d) exceeded for style '%s'", lineNum, MaxProperties, style.SourceName)
	}
	prop := SourceProperty{Key: key, ValueStr: value, LineNum: lineNum}
	style.SourceProperties = append(style.SourceProperties, prop)
	return nil
}
