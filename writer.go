// writer.go
package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"math"
	"os"
)

// --- Pass 2: Calculate Offsets & Sizes ---
func (state *CompilerState) calculateOffsetsAndSizes() error {
	log.Println("Pass 2: Calculating final offsets and sizes...")
	currentOffset := uint32(KRBHeaderSize)

	// --- Elements ---
	state.ElementOffset = currentOffset
	for i := range state.Elements {
		el := &state.Elements[i] // Use pointer to modify original
		el.AbsoluteOffset = currentOffset

		// Recalculate size based on FINAL counts and data
		size := uint32(KRBElementHeaderSize) // Base header size
		for _, prop := range el.KrbProperties {
			size += 3 + uint32(prop.Size) // ID(1) + Type(1) + Size(1) + Data(Size)
		}
		size += uint32(len(el.KrbEvents)) * 2 // Type(1) + CallbackID(1) per event
		size += uint32(len(el.Children)) * 2  // Offset(2) per child pointer

		el.CalculatedSize = size
		if size < KRBElementHeaderSize {
			return fmt.Errorf("internal error: calculated size %d for element %d ('%s') is less than header size %d", size, i, el.SourceElementName, KRBElementHeaderSize)
		}
		if size > math.MaxUint32 { // Check for overflow, though unlikely
			return fmt.Errorf("internal error: calculated size overflow for element %d ('%s')", i, el.SourceElementName)
		}
		currentOffset += size
	}

	// --- Styles ---
	state.StyleOffset = currentOffset
	for i := range state.Styles {
		style := &state.Styles[i]
		size := uint32(3) // ID(1) + NameIdx(1) + PropCount(1)
		for _, prop := range style.Properties {
			size += 3 + uint32(prop.Size) // ID(1) + Type(1) + Size(1) + Data(Size)
		}
		style.CalculatedSize = size
		if size < 3 {
			return fmt.Errorf("internal error: calculated size %d for style %d ('%s') is less than minimum size 3", size, i, style.SourceName)
		}
		if size > math.MaxUint32 { // Check for overflow
			return fmt.Errorf("internal error: calculated size overflow for style %d ('%s')", i, style.SourceName)
		}
		currentOffset += size
	}

	// --- Animations ---
	state.AnimOffset = currentOffset // Animation size is 0 for now
	// currentOffset += calculatedAnimationSize

	// --- Strings ---
	state.StringOffset = currentOffset
	stringSectionSize := uint32(2) // Count field (uint16)
	if len(state.Strings) > 0 {
		for _, s := range state.Strings {
			// Ensure string length fits in the 1-byte length prefix
			if s.Length > 255 {
				return fmt.Errorf("string '%s...' (index %d) length %d exceeds max (255)", s.Text[:min(len(s.Text), 20)], s.Index, s.Length)
			}
			stringSectionSize += 1 + uint32(s.Length) // LengthPrefix(1) + Data(Length)
		}
	}
	// Add size even if count is 0 (for the count field itself)
	currentOffset += stringSectionSize

	// --- Resources ---
	state.ResourceOffset = currentOffset
	resourceSectionSize := uint32(2) // Count field (uint16)
	if len(state.Resources) > 0 {
		for i := range state.Resources {
			// Recalculate resource size (should be fixed 4 for external)
			res := &state.Resources[i]
			resSize := uint32(0)
			if res.Format == ResFormatExternal {
				resSize = 4 // Type(1)+NameIdx(1)+Format(1)+DataStringIndex(1)
			} else {
				return fmt.Errorf("unsupported resource format %d for resource %d", res.Format, i)
			}
			res.CalculatedSize = resSize
			resourceSectionSize += resSize
		}
	}
	// Add size even if count is 0 (for the count field itself)
	currentOffset += resourceSectionSize

	state.TotalSize = currentOffset
	log.Printf("      Total calculated size: %d bytes\n", state.TotalSize)
	return nil
}

