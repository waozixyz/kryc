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
)

// --- Pass 2: Calculate Offsets & Sizes (KRB v0.4) ---
func (state *CompilerState) calculateOffsetsAndSizes() error {
	log.Println("Pass 2: Calculating final offsets and sizes (KRB v0.4)...")
	currentOffset := uint32(KRBHeaderSize) // Start after the main file header (48 bytes for v0.4)

	// --- 1. Elements Section Size (Main UI Tree ONLY) ---
	state.ElementOffset = currentOffset
	state.TotalElementDataSize = 0
	mainTreeElementCount := 0

	log.Println("DEBUG_CALC_OFFSETS: Elements considered for main UI tree sizing:")

	for i := range state.Elements {
		el := &state.Elements[i]
		if el.IsDefinitionRoot { // Skip elements that are part of a component template's definition
			continue
		}
		mainTreeElementCount++ // Count only main tree elements

		el.AbsoluteOffset = currentOffset // Global offset for this main tree element

		size := uint32(KRBElementHeaderSize)
		for _, prop := range el.KrbProperties {
			size += 3                 // PropertyID(1) + ValueType(1) + Size(1)
			size += uint32(prop.Size) // Value data
		}
		for _, customProp := range el.KrbCustomProperties {
			size += 1                        // Key Index
			size += 1                        // Value Type
			size += 1                        // Value Size
			size += uint32(customProp.Size) // Value data
		}
		size += uint32(len(el.KrbEvents)) * 2  // EventType(1) + CallbackID(1)
		size += uint32(len(el.Children)) * 2 // RelativeOffsetToChild(2)

		el.CalculatedSize = size
		if size < KRBElementHeaderSize {
			return fmt.Errorf("internal: main element %d ('%s') calculated size %d < header size %d", i, el.SourceElementName, size, KRBElementHeaderSize)
		}
		state.TotalElementDataSize += size
		currentOffset += size
	}
	log.Printf("      Calculated Main UI Tree: %d elements, %d bytes data.", mainTreeElementCount, state.TotalElementDataSize)

	// --- 2. Styles Section Size ---
	state.StyleOffset = currentOffset
	state.TotalStyleDataSize = 0
	if (state.HeaderFlags & FlagHasStyles) != 0 {
		for i := range state.Styles {
			style := &state.Styles[i]
			size := uint32(3) // StyleHeader: ID(1) + NameIdx(1) + PropCount(1)
			for _, prop := range style.Properties {
				size += 3                  // PropID(1) + ValType(1) + Size(1)
				size += uint32(prop.Size) // Data
			}
			style.CalculatedSize = size
			if size < 3 { // Smallest possible style block (header only, 0 props)
				return fmt.Errorf("internal: style %d ('%s') calculated size %d < minimum 3", i, style.SourceName, size)
			}
			state.TotalStyleDataSize += size
			currentOffset += size
		}
	}
	log.Printf("      Calculated Styles: %d styles, %d bytes data.", len(state.Styles), state.TotalStyleDataSize)
	
	// --- 3. Component Definition Table Section Size ---
	state.ComponentDefOffset = currentOffset
	state.TotalComponentDefDataSize = 0
	if (state.HeaderFlags & FlagHasComponentDefs) != 0 {
		for cdi := range state.ComponentDefs {
			def := &state.ComponentDefs[cdi]

			// Ensure component name string is accounted for BEFORE sizing this comp def entry
			// and crucially before state.Strings is used to size the string table section.
			_, err := state.addString(def.Name)
			if err != nil {
				return fmt.Errorf("CompDef '%s': error adding component name to string table during size calculation: %w", def.Name, err)
			}

			singleDefEntrySize := uint32(0)
			singleDefEntrySize += 1 // Name Index (for def.Name, 1 byte for the index itself)
			singleDefEntrySize += 1 // Property Def Count

			for _, propDef := range def.Properties {
				// Ensure property definition name string is accounted for
				_, err := state.addString(propDef.Name)
				if err != nil {
					return fmt.Errorf("CompDef '%s' prop '%s': error adding property name to string table during size calculation: %w", def.Name, propDef.Name, err)
				}

				singleDefEntrySize += 1 // Name Index (for propDef.Name)
				singleDefEntrySize += 1 // Value Type Hint
				singleDefEntrySize += 1 // Default Value Size

				// This part correctly handles adding default string values to state.Strings during sizing
				_, defaultValDataSize, parseErr := getBinaryDefaultValue(state, propDef.DefaultValueStr, propDef.ValueTypeHint)
				if parseErr != nil {
					return fmt.Errorf("compdef '%s' prop '%s': error sizing default value '%s': %w", def.Name, propDef.Name, propDef.DefaultValueStr, parseErr)
				}
				singleDefEntrySize += uint32(defaultValDataSize)
			}

			if def.DefinitionRootElementIndex < 0 || def.DefinitionRootElementIndex >= len(state.Elements) {
				return fmt.Errorf("compdef '%s': invalid root element index %d", def.Name, def.DefinitionRootElementIndex)
			}

			templateElementsIndices := getTemplateElementIndices(state, def.DefinitionRootElementIndex)
			templateTotalDataSize := uint32(0)
			for _, tplElIdx := range templateElementsIndices {
				tplEl := &state.Elements[tplElIdx]

				// Ensure strings from KrbProperties of template elements were added in Pass 1.5
				// (No explicit addString calls needed here for KrbProperties themselves, as they store indices/binary data)

				tempSize := uint32(KRBElementHeaderSize)
				for _, prop := range tplEl.KrbProperties {
					tempSize += 3 // PropertyID(1) + ValueType(1) + Size(1)
					tempSize += uint32(prop.Size) // Value data
				}
				// Note: Custom Properties and Events are typically NOT part of the template's
				// static definition in KRB. They are applied at instantiation.
				// So, their counts in the template element header should be 0, and no size added here.

				// ChildCount for template elements refers to children *within the template*.
				actualTemplateChildCount := 0
				for _, childRefIdx := range tplEl.SourceChildrenIndices {
					isChildInThisTemplate := false
					for _, tei := range templateElementsIndices {
						if childRefIdx == tei {
							isChildInThisTemplate = true
							break
						}
					}
					if isChildInThisTemplate {
						actualTemplateChildCount++
					}
				}
				tempSize += uint32(actualTemplateChildCount) * 2 // ChildOffset(2) for each child in template

				tplEl.CalculatedSize = tempSize // This is the size of this *one* element within the template definition
				templateTotalDataSize += tempSize
			}

			singleDefEntrySize += templateTotalDataSize
			def.CalculatedSize = singleDefEntrySize // Total size for this single component definition entry
			state.TotalComponentDefDataSize += singleDefEntrySize
			currentOffset += singleDefEntrySize
		}
	}
	log.Printf("      Calculated Component Defs: %d defs, %d bytes data.", len(state.ComponentDefs), state.TotalComponentDefDataSize)

	// --- 4. Animations Section Size ---
	state.AnimOffset = currentOffset
	log.Printf("      Calculated Animations: 0 anims, 0 bytes data.")

	// --- 5. Strings Section Size ---
	state.StringOffset = currentOffset
	stringSectionHeaderSize := uint32(2) // String Count (uint16) - Always present
	state.TotalStringDataSize = 0
	// The KRY spec says "Index 0 may represent an empty/null string."
	// The addString function ensures state.Strings[0] is the empty string if any strings are added.
	// If len(state.Strings) is 0 after parsing, your file header will have StringCount 0.
	// The string section itself would then just be the 2-byte count (0).
	// The loop calculates TotalStringDataSize based on actual strings.
	if len(state.Strings) > 0 { // This condition is actually not strictly necessary if stringSectionHeaderSize is always added
		for _, s := range state.Strings {
			if s.Length > 255 {
				return fmt.Errorf("string '%s...' (idx %d) length %d exceeds max 255", s.Text[:min(len(s.Text), 20)], s.Index, s.Length)
			}
			state.TotalStringDataSize += 1 + uint32(s.Length) // 1 for length byte + actual string bytes
		}
	}
	currentOffset += stringSectionHeaderSize + state.TotalStringDataSize
	log.Printf("      Calculated Strings: %d strings, %d bytes data (+%d for count).", len(state.Strings), state.TotalStringDataSize, stringSectionHeaderSize)

	// --- 6. Resources Section Size ---
	state.ResourceOffset = currentOffset
	resourceSectionHeaderSize := uint32(0) // Initialize to 0
	state.TotalResourceTableSize = 0

	if (state.HeaderFlags & FlagHasResources) != 0 { // Check if the flag IS set (meaning resources exist)
		resourceSectionHeaderSize = 2 // Resource Count (uint16) only if there are resources
		for i := range state.Resources {
			res := &state.Resources[i]
			resSize := uint32(0)
			if res.Format == ResFormatExternal {
				resSize = 4 // Type(1) + NameIndex(1) + Format(1) + DataStringIndex(1)
			} else if res.Format == ResFormatInline {
				// Properly calculate inline resource size if you implement it
				// resSize = 3 + uint32(len(res.InlineData)) // 3 for Type,NameIdx,Format + 2 for Size + data
				return fmt.Errorf("unsupported resource format %d for resource %d during size calculation", res.Format, i)
			} else {
				return fmt.Errorf("unknown resource format %d for resource %d during size calculation", res.Format, i)
			}
			res.CalculatedSize = resSize
			state.TotalResourceTableSize += resSize
		}
	}
	// Always add the calculated sizes to currentOffset. If no resources, both will be 0.
	currentOffset += resourceSectionHeaderSize + state.TotalResourceTableSize
	log.Printf("      Calculated Resources: %d resources, %d bytes data (+%d for count).", len(state.Resources), state.TotalResourceTableSize, resourceSectionHeaderSize)

	state.TotalSize = currentOffset

	log.Printf("      Total calculated KRB file size: %d bytes\n", state.TotalSize)
	return nil
}

