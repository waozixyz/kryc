// writer.go
package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"sort"
	// "strings" // Not needed directly here anymore
)

// --- Pass 2: Calculate Offsets & Sizes ---
// This function calculates the final size of each section and element,
// including the space needed for standard and custom properties, events, and children.
// It determines the offsets needed for the KRB header.
func (state *CompilerState) calculateOffsetsAndSizes() error {
	log.Println("Pass 2: Calculating final offsets and sizes...")
	currentOffset := uint32(KRBHeaderSize) // Start after the main file header

	// --- Elements Section Size ---
	state.ElementOffset = currentOffset // Mark the start of the element data
	for i := range state.Elements {
		el := &state.Elements[i] // Get a pointer to modify the element
		el.AbsoluteOffset = currentOffset // Store the calculated start offset for this element

		// Calculate the total size of this element's block in the KRB file
		size := uint32(KRBElementHeaderSize) // Start with the fixed header size (17 bytes for v0.3)

		// Add size for standard properties section
		for _, prop := range el.KrbProperties {
			size += 3 + uint32(prop.Size) // Property Header (3 bytes) + Value Data (Size bytes)
		}

		// Add size for CUSTOM properties section (KRB v0.3)
		for _, customProp := range el.KrbCustomProperties {
			size += 1                      // Key Index (1 byte)
			size += 1                      // Value Type (1 byte)
			size += 1                      // Value Size (1 byte)
			size += uint32(customProp.ValueSize) // Value Data (ValueSize bytes)
		}

		// Add size for events section
		size += uint32(len(el.KrbEvents)) * 2 // Event Type (1 byte) + Callback ID (1 byte) per event

		// Add size for animation refs section (currently always 0)
		// size += uint32(el.AnimationCount) * 2 // Animation Index (1 byte) + Trigger (1 byte)

		// Add size for child relative offsets section
		// Note: el.Children is populated by the resolver pass
		size += uint32(len(el.Children)) * 2 // Relative Offset (2 bytes) per child

		// Store the calculated size and check for errors/overflows
		el.CalculatedSize = size
		if size < KRBElementHeaderSize {
			// This indicates a calculation error
			return fmt.Errorf("internal error: calculated size %d for element %d ('%s') is less than header size %d", size, i, el.SourceElementName, KRBElementHeaderSize)
		}
		if size > math.MaxUint32 { // Extremely unlikely, but good practice
			return fmt.Errorf("internal error: calculated size overflow for element %d ('%s')", i, el.SourceElementName)
		}

		// Move the current offset pointer past this element's block
		currentOffset += size
	} // End loop through elements

	// --- Styles Section Size ---
	state.StyleOffset = currentOffset // Mark the start of style data
	for i := range state.Styles {
		style := &state.Styles[i]
		// Calculate style block size: Header(3 bytes) + Properties
		size := uint32(3) // Style Header: ID(1) + NameIdx(1) + PropCount(1)
		for _, prop := range style.Properties {
			size += 3 + uint32(prop.Size) // Property Header(3) + Value Data(Size)
		}
		style.CalculatedSize = size
		if size < 3 { // Basic sanity check
			return fmt.Errorf("internal error: calculated size %d for style %d ('%s') is less than minimum size 3", size, i, style.SourceName)
		}
		if size > math.MaxUint32 { // Check for overflow
			return fmt.Errorf("internal error: calculated size overflow for style %d ('%s')", i, style.SourceName)
		}
		currentOffset += size
	} // End loop through styles

	// --- Animations Section Size ---
	state.AnimOffset = currentOffset // Mark start of animation data (currently empty)
	// currentOffset += calculatedAnimationSize // No animations implemented

	// --- Strings Section Size ---
	state.StringOffset = currentOffset       // Mark start of string data
	stringSectionSize := uint32(2)         // Starts with the String Count (uint16)
	if len(state.Strings) > 0 {
		for _, s := range state.Strings {
			// KRB string format: LengthPrefix(1 byte) + UTF-8 Data(Length bytes)
			if s.Length > 255 { // Validate length fits in the 1-byte prefix
				return fmt.Errorf("string '%s...' (index %d) length %d exceeds max (255)", s.Text[:min(len(s.Text), 20)], s.Index, s.Length)
			}
			stringSectionSize += 1 + uint32(s.Length)
		}
	}
	currentOffset += stringSectionSize // Add the total size of the string section

	// --- Resources Section Size ---
	state.ResourceOffset = currentOffset     // Mark start of resource data
	resourceSectionSize := uint32(2)       // Starts with the Resource Count (uint16)
	if len(state.Resources) > 0 {
		for i := range state.Resources {
			res := &state.Resources[i]
			resSize := uint32(0)
			// Currently only external format is supported
			if res.Format == ResFormatExternal {
				// Format: Type(1) + NameIdx(1) + Format(1) + DataStringIndex(1)
				resSize = 4
			} else {
				return fmt.Errorf("unsupported resource format %d for resource %d", res.Format, i)
			}
			res.CalculatedSize = resSize // Store calculated size for the resource entry
			resourceSectionSize += resSize
		}
	}
	currentOffset += resourceSectionSize // Add the total size of the resource section

	// --- Set Final Total Size ---
	state.TotalSize = currentOffset
	log.Printf("      Total calculated size: %d bytes\n", state.TotalSize)
	return nil // Offset calculation successful
}