// --- Pass 3: Write KRB File ---
func (state *CompilerState) writeKrbFile(filePath string) error {
	log.Printf("Pass 3: Writing KRB v%d.%d binary to '%s'...\n", KRBVersionMajor, KRBVersionMinor, filePath)

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create output file '%s': %w", filePath, err)
	}
	defer file.Close() // Ensure file is closed even on error

	writer := bufio.NewWriter(file) // Use buffered writer for efficiency

	// --- Write Header ---
	if _, err := writer.WriteString(KRBMagic); err != nil {
		return fmt.Errorf("write magic: %w", err)
	}
	if err := writeUint16(writer, (uint16(KRBVersionMinor)<<8)|uint16(KRBVersionMajor)); err != nil {
		return fmt.Errorf("write version: %w", err)
	}
	if err := writeUint16(writer, state.HeaderFlags); err != nil {
		return fmt.Errorf("write flags: %w", err)
	}
	if len(state.Elements) > math.MaxUint16 || len(state.Styles) > math.MaxUint16 ||
		0 > math.MaxUint16 || // Animations
		len(state.Strings) > math.MaxUint16 || len(state.Resources) > math.MaxUint16 {
		return fmt.Errorf("too many items for uint16 counts in header")
	}
	if err := writeUint16(writer, uint16(len(state.Elements))); err != nil {
		return fmt.Errorf("write elem count: %w", err)
	}
	if err := writeUint16(writer, uint16(len(state.Styles))); err != nil {
		return fmt.Errorf("write style count: %w", err)
	}
	if err := writeUint16(writer, uint16(0)); err != nil {
		return fmt.Errorf("write anim count: %w", err)
	} // No animations
	if err := writeUint16(writer, uint16(len(state.Strings))); err != nil {
		return fmt.Errorf("write string count: %w", err)
	}
	if err := writeUint16(writer, uint16(len(state.Resources))); err != nil {
		return fmt.Errorf("write resource count: %w", err)
	}

	if err := writeUint32(writer, state.ElementOffset); err != nil {
		return fmt.Errorf("write elem offset: %w", err)
	}
	if err := writeUint32(writer, state.StyleOffset); err != nil {
		return fmt.Errorf("write style offset: %w", err)
	}
	if err := writeUint32(writer, state.AnimOffset); err != nil {
		return fmt.Errorf("write anim offset: %w", err)
	}
	if err := writeUint32(writer, state.StringOffset); err != nil {
		return fmt.Errorf("write string offset: %w", err)
	}
	if err := writeUint32(writer, state.ResourceOffset); err != nil {
		return fmt.Errorf("write resource offset: %w", err)
	}
	if err := writeUint32(writer, state.TotalSize); err != nil {
		return fmt.Errorf("write total size: %w", err)
	}

	// --- Pad to Element Offset ---
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush after header: %w", err)
	} // Flush before seeking/checking position
	currentPos, _ := file.Seek(0, io.SeekCurrent) // Get current position after flush
	padding := int64(state.ElementOffset) - currentPos
	if padding < 0 {
		return fmt.Errorf("header size/position mismatch or negative padding (%d at pos %d, expected offset %d)", padding, currentPos, state.ElementOffset)
	}
	if padding > 0 {
		log.Printf("    Padding header with %d bytes\n", padding)
		if _, err := writer.Write(make([]byte, padding)); err != nil {
			return fmt.Errorf("write header padding: %w", err)
		}
		if err := writer.Flush(); err != nil { return fmt.Errorf("flush after header padding: %w", err) }
		currentPos += padding // Update our tracking variable
	}
	// Verify position after potential padding
	if uint32(currentPos) != state.ElementOffset {
		actualPos, _ := file.Seek(0, io.SeekCurrent) // Double check actual file pos
		return fmt.Errorf("file position %d (%d actual) mismatch after header write, expected ElementOffset %d", currentPos, actualPos, state.ElementOffset)
	}

	// --- Write Element Blocks ---
	log.Printf("    Writing %d elements at offset %d\n", len(state.Elements), state.ElementOffset)
	for i := range state.Elements {
		el := &state.Elements[i]
		startPos := currentPos // Track start position for size check

		if uint32(currentPos) != el.AbsoluteOffset {
			actualPos, _ := file.Seek(0, io.SeekCurrent)
			return fmt.Errorf("element %d ('%s') offset mismatch: tracked pos %d (actual %d) != expected %d", i, el.SourceElementName, currentPos, actualPos, el.AbsoluteOffset)
		}

		// Write Element Header Fields
		if err := writeUint8(writer, el.Type); err != nil {
			return fmt.Errorf("E%d write type: %w", i, err)
		}
		if err := writeUint8(writer, el.IDStringIndex); err != nil {
			return fmt.Errorf("E%d write id index: %w", i, err)
		}
		if err := writeUint16(writer, el.PosX); err != nil {
			return fmt.Errorf("E%d write pos x: %w", i, err)
		}
		if err := writeUint16(writer, el.PosY); err != nil {
			return fmt.Errorf("E%d write pos y: %w", i, err)
		}
		if err := writeUint16(writer, el.Width); err != nil {
			return fmt.Errorf("E%d write width: %w", i, err)
		}
		if err := writeUint16(writer, el.Height); err != nil {
			return fmt.Errorf("E%d write height: %w", i, err)
		}
		if err := writeUint8(writer, el.Layout); err != nil {
			return fmt.Errorf("E%d write layout: %w", i, err)
		}
		if err := writeUint8(writer, el.StyleID); err != nil {
			return fmt.Errorf("E%d write style id: %w", i, err)
		}
		if err := writeUint8(writer, uint8(len(el.KrbProperties))); err != nil {
			return fmt.Errorf("E%d write prop count: %w", i, err)
		} // Use final count
		if err := writeUint8(writer, uint8(len(el.Children))); err != nil {
			return fmt.Errorf("E%d write child count: %w", i, err)
		} // Use final count
		if err := writeUint8(writer, uint8(len(el.KrbEvents))); err != nil {
			return fmt.Errorf("E%d write event count: %w", i, err)
		} // Use final count
		if err := writeUint8(writer, el.AnimationCount); err != nil {
			return fmt.Errorf("E%d write anim count: %w", i, err)
		} // 0
		if err := writeUint8(writer, el.CustomPropCount); err != nil {
			return fmt.Errorf("E%d write custom prop count: %w", i, err)
		} // 0

		// Write Properties
		for j, prop := range el.KrbProperties {
			if err := writeUint8(writer, prop.PropertyID); err != nil {
				return fmt.Errorf("E%d P%d write id: %w", i, j, err)
			}
			if err := writeUint8(writer, prop.ValueType); err != nil {
				return fmt.Errorf("E%d P%d write type: %w", i, j, err)
			}
			if err := writeUint8(writer, prop.Size); err != nil {
				return fmt.Errorf("E%d P%d write size: %w", i, j, err)
			}
			if prop.Size > 0 {
				if prop.Value == nil {
					return fmt.Errorf("E%d P%d has size %d but nil value", i, j, prop.Size)
				}
				if n, err := writer.Write(prop.Value); err != nil || n != int(prop.Size) {
					return fmt.Errorf("E%d P%d write value (size %d): wrote %d, err %w", i, j, prop.Size, n, err)
				}
			}
		}

		// Write Events
		for j, event := range el.KrbEvents {
			if err := writeUint8(writer, event.EventType); err != nil {
				return fmt.Errorf("E%d Ev%d write type: %w", i, j, err)
			}
			if err := writeUint8(writer, event.CallbackID); err != nil {
				return fmt.Errorf("E%d Ev%d write cb id: %w", i, j, err)
			}
		}

		// Write Children (relative offsets)
		for j, child := range el.Children {
			if child == nil {
				return fmt.Errorf("E%d C%d has nil child pointer", i, j)
			}
			if child.AbsoluteOffset == 0 && child.SelfIndex != 0 { // Allow root offset 0
				if el.AbsoluteOffset > 0 || child.AbsoluteOffset > 0 { // Error if parent has offset but child doesn't
					return fmt.Errorf("E%d C%d ('%s' -> '%s') has zero absolute offset %d but non-zero parent offset %d or index %d", i, j, el.SourceElementName, child.SourceElementName, child.AbsoluteOffset, el.AbsoluteOffset, child.SelfIndex)
				}
			}

			relativeOffset := int64(child.AbsoluteOffset) - int64(el.AbsoluteOffset) // Use int64 for intermediate calc

			if relativeOffset <= 0 {
				// Child offset MUST be positive if parent has non-zero offset.
				// If parent offset is 0 (root App), child offset can be > 0.
				// Can never be <=0 if child != parent.
				if el.AbsoluteOffset > 0 || child.AbsoluteOffset > 0 { // Allow if both are truly 0? Unlikely.
				    return fmt.Errorf("E%d C%d ('%s' -> '%s') has invalid relative offset %d (child_abs=%d, parent_abs=%d)", i, j, el.SourceElementName, child.SourceElementName, relativeOffset, child.AbsoluteOffset, el.AbsoluteOffset)
				}
				// If relativeOffset is 0 and both AbsoluteOffsets are 0, log warning? Assume ok for now.
			}
			if relativeOffset > math.MaxUint16 {
				return fmt.Errorf("E%d C%d ('%s' -> '%s') relative offset %d exceeds uint16 max", i, j, el.SourceElementName, child.SourceElementName, relativeOffset)
			}

			if err := writeUint16(writer, uint16(relativeOffset)); err != nil {
				return fmt.Errorf("E%d C%d write offset %d: %w", i, j, relativeOffset, err)
			}
		}

		// Size check for this element
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("E%d flush error: %w", i, err)
		}
		endPos, _ := file.Seek(0, io.SeekCurrent)
		bytesWrittenForElement := uint32(endPos - startPos)
		if bytesWrittenForElement != el.CalculatedSize {
			return fmt.Errorf("element %d ('%s') size mismatch: wrote %d bytes, calculated %d bytes", i, el.SourceElementName, bytesWrittenForElement, el.CalculatedSize)
		}
		currentPos = endPos // Update current position

	} // End element loop

	// --- Write Style Blocks ---
	log.Printf("    Writing %d styles at offset %d\n", len(state.Styles), state.StyleOffset)
	if len(state.Styles) > 0 {
		if uint32(currentPos) != state.StyleOffset {
			actualPos, _ := file.Seek(0, io.SeekCurrent)
			return fmt.Errorf("style section offset mismatch: tracked pos %d (actual %d) != expected %d", currentPos, actualPos, state.StyleOffset)
		}
		for i := range state.Styles {
			style := &state.Styles[i]
			startPos := currentPos

			if err := writeUint8(writer, style.ID); err != nil {
				return fmt.Errorf("S%d write id: %w", i, err)
			}
			if err := writeUint8(writer, style.NameIndex); err != nil {
				return fmt.Errorf("S%d write name idx: %w", i, err)
			}
			if err := writeUint8(writer, uint8(len(style.Properties))); err != nil {
				return fmt.Errorf("S%d write prop count: %w", i, err)
			}

			for j, prop := range style.Properties {
				if err := writeUint8(writer, prop.PropertyID); err != nil {
					return fmt.Errorf("S%d P%d write id: %w", i, j, err)
				}
				if err := writeUint8(writer, prop.ValueType); err != nil {
					return fmt.Errorf("S%d P%d write type: %w", i, j, err)
				}
				if err := writeUint8(writer, prop.Size); err != nil {
					return fmt.Errorf("S%d P%d write size: %w", i, j, err)
				}
				if prop.Size > 0 {
					if prop.Value == nil {
						return fmt.Errorf("S%d P%d has size %d but nil value", i, j, prop.Size)
					}
					if n, err := writer.Write(prop.Value); err != nil || n != int(prop.Size) {
						return fmt.Errorf("S%d P%d write value (size %d): wrote %d, err %w", i, j, prop.Size, n, err)
					}
				}
			}
			// Size check for this style
			if err := writer.Flush(); err != nil {
				return fmt.Errorf("S%d flush error: %w", i, err)
			}
			endPos, _ := file.Seek(0, io.SeekCurrent)
			bytesWrittenForStyle := uint32(endPos - startPos)
			if bytesWrittenForStyle != style.CalculatedSize {
				return fmt.Errorf("style %d ('%s') size mismatch: wrote %d bytes, calculated %d bytes", i, style.SourceName, bytesWrittenForStyle, style.CalculatedSize)
			}
			currentPos = endPos
		}
	}

	// --- Write Animation Table --- (Skip)
	log.Printf("    Writing 0 animations at offset %d\n", state.AnimOffset)
	if uint32(currentPos) != state.AnimOffset {
		actualPos, _ := file.Seek(0, io.SeekCurrent)
		return fmt.Errorf("animation section offset mismatch: tracked pos %d (actual %d) != expected %d", currentPos, actualPos, state.AnimOffset)
	}
	// No data to write for animations

	// --- Write String Table ---
	log.Printf("    Writing %d strings at offset %d\n", len(state.Strings), state.StringOffset)
	if uint32(currentPos) != state.StringOffset {
		actualPos, _ := file.Seek(0, io.SeekCurrent)
		return fmt.Errorf("string section offset mismatch: tracked pos %d (actual %d) != expected %d", currentPos, actualPos, state.StringOffset)
	}
	if err := writeUint16(writer, uint16(len(state.Strings))); err != nil {
		return fmt.Errorf("write string count value: %w", err)
	}
	if len(state.Strings) > 0 {
		for i := range state.Strings {
			s := &state.Strings[i]
			if s.Length > 255 { // Check moved to calculateOffsetsAndSizes
				return fmt.Errorf("string %d length %d exceeds max length prefix (255) - should have been caught earlier", i, s.Length)
			}
			if err := writeUint8(writer, uint8(s.Length)); err != nil {
				return fmt.Errorf("Str%d write len %d: %w", i, s.Length, err)
			}
			if s.Length > 0 {
				if n, err := writer.WriteString(s.Text); err != nil || n != s.Length {
					return fmt.Errorf("Str%d write text (len %d): wrote %d, err %w", i, s.Length, n, err)
				}
			}
		}
		// Flush after the loop finishes writing all strings in the section
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("flush after strings: %w", err)
		}
		endPos, _ := file.Seek(0, io.SeekCurrent) // Update position *after* the section
		currentPos = endPos
	} else {
		// If no strings, we still wrote the count(0), update position
		if err := writer.Flush(); err != nil { return fmt.Errorf("flush after zero strings: %w", err) }
		currentPos += 2 // For the count field
	}


	// --- Write Resource Table ---
	log.Printf("    Writing %d resources at offset %d\n", len(state.Resources), state.ResourceOffset)
	if uint32(currentPos) != state.ResourceOffset {
		actualPos, _ := file.Seek(0, io.SeekCurrent)
		return fmt.Errorf("resource section offset mismatch: tracked pos %d (actual %d) != expected %d", currentPos, actualPos, state.ResourceOffset)
	}
	if err := writeUint16(writer, uint16(len(state.Resources))); err != nil {
		return fmt.Errorf("write resource count value: %w", err)
	}
	if len(state.Resources) > 0 {
		for i := range state.Resources {
			r := &state.Resources[i]

			if err := writeUint8(writer, r.Type); err != nil {
				return fmt.Errorf("Res%d write type: %w", i, err)
			}
			if err := writeUint8(writer, r.NameIndex); err != nil {
				return fmt.Errorf("Res%d write name idx: %w", i, err)
			}
			if err := writeUint8(writer, r.Format); err != nil {
				return fmt.Errorf("Res%d write format: %w", i, err)
			}
			if r.Format == ResFormatExternal {
				if err := writeUint8(writer, r.DataStringIndex); err != nil {
					return fmt.Errorf("Res%d write data idx: %w", i, err)
				}
			} else {
				return fmt.Errorf("unsupported resource format %d for resource %d", r.Format, i)
			}
			// No size check inside the loop anymore
		}
		// Flush after the loop finishes writing all resources in the section
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("flush after resources: %w", err)
		}
		endPos, _ := file.Seek(0, io.SeekCurrent) // Update position *after* the section
		currentPos = endPos
	} else {
		// If no resources, we still wrote the count(0), update position
		if err := writer.Flush(); err != nil { return fmt.Errorf("flush after zero resources: %w", err) }
		currentPos += 2 // For the count field
	}


	// --- Final Flush and Size Check ---
	if err := writer.Flush(); err != nil { // Final flush before checking size
		return fmt.Errorf("final flush error: %w", err)
	}

	finalSize, err := file.Seek(0, io.SeekCurrent) // Get final position
	if err != nil {
		return fmt.Errorf("failed to get final file size: %w", err)
	}

	if uint32(finalSize) != state.TotalSize {
		return fmt.Errorf("final write size mismatch: file size %d != calculated total size %d", finalSize, state.TotalSize)
	}
	if uint32(currentPos) != state.TotalSize { // Also check our tracked position
		return fmt.Errorf("final tracked position mismatch: tracked pos %d != calculated total size %d", currentPos, state.TotalSize)
	}


	return nil // Success
}

// Helper for max(int, int) needed above
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}