// --- Pass 3: Write KRB File (KRB v0.4) ---
func (state *CompilerState) writeKrbFile(filePath string) error {
	log.Printf("Pass 3: Writing KRB v%d.%d binary to '%s'...\n", KRBVersionMajor, KRBVersionMinor, filePath)

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("create output file '%s': %w", filePath, err)
	}
	defer file.Close()
	writer := bufio.NewWriter(file)

	// --- Write KRB File Header (48 bytes for v0.4) ---
	_, err = writer.WriteString(KRBMagic)
	if err != nil {
		return fmt.Errorf("write magic: %w", err)
	}

	versionField := (uint16(KRBVersionMinor) << 8) | uint16(KRBVersionMajor)
	err = writeUint16(writer, versionField)
	if err != nil {
		return fmt.Errorf("write version: %w", err)
	}

	err = writeUint16(writer, state.HeaderFlags)
	if err != nil {
		return fmt.Errorf("write flags: %w", err)
	}

	mainTreeElementCount := 0
	for i := range state.Elements {
		if !state.Elements[i].IsDefinitionRoot {
			mainTreeElementCount++
		}
	}

	err = writeUint16(writer, uint16(mainTreeElementCount))
	if err != nil {
		return fmt.Errorf("write element count: %w", err)
	}
	err = writeUint16(writer, uint16(len(state.Styles)))
	if err != nil {
		return fmt.Errorf("write style count: %w", err)
	}
	err = writeUint16(writer, uint16(len(state.ComponentDefs)))
	if err != nil {
		return fmt.Errorf("write component def count: %w", err)
	}
	err = writeUint16(writer, uint16(0)) // Animation Count
	if err != nil {
		return fmt.Errorf("write animation count: %w", err)
	}
	err = writeUint16(writer, uint16(len(state.Strings)))
	if err != nil {
		return fmt.Errorf("write string count: %w", err)
	}
	err = writeUint16(writer, uint16(len(state.Resources)))
	if err != nil {
		return fmt.Errorf("write resource count: %w", err)
	}

	err = writeUint32(writer, state.ElementOffset)
	if err != nil {
		return fmt.Errorf("write element offset: %w", err)
	}
	err = writeUint32(writer, state.StyleOffset)
	if err != nil {
		return fmt.Errorf("write style offset: %w", err)
	}
	err = writeUint32(writer, state.ComponentDefOffset)
	if err != nil {
		return fmt.Errorf("write component def offset: %w", err)
	}
	err = writeUint32(writer, state.AnimOffset)
	if err != nil {
		return fmt.Errorf("write animation offset: %w", err)
	}
	err = writeUint32(writer, state.StringOffset)
	if err != nil {
		return fmt.Errorf("write string offset: %w", err)
	}
	err = writeUint32(writer, state.ResourceOffset)
	if err != nil {
		return fmt.Errorf("write resource offset: %w", err)
	}
	err = writeUint32(writer, state.TotalSize)
	if err != nil {
		return fmt.Errorf("write total size: %w", err)
	}

	// --- Pad to Element Offset if necessary ---
	err = writer.Flush()
	if err != nil {
		return fmt.Errorf("flush after header: %w", err)
	}
	currentFilePos, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("seek after header flush: %w", err)
	}
	paddingNeeded := int64(state.ElementOffset) - currentFilePos
	if paddingNeeded < 0 {
		return fmt.Errorf("header size/position mismatch: negative padding %d (pos %d, elemOffset %d)", paddingNeeded, currentFilePos, state.ElementOffset)
	}
	if paddingNeeded > 0 {
		log.Printf("    Padding KRB header with %d zero bytes.\n", paddingNeeded)
		_, err = writer.Write(make([]byte, paddingNeeded))
		if err != nil {
			return fmt.Errorf("write header padding: %w", err)
		}
	}
	err = writer.Flush()
	if err != nil {
		return fmt.Errorf("flush after padding: %w", err)
	}
	currentFilePos, err = file.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("seek after padding flush: %w", err)
	}

	// --- Write Element Blocks (Main UI Tree ONLY) ---
	log.Printf("    Writing %d main UI tree elements at offset %d (actual: %d)\n", mainTreeElementCount, state.ElementOffset, currentFilePos)
	if uint32(currentFilePos) != state.ElementOffset {
		return fmt.Errorf("file pos %d != ElementOffset %d before main elements", currentFilePos, state.ElementOffset)
	}
	
	firstElementWritten := false // Add a flag
	for i := range state.Elements {
		el := &state.Elements[i]
		if el.IsDefinitionRoot { // Skip template elements
			continue
		}

		if !firstElementWritten {
			if (state.HeaderFlags&FlagHasApp) != 0 && el.Type != ElemTypeApp {
                // Corrected Errorf:
                return fmt.Errorf("KRB_WRITE_ERROR: FLAG_HAS_APP is set, but the first main tree element to be written is '%s' (Type 0x%02X), not App (Type 0x%02X). Index in state.Elements: %d", el.SourceElementName, el.Type, ElemTypeApp, i)
            }
			firstElementWritten = true
		}

		startPos := currentFilePos
		if uint32(currentFilePos) != el.AbsoluteOffset {
			return fmt.Errorf("main elem %d ('%s') offset mismatch: current %d != expected %d", i, el.SourceElementName, currentFilePos, el.AbsoluteOffset)
		}

		err = writeElementHeader(writer, el)
		if err != nil {
			return fmt.Errorf("main elem %d ('%s') header write: %w", i, el.SourceElementName, err)
		}
		err = writeElementProperties(writer, el.KrbProperties, "StdP")
		if err != nil {
			return fmt.Errorf("main elem %d ('%s') std props write: %w", i, el.SourceElementName, err)
		}
		err = writeElementCustomProperties(writer, el.KrbCustomProperties, "CstP")
		if err != nil {
			return fmt.Errorf("main elem %d ('%s') custom props write: %w", i, el.SourceElementName, err)
		}
		err = writeElementEvents(writer, el.KrbEvents)
		if err != nil {
			return fmt.Errorf("main elem %d ('%s') events write: %w", i, el.SourceElementName, err)
		}

		for cIdx, child := range el.Children {
			relativeOffset := child.AbsoluteOffset - el.AbsoluteOffset
			if relativeOffset > math.MaxUint16 || (relativeOffset <= 0 && child.AbsoluteOffset != el.AbsoluteOffset) {
				return fmt.Errorf("main elem %d ('%s') child #%d ('%s') invalid rel offset %d (child_abs=%d, parent_abs=%d)", i, el.SourceElementName, cIdx, child.SourceElementName, relativeOffset, child.AbsoluteOffset, el.AbsoluteOffset)
			}
			err = writeUint16(writer, uint16(relativeOffset))
			if err != nil {
				return fmt.Errorf("main elem %d child #%d rel offset write: %w", i, cIdx, err)
			}
		}

		err = writer.Flush()
		if err != nil {
			return fmt.Errorf("main elem %d ('%s') flush: %w", i, el.SourceElementName, err)
		}
		currentFilePos, err = file.Seek(0, io.SeekCurrent)
		if err != nil {
			return fmt.Errorf("main elem %d ('%s') seek after write: %w", i, el.SourceElementName, err)
		}

		bytesWritten := uint32(currentFilePos - startPos)
		if bytesWritten != el.CalculatedSize {
			return fmt.Errorf("main elem %d ('%s') size mismatch: wrote %d, expected %d", i, el.SourceElementName, bytesWritten, el.CalculatedSize)
		}
	}

	// --- Write Style Blocks ---

	log.Println("DEBUG_WRITER: Elements considered for main UI tree output:")
	log.Printf("DEBUG_WRITER: state.HasApp = %t, (state.HeaderFlags & FlagHasApp) != 0 is %t", state.HasApp, (state.HeaderFlags&FlagHasApp) != 0)
	actualMainTreeElementsCountedInWriter := 0
	for elemIdxLoop := range state.Elements {
		loopEl := &state.Elements[elemIdxLoop]
		if !loopEl.IsDefinitionRoot {
			log.Printf("  -> Main Tree Element Candidate: IdxInState=%d, Name='%s', Type=0x%02X, IsDefRoot=%t, SourceLine=%d, ParentIdx=%d, CalculatedSize=%d, AbsOffset=%d",
				elemIdxLoop,
				loopEl.SourceElementName,
				loopEl.Type,
				loopEl.IsDefinitionRoot,
				loopEl.SourceLineNum,
				loopEl.ParentIndex,
				loopEl.CalculatedSize,
				loopEl.AbsoluteOffset)
			actualMainTreeElementsCountedInWriter++
		}
	}
	log.Printf("DEBUG_WRITER: Total candidates identified by writer loop: %d. Header's mainTreeElementCount: %d", actualMainTreeElementsCountedInWriter, mainTreeElementCount)
	// Check if mainTreeElementCount (used in header) matches actualMainTreeElementsCountedInWriter (that will be looped)
	if uint16(actualMainTreeElementsCountedInWriter) != uint16(mainTreeElementCount) {
		 log.Printf("DEBUG_WRITER_ERROR: Mismatch between mainTreeElementCount in header (%d) and elements writer will loop over (%d)!", mainTreeElementCount, actualMainTreeElementsCountedInWriter)
	}

	log.Printf("    Writing %d styles at offset %d (actual: %d)\n", len(state.Styles), state.StyleOffset, currentFilePos)
	if (state.HeaderFlags & FlagHasStyles) != 0 {
		if uint32(currentFilePos) != state.StyleOffset {
			return fmt.Errorf("file pos %d != StyleOffset %d before styles", currentFilePos, state.StyleOffset)
		}
		for i := range state.Styles {
			style := &state.Styles[i]
			startPos := currentFilePos
			err = writeUint8(writer, style.ID)
			if err != nil { return fmt.Errorf("style %d ID: %w", i, err) }
			err = writeUint8(writer, style.NameIndex)
			if err != nil { return fmt.Errorf("style %d NameIdx: %w", i, err) }
			err = writeUint8(writer, uint8(len(style.Properties)))
			if err != nil { return fmt.Errorf("style %d PropCount: %w", i, err) }
			err = writeElementProperties(writer, style.Properties, "StyleP")
			if err != nil { return fmt.Errorf("style %d ('%s') props: %w", i, style.SourceName, err) }

			err = writer.Flush()
			if err != nil { return fmt.Errorf("style %d ('%s') flush: %w", i, style.SourceName, err) }
			currentFilePos, err = file.Seek(0, io.SeekCurrent)
			if err != nil { return fmt.Errorf("style %d ('%s') seek: %w", i, style.SourceName, err) }
			bytesWritten := uint32(currentFilePos - startPos)
			if bytesWritten != style.CalculatedSize {
				return fmt.Errorf("style %d ('%s') size mismatch: wrote %d, expected %d", i, style.SourceName, bytesWritten, style.CalculatedSize)
			}
		}
	}

	// --- Write Component Definition Table ---
	log.Printf("    Writing %d component definitions at offset %d (actual: %d)\n", len(state.ComponentDefs), state.ComponentDefOffset, currentFilePos)
	if (state.HeaderFlags & FlagHasComponentDefs) != 0 {
		if uint32(currentFilePos) != state.ComponentDefOffset {
			return fmt.Errorf("file pos %d != ComponentDefOffset %d before comp defs", currentFilePos, state.ComponentDefOffset)
		}
		for cdi := range state.ComponentDefs {
			def := &state.ComponentDefs[cdi]
			startPosDef := currentFilePos

			nameIdx, strErr := state.addString(def.Name)
			if strErr != nil {
				return fmt.Errorf("CompDef '%s' name string error: %w", def.Name, strErr)
			}
			err = writeUint8(writer, nameIdx)
			if err != nil {
				return fmt.Errorf("CompDef '%s' write name index: %w", def.Name, err)
			}

			err = writeUint8(writer, uint8(len(def.Properties)))
			if err != nil {
				return fmt.Errorf("CompDef '%s' write prop def count: %w", def.Name, err)
			}

			for pdi, propDef := range def.Properties {
				propNameIdx, strErr2 := state.addString(propDef.Name)
				if strErr2 != nil {
					return fmt.Errorf("CompDef '%s' prop #%d ('%s') name string error: %w", def.Name, pdi, propDef.Name, strErr2)
				}
				err = writeUint8(writer, propNameIdx)
				if err != nil {
					return fmt.Errorf("CompDef '%s' prop '%s' write name index: %w", def.Name, propDef.Name, err)
				}
				err = writeUint8(writer, propDef.ValueTypeHint)
				if err != nil {
					return fmt.Errorf("CompDef '%s' prop '%s' write type hint: %w", def.Name, propDef.Name, err)
				}

				valueBytes, _, valueSize, parseErr := parseKryValueToKrbBytes(state, propDef.DefaultValueStr, propDef.ValueTypeHint, "compDefDefaultWrite", lineNumForError(def.DefinitionStartLine, pdi))
				if parseErr != nil {
					return fmt.Errorf("CompDef '%s' prop '%s' default value '%s' (hint %d): %w", def.Name, propDef.Name, propDef.DefaultValueStr, propDef.ValueTypeHint, parseErr)
				}
				err = writeUint8(writer, valueSize) // DefaultValueSize
				if err != nil {
					return fmt.Errorf("CompDef '%s' prop '%s' write default value size: %w", def.Name, propDef.Name, err)
				}
				if valueSize > 0 {
					_, err = writer.Write(valueBytes)
					if err != nil {
						return fmt.Errorf("CompDef '%s' prop '%s' write default value data (size %d): %w", def.Name, propDef.Name, valueSize, err)
					}
				}
			}

			templateRootEl := &state.Elements[def.DefinitionRootElementIndex]
			templateElementsIndices := getTemplateElementIndices(state, def.DefinitionRootElementIndex)

			// The file offset where the header of this specific template's root element begins.
			// All child offsets within this template definition will be relative to this.
			// Note: writer.Buffered() gives bytes in buffer, not yet written to file.
			// We need the position *after* the component def header and property defs are flushed.
			err = writer.Flush()
			if err != nil { return fmt.Errorf("CompDef '%s' flush before template root: %w", def.Name, err) }
			_, err := file.Seek(0, io.SeekCurrent)
			if err != nil { return fmt.Errorf("CompDef '%s' seek for template root offset: %w", def.Name, err) }


			for _, tplElIdx := range templateElementsIndices {
				tplEl := &state.Elements[tplElIdx]

				// Write Element Header for this template element
				err = writeUint8(writer, tplEl.Type)
				if err != nil { return fmt.Errorf("TplElem '%s' Type: %w", tplEl.SourceElementName, err)}
				err = writeUint8(writer, tplEl.IDStringIndex)
				if err != nil { return fmt.Errorf("TplElem '%s' IDStringIndex: %w", tplEl.SourceElementName, err) }
				err = writeUint16(writer, tplEl.PosX)
				if err != nil { return fmt.Errorf("TplElem '%s' PosX: %w", tplEl.SourceElementName, err)}
				err = writeUint16(writer, tplEl.PosY)
				if err != nil { return fmt.Errorf("TplElem '%s' PosY: %w", tplEl.SourceElementName, err)}
				err = writeUint16(writer, tplEl.Width)
				if err != nil { return fmt.Errorf("TplElem '%s' Width: %w", tplEl.SourceElementName, err)}
				err = writeUint16(writer, tplEl.Height)
				if err != nil { return fmt.Errorf("TplElem '%s' Height: %w", tplEl.SourceElementName, err)}
				err = writeUint8(writer, tplEl.Layout)
				if err != nil { return fmt.Errorf("TplElem '%s' Layout: %w", tplEl.SourceElementName, err)}
				err = writeUint8(writer, tplEl.StyleID)
				if err != nil { return fmt.Errorf("TplElem '%s' StyleID: %w", tplEl.SourceElementName, err)}
				err = writeUint8(writer, uint8(len(tplEl.KrbProperties)))
				if err != nil { return fmt.Errorf("TplElem '%s' PropertyCount: %w", tplEl.SourceElementName, err)}

				actualTemplateChildCount := 0
				for _, childRefIdx := range tplEl.SourceChildrenIndices {
					isChildInThisTemplate := false
					for _, tei := range templateElementsIndices { if childRefIdx == tei { isChildInThisTemplate = true; break } }
					if isChildInThisTemplate { actualTemplateChildCount++ }
				}
				err = writeUint8(writer, uint8(actualTemplateChildCount))
				if err != nil { return fmt.Errorf("TplElem '%s' ChildCount: %w", tplEl.SourceElementName, err)}
				err = writeUint8(writer, 0) // Event Count
				if err != nil { return fmt.Errorf("TplElem '%s' EventCount: %w", tplEl.SourceElementName, err)}
				err = writeUint8(writer, 0) // Animation Count
				if err != nil { return fmt.Errorf("TplElem '%s' AnimationCount: %w", tplEl.SourceElementName, err)}
				err = writeUint8(writer, 0) // Custom Prop Count
				if err != nil { return fmt.Errorf("TplElem '%s' CustomPropCount: %w", tplEl.SourceElementName, err)}

				// Write Standard Properties of the template element
				err = writeElementProperties(writer, tplEl.KrbProperties, fmt.Sprintf("TplElem '%s' Prop", tplEl.SourceElementName))
				if err != nil { return err }

				// Write Child Relative Offsets for template children
				for childNum, childRefIdx := range tplEl.SourceChildrenIndices {
					isChildInThisTemplate := false
					for _, tei := range templateElementsIndices { if childRefIdx == tei { isChildInThisTemplate = true; break } }
					if !isChildInThisTemplate { continue }

					childEl := &state.Elements[childRefIdx]
					
					// The offset written must be:
					// (Absolute file offset where childEl's header will start for *this def entry*) - (Absolute file offset where templateRootEl's header *started* for *this def entry*)
					// This requires pre-calculating the layout of the template elements within the definition entry.
					// `childEl.AbsoluteOffset` and `templateRootEl.AbsoluteOffset` are their global positions if all elements were flat.
					// The difference `childEl.AbsoluteOffset - templateRootEl.AbsoluteOffset` should represent the correct relative
					// offset if the template elements are written sequentially as collected by `getTemplateElementIndices`.

					var offsetRelativeToTemplateRoot uint32
					if childEl.AbsoluteOffset >= templateRootEl.AbsoluteOffset {
						offsetRelativeToTemplateRoot = childEl.AbsoluteOffset - templateRootEl.AbsoluteOffset
					} else {
						return fmt.Errorf("CompDef '%s', TplParent '%s': template child '%s' (abs %d) appears before template root '%s' (abs %d)",
							def.Name, tplEl.SourceElementName, childEl.SourceElementName, childEl.AbsoluteOffset, templateRootEl.SourceElementName, templateRootEl.AbsoluteOffset)
					}

					if offsetRelativeToTemplateRoot > math.MaxUint16 {
						return fmt.Errorf("CompDef '%s', TplParent '%s': template child '%s' relative offset %d to template root exceeds uint16 max",
							def.Name, tplEl.SourceElementName, childEl.SourceElementName, offsetRelativeToTemplateRoot)
					}
					err = writeUint16(writer, uint16(offsetRelativeToTemplateRoot))
					if err != nil {
						return fmt.Errorf("CompDef '%s', TplParent '%s' child #%d ('%s') write rel offset: %w", def.Name, tplEl.SourceElementName, childNum, childEl.SourceElementName, err)
					}
				}
			}

			err = writer.Flush()
			if err != nil {
				return fmt.Errorf("CompDef '%s' flush after template write: %w", def.Name, err)
			}
			currentFilePos, err = file.Seek(0, io.SeekCurrent)
			if err != nil { return fmt.Errorf("CompDef '%s' seek after template write: %w", def.Name, err) }
			
			bytesWrittenForDef := uint32(currentFilePos - startPosDef)
			if bytesWrittenForDef != def.CalculatedSize {
				log.Printf("Warn: CompDef '%s' size mismatch: wrote %d, expected %d. Review CalculatedSize for component definitions.", def.Name, bytesWrittenForDef, def.CalculatedSize)
			}
		}
	}

	// --- Write Animation Table Section ---
	log.Printf("    Writing 0 animations at offset %d (actual: %d)\n", state.AnimOffset, currentFilePos)
	if uint32(currentFilePos) != state.AnimOffset {
		if !(state.AnimOffset == state.StringOffset && (state.HeaderFlags&FlagHasAnimations) == 0) {
			return fmt.Errorf("file pos %d != AnimOffset %d", currentFilePos, state.AnimOffset)
		}
	}

	// --- Write String Table Section ---
	log.Printf("    Writing %d strings at offset %d (actual: %d)\n", len(state.Strings), state.StringOffset, currentFilePos)
	if uint32(currentFilePos) != state.StringOffset {
		return fmt.Errorf("file pos %d != StringOffset %d", currentFilePos, state.StringOffset)
	}
	err = writeUint16(writer, uint16(len(state.Strings)))
	if err != nil {
		return fmt.Errorf("write string count: %w", err)
	}
	for i := range state.Strings {
		s := &state.Strings[i]
		err = writeUint8(writer, uint8(s.Length))
		if err != nil {
			return fmt.Errorf("Str%d write len: %w", i, err)
		}
		if s.Length > 0 {
			_, err = writer.WriteString(s.Text)
			if err != nil {
				return fmt.Errorf("Str%d write text: %w", i, err)
			}
		}
	}
	err = writer.Flush()
	if err != nil {
		return fmt.Errorf("flush strings: %w", err)
	}
	currentFilePos, err = file.Seek(0, io.SeekCurrent)
	if err != nil { return fmt.Errorf("seek after strings: %w", err) }
	// --- Write Resource Table Section ---
	log.Printf("    Writing %d resources at offset %d (actual: %d)\n", len(state.Resources), state.ResourceOffset, currentFilePos)

	if (state.HeaderFlags & FlagHasResources) != 0 {
		// This case means len(state.Resources) > 0 and the FlagHasResources is set.
		// We expect currentFilePos to exactly match where the ResourceOffset was calculated to be.
		if uint32(currentFilePos) != state.ResourceOffset {
			return fmt.Errorf("file position %d != ResourceOffset %d before writing resource section header (resources expected)", currentFilePos, state.ResourceOffset)
		}

		// Write Resource Count
		err = writeUint16(writer, uint16(len(state.Resources)))
		if err != nil {
			return fmt.Errorf("write resource count: %w", err)
		}

		// Write each resource entry
		for _, r := range state.Resources {
			err = writeUint8(writer, r.Type)
			if err != nil {
				return fmt.Errorf("write resource type: %w", err)
			}
			err = writeUint8(writer, r.NameIndex)
			if err != nil {
				return fmt.Errorf("write resource name index: %w", err)
			}
			err = writeUint8(writer, r.Format)
			if err != nil {
				return fmt.Errorf("write resource format: %w", err)
			}
			if r.Format == ResFormatExternal {
				err = writeUint8(writer, r.DataStringIndex)
				if err != nil {
					return fmt.Errorf("write resource data string index: %w", err)
				}
			} else {
				return fmt.Errorf("unsupported resource format %d encountered during write", r.Format)
			}
		}

		err = writer.Flush()
		if err != nil {
			return fmt.Errorf("flush after writing resources: %w", err)
		}
		currentFilePos, err = file.Seek(0, io.SeekCurrent) // Update currentFilePos
		if err != nil {
			return fmt.Errorf("seek after writing resources: %w", err)
		}
	} else {
		// This case means FlagHasResources is NOT set, and len(state.Resources) should be 0.
		// The currentFilePos (which is after the previous section, e.g., Strings)
		// should be exactly where the (empty) Resource section was calculated to start.
		if uint32(currentFilePos) != state.ResourceOffset {
			// If this condition is met, it means the previous section (Strings) did not end
			// where calculateOffsetsAndSizes expected it to end. The discrepancy lies there.
			return fmt.Errorf("file position %d != ResourceOffset %d (no resources flag; indicates previous section ended at an unexpected position)", currentFilePos, state.ResourceOffset)
		}

		// If we are here, it means currentFilePos == state.ResourceOffset.
		// Since there are no resources and this is the last *data* section before TotalSize is checked:
		// - ResourceOffset should effectively point to the end of the file if no other conceptual (empty) sections follow.
		// - TotalSize should be equal to ResourceOffset.
		if state.ResourceOffset != state.TotalSize {
			// This warning indicates that TotalSize was calculated to be different from ResourceOffset,
			// even though the resource section is empty. This might be okay if there are other
			// *conceptual* empty sections after resources that TotalSize accounts for,
			// but in your current spec, Resources is the last data table.
			// The most important check for this 'else' branch is the one above:
			// that currentFilePos (end of strings) == state.ResourceOffset.
			log.Printf("Debug: No resources. ResourceOffset (%d) and TotalSize (%d) differ. currentFilePos is %d.", state.ResourceOffset, state.TotalSize, currentFilePos)
		}
		// No actual data is written for the resource section if FlagHasResources is not set.
		// currentFilePos remains unchanged here.
	}
	// --- Final Flush and Size Verification ---
	err = writer.Flush()
	if err != nil {
		return fmt.Errorf("final flush: %w", err)
	}
	finalFileSize, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("get final file size: %w", err)
	}
	if uint32(finalFileSize) != state.TotalSize {
		return fmt.Errorf("final write size mismatch: actual %d != calculated total %d", finalFileSize, state.TotalSize)
	}

	log.Printf("   Successfully wrote %d bytes.\n", finalFileSize)
	return nil
}