// --- Pass 3: Write KRB File ---
// This function writes the entire compiled state into the final KRB binary file.
func (state *CompilerState) writeKrbFile(filePath string) error {
	log.Printf("Pass 3: Writing KRB v%d.%d binary to '%s'...\n", KRBVersionMajor, KRBVersionMinor, filePath)

	// Create or truncate the output file
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create output file '%s': %w", filePath, err)
	}
	defer file.Close() // Ensure file is closed even if errors occur

	// Use a buffered writer for better performance
	writer := bufio.NewWriter(file)

	// --- Write KRB File Header (42 bytes) ---
	if _, err := writer.WriteString(KRBMagic); err != nil { return fmt.Errorf("write magic: %w", err) }
	versionField := (uint16(KRBVersionMinor) << 8) | uint16(KRBVersionMajor)
	if err := writeUint16(writer, versionField); err != nil { return fmt.Errorf("write version: %w", err) }
	if err := writeUint16(writer, state.HeaderFlags); err != nil { return fmt.Errorf("write flags: %w", err) }

	// Write counts (ensure they fit in uint16)
	if len(state.Elements) > math.MaxUint16 { return fmt.Errorf("element count %d exceeds uint16 max", len(state.Elements)) }
	if len(state.Styles) > math.MaxUint16 { return fmt.Errorf("style count %d exceeds uint16 max", len(state.Styles)) }
	if len(state.Strings) > math.MaxUint16 { return fmt.Errorf("string count %d exceeds uint16 max", len(state.Strings)) }
	if len(state.Resources) > math.MaxUint16 { return fmt.Errorf("resource count %d exceeds uint16 max", len(state.Resources)) }
	// Write actual counts
	if err := writeUint16(writer, uint16(len(state.Elements))); err != nil { return fmt.Errorf("write elem count: %w", err) }
	if err := writeUint16(writer, uint16(len(state.Styles))); err != nil { return fmt.Errorf("write style count: %w", err) }
	if err := writeUint16(writer, uint16(0)); err != nil { return fmt.Errorf("write anim count (0): %w", err) } // No animations implemented
	if err := writeUint16(writer, uint16(len(state.Strings))); err != nil { return fmt.Errorf("write string count: %w", err) }
	if err := writeUint16(writer, uint16(len(state.Resources))); err != nil { return fmt.Errorf("write resource count: %w", err) }

	// Write section offsets (calculated in Pass 2)
	if err := writeUint32(writer, state.ElementOffset); err != nil { return fmt.Errorf("write elem offset: %w", err) }
	if err := writeUint32(writer, state.StyleOffset); err != nil { return fmt.Errorf("write style offset: %w", err) }
	if err := writeUint32(writer, state.AnimOffset); err != nil { return fmt.Errorf("write anim offset: %w", err) }
	if err := writeUint32(writer, state.StringOffset); err != nil { return fmt.Errorf("write string offset: %w", err) }
	if err := writeUint32(writer, state.ResourceOffset); err != nil { return fmt.Errorf("write resource offset: %w", err) }
	// Write total file size
	if err := writeUint32(writer, state.TotalSize); err != nil { return fmt.Errorf("write total size: %w", err) }

	// --- Pad to Element Offset if necessary ---
	if err := writer.Flush(); err != nil { return fmt.Errorf("flush after header write: %w", err) }
	currentPos, err := file.Seek(0, io.SeekCurrent) // Get current file position (use int64)
	if err != nil { return fmt.Errorf("seek after header flush: %w", err) }
	padding := int64(state.ElementOffset) - currentPos
	if padding < 0 {
		return fmt.Errorf("header size/position mismatch: negative padding %d needed (pos %d, expected elem offset %d)", padding, currentPos, state.ElementOffset)
	}
	if padding > 0 {
		log.Printf("    Padding header with %d zero bytes\n", padding)
		padBytes := make([]byte, padding) // Allocate padding buffer
		n, writeErr := writer.Write(padBytes)
		if writeErr != nil { return fmt.Errorf("write header padding: %w", writeErr) }
		if int64(n) != padding { return fmt.Errorf("wrote %d padding bytes, expected %d", n, padding) }
		// Flush padding and update position
		if err := writer.Flush(); err != nil { return fmt.Errorf("flush after header padding: %w", err) }
		currentPos += padding // Update tracked position
	}
	// Verify position before writing elements
	if uint32(currentPos) != state.ElementOffset {
		actualPos, seekErr := file.Seek(0, io.SeekCurrent)
		if seekErr != nil { return fmt.Errorf("seek check post-padding: %w", seekErr)}
		return fmt.Errorf("file position mismatch before elements: tracked pos %d (actual %d) != expected ElementOffset %d", currentPos, actualPos, state.ElementOffset)
	}

	// --- Write Element Blocks ---
	log.Printf("    Writing %d elements starting at offset %d\n", len(state.Elements), state.ElementOffset)
	for i := range state.Elements {
		el := &state.Elements[i]
		startPos := currentPos // Record position before writing this element for size check

		// Verify element offset calculation from Pass 2
		if uint32(currentPos) != el.AbsoluteOffset {
			actualPos, _ := file.Seek(0, io.SeekCurrent)
			return fmt.Errorf("element %d ('%s') offset mismatch: tracked pos %d (actual %d) != expected absolute offset %d", i, el.SourceElementName, currentPos, actualPos, el.AbsoluteOffset)
		}

		// --- Write Element Header (17 bytes) ---
		// Recalculate counts just before writing
		el.PropertyCount = uint8(len(el.KrbProperties))
		el.CustomPropCount = uint8(len(el.KrbCustomProperties))
		el.EventCount = uint8(len(el.KrbEvents))
		el.ChildCount = uint8(len(el.Children))
		el.AnimationCount = 0 // Still 0

		// Write header fields (error checking omitted for brevity, add back if needed)
		_ = writeUint8(writer, el.Type)
		_ = writeUint8(writer, el.IDStringIndex)
		_ = writeUint16(writer, el.PosX)
		_ = writeUint16(writer, el.PosY)
		_ = writeUint16(writer, el.Width)
		_ = writeUint16(writer, el.Height)
		_ = writeUint8(writer, el.Layout)
		_ = writeUint8(writer, el.StyleID)
		_ = writeUint8(writer, el.PropertyCount)
		_ = writeUint8(writer, el.ChildCount)
		_ = writeUint8(writer, el.EventCount)
		_ = writeUint8(writer, el.AnimationCount)
		_ = writeUint8(writer, el.CustomPropCount) // Write the count

		// --- Write Standard Properties Section ---
		for j, prop := range el.KrbProperties {
			_ = writeUint8(writer, prop.PropertyID)
			_ = writeUint8(writer, prop.ValueType)
			_ = writeUint8(writer, prop.Size)
			if prop.Size > 0 {
				if prop.Value == nil { return fmt.Errorf("E%d StdP%d nil value size %d", i, j, prop.Size) }
				n, writeErr := writer.Write(prop.Value)
				if writeErr != nil { return fmt.Errorf("E%d StdP%d write val: %w", i, j, writeErr) }
				if n != int(prop.Size) { return fmt.Errorf("E%d StdP%d short write: wrote %d, expected %d", i, j, n, prop.Size) }
			}
		}

		// --- Write Custom Properties Section (KRB v0.3) ---
		for j, customProp := range el.KrbCustomProperties {
			_ = writeUint8(writer, customProp.KeyIndex)
			_ = writeUint8(writer, customProp.ValueType)
			_ = writeUint8(writer, customProp.ValueSize)
			if customProp.ValueSize > 0 {
				if customProp.Value == nil { return fmt.Errorf("E%d CstP%d nil value size %d", i, j, customProp.ValueSize) }
				n, writeErr := writer.Write(customProp.Value)
				if writeErr != nil { return fmt.Errorf("E%d CstP%d write val: %w", i, j, writeErr) }
				if n != int(customProp.ValueSize) { return fmt.Errorf("E%d CstP%d short write: wrote %d, expected %d", i, j, n, customProp.ValueSize) }
			}
		}

		// --- Write Events Section ---
		for _, event := range el.KrbEvents {
			_ = writeUint8(writer, event.EventType)
			_ = writeUint8(writer, event.CallbackID)
		}

		// --- Write Child Relative Offsets Section ---
		if len(el.Children) > 0 {
			// Apply simple child reordering based on PositionHint
			orderedChildren := make([]*Element, len(el.Children))
			copy(orderedChildren, el.Children)
			sort.SliceStable(orderedChildren, func(i, j int) bool {
				getSortOrder := func(hint string) int { switch hint { case "top", "left": return 1; case "bottom", "right": return 2; default: return 0 } }; return getSortOrder(orderedChildren[i].PositionHint) < getSortOrder(orderedChildren[j].PositionHint)
			})

			// Write the relative offsets using the reordered list
			for j, child := range orderedChildren {
				if child == nil { return fmt.Errorf("E%d C%d has nil child pointer after reorder", i, j) }
				relativeOffset := int64(child.AbsoluteOffset) - int64(el.AbsoluteOffset)
				if relativeOffset <= 0 && (el.AbsoluteOffset != 0 || child.AbsoluteOffset != 0) {
					return fmt.Errorf("E%d C%d ('%s' -> '%s') has invalid non-positive relative offset %d (child_abs=%d, parent_abs=%d)", i, j, el.SourceElementName, child.SourceElementName, relativeOffset, child.AbsoluteOffset, el.AbsoluteOffset)
				}
				if relativeOffset > math.MaxUint16 {
					return fmt.Errorf("E%d C%d ('%s' -> '%s') relative offset %d exceeds uint16 max", i, j, el.SourceElementName, child.SourceElementName, relativeOffset)
				}
				if err := writeUint16(writer, uint16(relativeOffset)); err != nil {
					return fmt.Errorf("E%d C%d write child relative offset %d: %w", i, j, relativeOffset, err)
				}
			}
		} // End child writing

		// --- Verify Size for this Element ---
		if err := writer.Flush(); err != nil { return fmt.Errorf("E%d post-write flush error: %w", i, err) }
		endPos, err := file.Seek(0, io.SeekCurrent) // Get position after writing element
		if err != nil { return fmt.Errorf("E%d post-write seek error: %w", i, err) }
		bytesWrittenForElement := uint32(endPos - startPos)
		if bytesWrittenForElement != el.CalculatedSize {
			return fmt.Errorf("element %d ('%s') size mismatch: wrote %d bytes, but calculated size was %d bytes", i, el.SourceElementName, bytesWrittenForElement, el.CalculatedSize)
		}
		currentPos = endPos // Update tracked position *after* writing and flushing

	} // --- End loop through elements ---

	// --- Write Style Blocks ---
	log.Printf("    Writing %d styles at offset %d\n", len(state.Styles), state.StyleOffset)
	if len(state.Styles) > 0 {
		if uint32(currentPos) != state.StyleOffset { actualPos, _ := file.Seek(0, io.SeekCurrent); return fmt.Errorf("style section offset mismatch: tracked pos %d (actual %d) != expected StyleOffset %d", currentPos, actualPos, state.StyleOffset) }
		for i := range state.Styles {
			style := &state.Styles[i]; startPos := currentPos
			// Write Style Header & Properties
			_ = writeUint8(writer, style.ID)
			_ = writeUint8(writer, style.NameIndex)
			_ = writeUint8(writer, uint8(len(style.Properties)))
			for j, prop := range style.Properties {
				_ = writeUint8(writer, prop.PropertyID)
				_ = writeUint8(writer, prop.ValueType)
				_ = writeUint8(writer, prop.Size)
				if prop.Size > 0 {
					if prop.Value == nil { return fmt.Errorf("S%d P%d nil value sz %d", i, j, prop.Size) }
					if n, err := writer.Write(prop.Value); err != nil || n != int(prop.Size) { return fmt.Errorf("S%d P%d write val sz %d: wrote %d, err %w", i, j, prop.Size, n, err) }
				}
			}
			// Verify size for this style
			if err := writer.Flush(); err != nil { return fmt.Errorf("S%d post-write flush error: %w", i, err) }
			endPos, _ := file.Seek(0, io.SeekCurrent); bytesWrittenForStyle := uint32(endPos - startPos)
			if bytesWrittenForStyle != style.CalculatedSize { return fmt.Errorf("style %d ('%s') size mismatch: wrote %d bytes, calculated %d bytes", i, style.SourceName, bytesWrittenForStyle, style.CalculatedSize) }
			currentPos = endPos
		}
	}

	// --- Write Animation Table Section --- (Skip)
	log.Printf("    Writing 0 animations at offset %d\n", state.AnimOffset)
	if uint32(currentPos) != state.AnimOffset { actualPos, _ := file.Seek(0, io.SeekCurrent); return fmt.Errorf("animation section offset mismatch: tracked pos %d (actual %d) != expected AnimOffset %d", currentPos, actualPos, state.AnimOffset) }
	// No data

	// --- Write String Table Section ---
	log.Printf("    Writing %d strings at offset %d\n", len(state.Strings), state.StringOffset)
	if uint32(currentPos) != state.StringOffset { actualPos, _ := file.Seek(0, io.SeekCurrent); return fmt.Errorf("string section offset mismatch: tracked pos %d (actual %d) != expected StringOffset %d", currentPos, actualPos, state.StringOffset) }
	// Write String Count
	if err := writeUint16(writer, uint16(len(state.Strings))); err != nil { return fmt.Errorf("write string count value: %w", err) }
	currentPos += 2 // Account for count field size
	// Write each string (Length Prefix + Data)
	if len(state.Strings) > 0 {
		for i := range state.Strings {
			s := &state.Strings[i]
			if s.Length > 255 { return fmt.Errorf("Str%d length %d > 255", i, s.Length) }
			// Write 1-byte length prefix
			if err := writeUint8(writer, uint8(s.Length)); err != nil { return fmt.Errorf("Str%d write len %d: %w", i, s.Length, err) }
			currentPos += 1 // Add size of length prefix
			// Write UTF-8 string data if length > 0
			if s.Length > 0 {
				n, writeErr := writer.WriteString(s.Text)
				if writeErr != nil { return fmt.Errorf("Str%d write text (len %d): err %w", i, s.Length, writeErr) }
				if n != s.Length { return fmt.Errorf("Str%d short write text (len %d): wrote %d", i, s.Length, n) }
				// *** Corrected Type Cast for position update ***
				currentPos += int64(s.Length)
			}
		}
		// Flush buffer after writing all strings
		if err := writer.Flush(); err != nil { return fmt.Errorf("flush after writing strings: %w", err) }
	}

	// --- Write Resource Table Section ---
	log.Printf("    Writing %d resources at offset %d\n", len(state.Resources), state.ResourceOffset)
	if uint32(currentPos) != state.ResourceOffset { actualPos, _ := file.Seek(0, io.SeekCurrent); return fmt.Errorf("resource section offset mismatch: tracked pos %d (actual %d) != expected ResourceOffset %d", currentPos, actualPos, state.ResourceOffset) }
	// Write Resource Count
	if err := writeUint16(writer, uint16(len(state.Resources))); err != nil { return fmt.Errorf("write resource count value: %w", err) }
	currentPos += 2 // Account for count field size
	// Write each resource entry
	if len(state.Resources) > 0 {
		for i := range state.Resources {
			r := &state.Resources[i]
			// Write Type, NameIndex, Format
			_ = writeUint8(writer, r.Type)
			_ = writeUint8(writer, r.NameIndex)
			_ = writeUint8(writer, r.Format)
			// Write format-specific data
			if r.Format == ResFormatExternal {
				_ = writeUint8(writer, r.DataStringIndex)
			} else {
				return fmt.Errorf("unsupported resource format %d", r.Format)
			}
			// *** Corrected Type Cast for position update ***
			currentPos += int64(r.CalculatedSize) // External is fixed size (e.g., 4)
		}
		// Flush buffer after writing all resources
		if err := writer.Flush(); err != nil { return fmt.Errorf("flush after writing resources: %w", err) }
	}

	// --- Final Flush and Size Verification ---
	if err := writer.Flush(); err != nil { return fmt.Errorf("final flush error: %w", err) }
	finalFileSize, err := file.Seek(0, io.SeekCurrent); if err != nil { return fmt.Errorf("get final size: %w", err) }
	if uint32(finalFileSize) != state.TotalSize { return fmt.Errorf("final write size mismatch: actual file size %d != calculated total size %d", finalFileSize, state.TotalSize) }
	// Use int64 for comparison here as currentPos is int64
	if currentPos != int64(state.TotalSize) { return fmt.Errorf("final tracked position mismatch: tracked pos %d != calculated total size %d", currentPos, state.TotalSize) }

	log.Printf("   Successfully wrote %d bytes.\n", finalFileSize)
	return nil // Success!
}