// --- Helper functions for writing parts of an element block ---

func writeElementHeader(w *bufio.Writer, el *Element) error {
	el.PropertyCount = uint8(len(el.KrbProperties))
	el.CustomPropCount = uint8(len(el.KrbCustomProperties))
	el.EventCount = uint8(len(el.KrbEvents))
	// el.ChildCount is set by resolver for main tree, or specifically for templates during template write
	el.AnimationCount = 0

	var err error
	err = writeUint8(w, el.Type); if err != nil { return err }
	err = writeUint8(w, el.IDStringIndex); if err != nil { return err }
	err = writeUint16(w, el.PosX); if err != nil { return err }
	err = writeUint16(w, el.PosY); if err != nil { return err }
	err = writeUint16(w, el.Width); if err != nil { return err }
	err = writeUint16(w, el.Height); if err != nil { return err }
	err = writeUint8(w, el.Layout); if err != nil { return err }
	err = writeUint8(w, el.StyleID); if err != nil { return err }
	err = writeUint8(w, el.PropertyCount); if err != nil { return err }
	err = writeUint8(w, el.ChildCount); if err != nil { return err }
	err = writeUint8(w, el.EventCount); if err != nil { return err }
	err = writeUint8(w, el.AnimationCount); if err != nil { return err }
	err = writeUint8(w, el.CustomPropCount); if err != nil { return err }
	return nil
}

func writeElementProperties(w *bufio.Writer, props []KrbProperty, logPrefix string) error {
	for i, prop := range props {
		var err error
		err = writeUint8(w, prop.PropertyID)
		if err != nil { return fmt.Errorf("%s #%d PropID: %w", logPrefix, i, err)}
		err = writeUint8(w, prop.ValueType)
		if err != nil { return fmt.Errorf("%s #%d ValType: %w", logPrefix, i, err)}
		err = writeUint8(w, prop.Size)
		if err != nil { return fmt.Errorf("%s #%d Size: %w", logPrefix, i, err)}
		if prop.Size > 0 {
			if prop.Value == nil {
				return fmt.Errorf("%s #%d nil value for size %d", logPrefix, i, prop.Size)
			}
			n, writeErr := w.Write(prop.Value)
			if writeErr != nil {
				return fmt.Errorf("%s #%d write value: %w", logPrefix, i, writeErr)
			}
			if n != int(prop.Size) {
				return fmt.Errorf("%s #%d short write: %d/%d", logPrefix, i, n, prop.Size)
			}
		}
	}
	return nil
}

func writeElementCustomProperties(w *bufio.Writer, props []KrbCustomProperty, logPrefix string) error {
	for i, prop := range props {
		var err error
		err = writeUint8(w, prop.KeyIndex)
		if err != nil { return fmt.Errorf("%s #%d KeyIdx: %w", logPrefix, i, err)}
		err = writeUint8(w, prop.ValueType)
		if err != nil { return fmt.Errorf("%s #%d ValType: %w", logPrefix, i, err)}
		err = writeUint8(w, prop.Size)
		if err != nil { return fmt.Errorf("%s #%d Size: %w", logPrefix, i, err)}
		if prop.Size > 0 {
			if prop.Value == nil {
				return fmt.Errorf("%s #%d nil value for size %d", logPrefix, i, prop.Size)
			}
			n, writeErr := w.Write(prop.Value)
			if writeErr != nil {
				return fmt.Errorf("%s #%d write value: %w", logPrefix, i, writeErr)
			}
			if n != int(prop.Size) {
				return fmt.Errorf("%s #%d short write: %d/%d", logPrefix, i, n, prop.Size)
			}
		}
	}
	return nil
}

func writeElementEvents(w *bufio.Writer, events []KrbEvent) error {
	for i, event := range events {
		var err error
		err = writeUint8(w, event.EventType)
		if err != nil { return fmt.Errorf("Event #%d type: %w", i, err) }
		err = writeUint8(w, event.CallbackID)
		if err != nil { return fmt.Errorf("Event #%d cbID: %w", i, err) }
	}
	return nil
}

// getBinaryDefaultValue retrieves the binary data and its size for a component property's default value.
func getBinaryDefaultValue(state *CompilerState, valueStr string, hint uint8) (data []byte, size uint8, err error) {
	if valueStr == "" {
		return nil, 0, nil
	}
	// parseKryValueToKrbBytes is defined in resolver.go and needs access to CompilerState
	// for tasks like adding default strings to the string table.
	// The propKey and lineNum are illustrative for the call context within parseKryValueToKrbBytes.
	data, _, size, err = parseKryValueToKrbBytes(state, valueStr, hint, "compDefDefaultSerialization", 0)
	return data, size, err
}

// getTemplateElementIndices collects all element indices that form a component template's structure.
// It starts with rootTemplateElIdx and recursively follows SourceChildrenIndices of elements
// that are confirmed to be part of the same template definition.
// The returned slice is sorted by SelfIndex to ensure a consistent processing/writing order.
func getTemplateElementIndices(state *CompilerState, rootTemplateElIdx int) []int {
    var indices []int
    visited := make(map[int]bool)
    var collect func(elIdx int)

    collect = func(elIdx int) {
        if elIdx < 0 || elIdx >= len(state.Elements) || visited[elIdx] {
            return
        }
        
        el := &state.Elements[elIdx]

        // To be part of *this* template, an element must either be the designated root
        // or a descendant that is also marked as IsDefinitionRoot and whose chain of
        // IsDefinitionRoot parents leads back to this rootTemplateElIdx.
        // A simpler heuristic for flat template structures: it's the root or its parent was visited.
        isCorrectTemplatePart := false
        if elIdx == rootTemplateElIdx {
            isCorrectTemplatePart = true
        } else if el.IsDefinitionRoot && el.ParentIndex != -1 && visited[el.ParentIndex] {
            // If it's a definition part and its parent was part of this collection run, include it.
            isCorrectTemplatePart = true
        }


        if !isCorrectTemplatePart {
            return // Not considered part of this specific template's flattened structure
        }

        indices = append(indices, elIdx)
        visited[elIdx] = true

        for _, childIdx := range el.SourceChildrenIndices {
            if childIdx >= 0 && childIdx < len(state.Elements) {
                // Only recurse for children that are themselves part of *some* definition structure.
                // This is a safeguard to prevent jumping into the main tree.
                if state.Elements[childIdx].IsDefinitionRoot {
                     collect(childIdx)
                }
            }
        }
    }

    if rootTemplateElIdx >= 0 && rootTemplateElIdx < len(state.Elements) {
         // Ensure the starting point is actually a definition root.
         if state.Elements[rootTemplateElIdx].IsDefinitionRoot {
            collect(rootTemplateElIdx)
         } else {
            log.Printf("Error: Root index %d provided to getTemplateElementIndices is not itself a definition root.", rootTemplateElIdx)
         }
    }
    sort.Ints(indices) // Sort by original parse order (SelfIndex) for consistent writing
    return indices
}

// lineNumForError is a small helper to provide a line number context for errors
// during default value parsing for component definitions.
func lineNumForError(defStartLine, propIndex int) int {
	// This is an approximation. If ComponentPropertyDef stored its own line number, that would be better.
	return defStartLine + propIndex + 1 // +1 assuming Properties block content starts after its declaration line
}
