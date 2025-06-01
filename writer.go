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
	currentOffset := uint32(KRBHeaderSize) // Start after the main file header

	// --- 1. Elements Section Size (Main UI Tree Placeholders and Standard Elements ONLY) ---
	state.ElementOffset = currentOffset
	state.TotalElementDataSize = 0
	mainTreeElementCount := 0 // This count goes into the KRB File Header

	// log.Println("DEBUG_CALC_OFFSETS: Elements considered for main UI tree sizing:")
	for i := range state.Elements {
		el := &state.Elements[i]
		// Only include elements in the main UI tree (not parts of component definition templates)
		// for this section's size calculation and Element Count in the header.
		if el.IsDefinitionRoot {
			// log.Printf("  -> Skipping Template Element: Idx=%d, Name='%s'", i, el.SourceElementName)
			continue
		}

		mainTreeElementCount++
		el.AbsoluteOffset = currentOffset // Global offset for this main tree element/placeholder

		size := uint32(KRBElementHeaderSize) // Type, ID, PosX/Y, W/H, Layout, StyleID, Counts...
		// Standard Properties of the placeholder/element
		for _, prop := range el.KrbProperties {
			size += 3                 // PropertyID(1) + ValueType(1) + Size(1)
			size += uint32(prop.Size) // Value data
		}
		// Custom Properties of the placeholder (e.g., _componentName, instance props)
		for _, customProp := range el.KrbCustomProperties {
			size += 1                       // Key Index
			size += 1                       // Value Type
			size += 1                       // Value Size
			size += uint32(customProp.Size) // Value data
		}
		// Events on the placeholder/element
		size += uint32(len(el.KrbEvents)) * 2 // EventType(1) + CallbackID(1)
		// Children of the placeholder (from KRY usage tag)
		size += uint32(len(el.Children)) * 2 // RelativeOffsetToChild(2 bytes)

		el.CalculatedSize = size
		if size < KRBElementHeaderSize {
			return fmt.Errorf("internal: main element/placeholder %d ('%s') calculated size %d < header size %d", i, el.SourceElementName, size, KRBElementHeaderSize)
		}
		state.TotalElementDataSize += size
		currentOffset += size
		// log.Printf("  -> Sized Main Tree Element: Idx=%d, Name='%s', Size=%d, AbsOffset=%d", i, el.SourceElementName, size, el.AbsoluteOffset)
	}
	log.Printf("      Calculated Main UI Tree: %d elements, %d bytes data.", mainTreeElementCount, state.TotalElementDataSize)

	// --- 2. Styles Section Size ---
	state.StyleOffset = currentOffset
	state.TotalStyleDataSize = 0
	if (state.HeaderFlags & FlagHasStyles) != 0 {
		for i := range state.Styles {
			style := &state.Styles[i]
			// Size calculation for style.CalculatedSize should already be done in style_resolver.go
			if style.CalculatedSize < 3 { // Smallest possible style block (header only, 0 props)
				return fmt.Errorf("internal: style %d ('%s') calculated size %d < minimum 3 (was: %d)", i, style.SourceName, style.CalculatedSize, style.CalculatedSize)
			}
			state.TotalStyleDataSize += style.CalculatedSize
			currentOffset += style.CalculatedSize
		}
	}
	log.Printf("      Calculated Styles: %d styles, %d bytes data.", len(state.Styles), state.TotalStyleDataSize)

	// --- 3. Component Definition Table Section Size ---
	state.ComponentDefOffset = currentOffset
	state.TotalComponentDefDataSize = 0
	if (state.HeaderFlags & FlagHasComponentDefs) != 0 {
		for cdi := range state.ComponentDefs {
			def := &state.ComponentDefs[cdi]
			singleDefEntrySize := uint32(0)

			// Name Index of the component definition itself
			_, err := state.addString(def.Name) // Ensure name is in string table for index lookup
			if err != nil {
				return fmt.Errorf("CompDef '%s': error ensuring component name in string table for sizing: %w", def.Name, err)
			}
			singleDefEntrySize += 1 // Name Index (1 byte for the string table index)
			singleDefEntrySize += 1 // Property Def Count (1 byte)

			// Size of Property Definitions part
			for _, propDef := range def.Properties {
				_, err := state.addString(propDef.Name) // Ensure prop def name is in string table
				if err != nil {
					return fmt.Errorf("CompDef '%s' prop '%s': error ensuring prop name in string table for sizing: %w", def.Name, propDef.Name, err)
				}
				singleDefEntrySize += 1 // Property Name Index (1 byte)
				singleDefEntrySize += 1 // Value Type Hint (1 byte)
				singleDefEntrySize += 1 // Default Value Size (1 byte)

				// Sizing for Default Value Data (ensures strings for defaults are added to string table *now*)
				_, defaultValDataSize, parseErr := getBinaryDefaultValue(state, propDef.DefaultValueStr, propDef.ValueTypeHint)
				if parseErr != nil {
					return fmt.Errorf("compdef '%s' prop '%s': error sizing default value '%s' for KRB: %w", def.Name, propDef.Name, propDef.DefaultValueStr, parseErr)
				}
				singleDefEntrySize += uint32(defaultValDataSize)
			}

			// Size of the Root Element Template part
			if def.DefinitionRootElementIndex < 0 || def.DefinitionRootElementIndex >= len(state.Elements) {
				return fmt.Errorf("compdef '%s': invalid root element index %d for its template", def.Name, def.DefinitionRootElementIndex)
			}

			templateElementsIndices := getTemplateElementIndices(state, def.DefinitionRootElementIndex)
			if len(templateElementsIndices) == 0 {
				return fmt.Errorf("compdef '%s': root element template is empty or could not be identified", def.Name)
			}

			templateTotalDataSize := uint32(0)
			// Pre-calculate absolute offsets *within this component definition entry* for template elements
			// to correctly calculate relative child offsets for the template.
			currentTemplateElementOffset := uint32(0)      // Relative to start of Root Element Template data
			templateElementOffsets := make(map[int]uint32) // Map from el.SelfIndex to its offset within this template blob

			for _, tplElIdx := range templateElementsIndices { // Iterate in sorted order
				tplEl := &state.Elements[tplElIdx]
				templateElementOffsets[tplEl.SelfIndex] = currentTemplateElementOffset

				tempSize := uint32(KRBElementHeaderSize)
				for _, prop := range tplEl.KrbProperties { // Standard properties of the template element
					tempSize += 3 + uint32(prop.Size)
				}
				// Custom Props and Events should be 0 for template elements as per spec
				if tplEl.CustomPropCount > 0 || tplEl.EventCount > 0 {
					log.Printf("Warning: CompDef '%s' template element '%s' (idx %d) has CustomPropCount=%d or EventCount=%d. These should be 0 for templates.", def.Name, tplEl.SourceElementName, tplEl.SelfIndex, tplEl.CustomPropCount, tplEl.EventCount)
				}

				// ChildCount for template elements refers to children *within this template*.
				// The actual offsets are relative to THIS template's root element.
				actualTemplateChildCount := 0
				for _, childRefIdx := range tplEl.SourceChildrenIndices {
					// Check if this child is part of *this specific template's element list*
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
				tempSize += uint32(actualTemplateChildCount) * 2 // ChildOffset(2 bytes) for each *template child*

				tplEl.CalculatedSize = tempSize // Size of this *one* element within the template definition
				templateTotalDataSize += tempSize
				currentTemplateElementOffset += tempSize
			}
			// Store these relative offsets for use during writing the template
			def.InternalTemplateElementOffsets = templateElementOffsets

			singleDefEntrySize += templateTotalDataSize
			def.CalculatedSize = singleDefEntrySize // Total size for this single component definition entry
			state.TotalComponentDefDataSize += singleDefEntrySize
			currentOffset += singleDefEntrySize
		}
	}
	log.Printf("      Calculated Component Defs: %d defs, %d bytes data.", len(state.ComponentDefs), state.TotalComponentDefDataSize)

	// --- 4. Animations Section Size ---
	state.AnimOffset = currentOffset // Even if 0 anims, offset points to where it would start
	// No data if 0 anims. state.TotalAnimationDataSize would be 0.
	log.Printf("      Calculated Animations: 0 anims, 0 bytes data.")

	// --- 5. Strings Section Size ---
	state.StringOffset = currentOffset
	stringSectionHeaderSize := uint32(2) // String Count (uint16) - Always present in section
	state.TotalStringDataSize = 0
	if len(state.Strings) > 0 { // String data only if strings exist
		for _, s := range state.Strings {
			if s.Length > 255 { // KRB String length is 1 byte
				return fmt.Errorf("string '%s...' (idx %d) length %d exceeds max 255 for KRB", s.Text[:min(len(s.Text), 20)], s.Index, s.Length)
			}
			state.TotalStringDataSize += 1 + uint32(s.Length) // LengthByte(1) + UTF-8 Bytes
		}
	}
	currentOffset += stringSectionHeaderSize + state.TotalStringDataSize
	log.Printf("      Calculated Strings: %d strings, %d bytes data (+%d for count field).", len(state.Strings), state.TotalStringDataSize, stringSectionHeaderSize)

	// --- 6. Resources Section Size ---
	state.ResourceOffset = currentOffset
	resourceSectionHeaderSize := uint32(0)
	state.TotalResourceTableSize = 0
	if (state.HeaderFlags&FlagHasResources) != 0 && len(state.Resources) > 0 {
		resourceSectionHeaderSize = 2 // Resource Count (uint16) field is present
		for i := range state.Resources {
			res := &state.Resources[i]
			// res.CalculatedSize should be set during addResource or a dedicated resource sizing pass
			if res.CalculatedSize == 0 { // Fallback if not pre-calculated
				if res.Format == ResFormatExternal {
					res.CalculatedSize = 4
				} else {
					return fmt.Errorf("unsupported/unsized resource format %d for resource %d ('%s') during offset calculation", res.Format, i, state.Strings[res.NameIndex].Text)
				}
			}
			state.TotalResourceTableSize += res.CalculatedSize
		}
	}
	currentOffset += resourceSectionHeaderSize + state.TotalResourceTableSize
	log.Printf("      Calculated Resources: %d resources, %d bytes data (+%d for count field).", len(state.Resources), state.TotalResourceTableSize, resourceSectionHeaderSize)

	state.TotalSize = currentOffset
	log.Printf("      Total calculated KRB file size: %d bytes\n", state.TotalSize)
	return nil
}

// --- Pass 3: Write KRB File (KRB v0.4) ---

func (state *CompilerState) writeKrbFile(filePath string) error {
	log.Printf("Pass 3: Writing KRB v%d.%d binary to '%s'...\n", KRBVersionMajor, KRBVersionMinor, filePath)

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("error creating output file '%s': %w", filePath, err)
	}
	defer file.Close()
	writer := bufio.NewWriter(file)

	// --- Write KRB File Header (48 bytes for v0.4) ---
	if _, err = writer.WriteString(KRBMagic); err != nil {
		return fmt.Errorf("write magic: %w", err)
	}
	versionField := (uint16(KRBVersionMinor) << 8) | uint16(KRBVersionMajor)
	if err = writeUint16(writer, versionField); err != nil {
		return fmt.Errorf("write version: %w", err)
	}
	if err = writeUint16(writer, state.HeaderFlags); err != nil {
		return fmt.Errorf("write flags: %w", err)
	}

	mainTreeElementCount := 0
	for i := range state.Elements {
		if !state.Elements[i].IsDefinitionRoot {
			mainTreeElementCount++
		}
	}
	if err = writeUint16(writer, uint16(mainTreeElementCount)); err != nil {
		return fmt.Errorf("write element count: %w", err)
	}
	if err = writeUint16(writer, uint16(len(state.Styles))); err != nil {
		return fmt.Errorf("write style count: %w", err)
	}
	if err = writeUint16(writer, uint16(len(state.ComponentDefs))); err != nil {
		return fmt.Errorf("write component def count: %w", err)
	}
	if err = writeUint16(writer, uint16(0)); err != nil {
		return fmt.Errorf("write animation count: %w", err)
	} // Animation Count
	if err = writeUint16(writer, uint16(len(state.Strings))); err != nil {
		return fmt.Errorf("write string count: %w", err)
	}
	if err = writeUint16(writer, uint16(len(state.Resources))); err != nil {
		return fmt.Errorf("write resource count: %w", err)
	}

	if err = writeUint32(writer, state.ElementOffset); err != nil {
		return fmt.Errorf("write element offset: %w", err)
	}
	if err = writeUint32(writer, state.StyleOffset); err != nil {
		return fmt.Errorf("write style offset: %w", err)
	}
	if err = writeUint32(writer, state.ComponentDefOffset); err != nil {
		return fmt.Errorf("write component def offset: %w", err)
	}
	if err = writeUint32(writer, state.AnimOffset); err != nil {
		return fmt.Errorf("write animation offset: %w", err)
	}
	if err = writeUint32(writer, state.StringOffset); err != nil {
		return fmt.Errorf("write string offset: %w", err)
	}
	if err = writeUint32(writer, state.ResourceOffset); err != nil {
		return fmt.Errorf("write resource offset: %w", err)
	}
	if err = writeUint32(writer, state.TotalSize); err != nil {
		return fmt.Errorf("write total size: %w", err)
	}

	// --- Pad to Element Offset if necessary ---
	if err = writer.Flush(); err != nil {
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
		// log.Printf("    Padding KRB header with %d zero bytes.\n", paddingNeeded)
		if _, err = writer.Write(make([]byte, paddingNeeded)); err != nil {
			return fmt.Errorf("write header padding: %w", err)
		}
	}
	if err = writer.Flush(); err != nil {
		return fmt.Errorf("flush after padding: %w", err)
	} // Ensure padding is written
	currentFilePos, _ = file.Seek(0, io.SeekCurrent) // Update currentFilePos

	// --- Write Element Blocks (Main UI Tree Placeholders and Standard Elements ONLY) ---
	log.Printf("    Writing %d main UI tree elements at offset %d (actual: %d)\n", mainTreeElementCount, state.ElementOffset, currentFilePos)
	if uint32(currentFilePos) != state.ElementOffset {
		return fmt.Errorf("file pos %d != ElementOffset %d before writing main elements", currentFilePos, state.ElementOffset)
	}

	firstElementWritten := false
	for i := range state.Elements {
		el := &state.Elements[i]
		if el.IsDefinitionRoot { // Skip elements that are part of a component template
			continue
		}

		// Sanity check for App element as the first element if FLAG_HAS_APP is set
		if !firstElementWritten {
			if (state.HeaderFlags&FlagHasApp) != 0 && el.Type != ElemTypeApp {
				return fmt.Errorf("KRB_WRITE_ERROR: FLAG_HAS_APP is set, but the first main tree element is '%s' (Type 0x%02X), not App. Index: %d", el.SourceElementName, el.Type, i)
			}
			firstElementWritten = true
		}

		startPos := currentFilePos
		if uint32(currentFilePos) != el.AbsoluteOffset { // Ensure calculated absolute offset matches write position
			return fmt.Errorf("main elem %d ('%s') offset mismatch: current write pos %d != expected abs_offset %d", i, el.SourceElementName, currentFilePos, el.AbsoluteOffset)
		}

		if err = writeElementHeader(writer, el); err != nil {
			return fmt.Errorf("main elem %d ('%s') header write: %w", i, el.SourceElementName, err)
		}
		if err = writeElementProperties(writer, el.KrbProperties, "StdP"); err != nil {
			return fmt.Errorf("main elem %d ('%s') std props: %w", i, el.SourceElementName, err)
		}
		if err = writeElementCustomProperties(writer, el.KrbCustomProperties, "CstP"); err != nil {
			return fmt.Errorf("main elem %d ('%s') custom props: %w", i, el.SourceElementName, err)
		}
		if err = writeElementEvents(writer, el.KrbEvents); err != nil {
			return fmt.Errorf("main elem %d ('%s') events: %w", i, el.SourceElementName, err)
		}

		// Write Child References for this placeholder/standard element
		// Children are those from the KRY usage tag (for placeholders) or direct KRY children (for standard elements)
		for cIdx, childInstance := range el.Children { // el.Children should be populated by resolver correctly
			// childInstance.AbsoluteOffset is the global offset where that child (which could be another placeholder) WILL BE written.
			// el.AbsoluteOffset is where the current parent `el` header started.
			relativeOffset := childInstance.AbsoluteOffset - el.AbsoluteOffset
			if relativeOffset > math.MaxUint16 || (relativeOffset <= 0 && childInstance.AbsoluteOffset != el.AbsoluteOffset) { // relativeOffset can be 0 if child is empty and written immediately after
				return fmt.Errorf("main elem %d ('%s') child #%d ('%s') invalid relative offset %d (child_abs=%d, parent_abs=%d)", i, el.SourceElementName, cIdx, childInstance.SourceElementName, relativeOffset, childInstance.AbsoluteOffset, el.AbsoluteOffset)
			}
			if err = writeUint16(writer, uint16(relativeOffset)); err != nil {
				return fmt.Errorf("main elem %d child #%d rel offset: %w", i, cIdx, err)
			}
		}

		if err = writer.Flush(); err != nil {
			return fmt.Errorf("main elem %d ('%s') flush: %w", i, el.SourceElementName, err)
		}
		currentFilePos, err = file.Seek(0, io.SeekCurrent)
		if err != nil {
			return fmt.Errorf("main elem %d ('%s') seek: %w", i, el.SourceElementName, err)
		}

		bytesWrittenThisElement := uint32(currentFilePos - startPos)
		if bytesWrittenThisElement != el.CalculatedSize {
			return fmt.Errorf("main elem %d ('%s') size mismatch: wrote %d, expected %d", i, el.SourceElementName, bytesWrittenThisElement, el.CalculatedSize)
		}
	}

	// --- Write Style Blocks ---
	log.Printf("    Writing %d styles at offset %d (actual: %d)\n", len(state.Styles), state.StyleOffset, currentFilePos)
	if (state.HeaderFlags & FlagHasStyles) != 0 {
		if uint32(currentFilePos) != state.StyleOffset {
			return fmt.Errorf("file pos %d != StyleOffset %d before writing styles", currentFilePos, state.StyleOffset)
		}
		for i := range state.Styles {
			style := &state.Styles[i]
			startPos := currentFilePos
			if err = writeUint8(writer, style.ID); err != nil {
				return fmt.Errorf("style %d ID: %w", i, err)
			}
			if err = writeUint8(writer, style.NameIndex); err != nil {
				return fmt.Errorf("style %d NameIdx: %w", i, err)
			}
			if err = writeUint8(writer, uint8(len(style.Properties))); err != nil {
				return fmt.Errorf("style %d PropCount: %w", i, err)
			}
			if err = writeElementProperties(writer, style.Properties, "StyleP"); err != nil {
				return fmt.Errorf("style %d ('%s') props: %w", i, style.SourceName, err)
			}

			if err = writer.Flush(); err != nil {
				return fmt.Errorf("style %d ('%s') flush: %w", i, style.SourceName, err)
			}
			currentFilePos, err = file.Seek(0, io.SeekCurrent)
			if err != nil {
				return fmt.Errorf("style %d ('%s') seek: %w", i, style.SourceName, err)
			}
			bytesWrittenThisStyle := uint32(currentFilePos - startPos)
			if bytesWrittenThisStyle != style.CalculatedSize {
				return fmt.Errorf("style %d ('%s') size mismatch: wrote %d, expected %d", i, style.SourceName, bytesWrittenThisStyle, style.CalculatedSize)
			}
		}
	}

	// --- Write Component Definition Table ---
	log.Printf("    Writing %d component definitions at offset %d (actual: %d)\n", len(state.ComponentDefs), state.ComponentDefOffset, currentFilePos)
	if (state.HeaderFlags & FlagHasComponentDefs) != 0 {
		if uint32(currentFilePos) != state.ComponentDefOffset {
			return fmt.Errorf("file pos %d != ComponentDefOffset %d before writing comp defs", currentFilePos, state.ComponentDefOffset)
		}
		for cdi := range state.ComponentDefs {
			def := &state.ComponentDefs[cdi]
			startPosDef := currentFilePos // For verifying size of this whole definition entry

			// Write Component Definition Name Index
			nameIdx, strErr := state.getStringIndex(def.Name) // getStringIndex should be robust
			if !strErr {
				return fmt.Errorf("CompDef '%s': name not found in string table during write", def.Name)
			}
			if err = writeUint8(writer, nameIdx); err != nil {
				return fmt.Errorf("CompDef '%s' write name index: %w", def.Name, err)
			}

			// Write Property Definition Count
			if err = writeUint8(writer, uint8(len(def.Properties))); err != nil {
				return fmt.Errorf("CompDef '%s' write prop def count: %w", def.Name, err)
			}

			// Write each Property Definition
			for pdi, propDef := range def.Properties {
				propNameIdx, strErr2 := state.getStringIndex(propDef.Name)
				if !strErr2 {
					return fmt.Errorf("CompDef '%s' prop #%d ('%s'): name not found in string table", def.Name, pdi, propDef.Name)
				}
				if err = writeUint8(writer, propNameIdx); err != nil {
					return fmt.Errorf("CompDef '%s' prop '%s' write name idx: %w", def.Name, propDef.Name, err)
				}
				if err = writeUint8(writer, propDef.ValueTypeHint); err != nil {
					return fmt.Errorf("CompDef '%s' prop '%s' write type hint: %w", def.Name, propDef.Name, err)
				}

				// Get binary data for default value (this ensures strings are added to table if needed by getBinaryDefaultValue)
				valueBytes, valueSize, parseErr := getBinaryDefaultValue(state, propDef.DefaultValueStr, propDef.ValueTypeHint)
				if parseErr != nil {
					return fmt.Errorf("CompDef '%s' prop '%s' default value '%s' (hint %d) parse error for write: %w", def.Name, propDef.Name, propDef.DefaultValueStr, propDef.ValueTypeHint, parseErr)
				}
				if err = writeUint8(writer, valueSize); err != nil {
					return fmt.Errorf("CompDef '%s' prop '%s' write default value size: %w", def.Name, propDef.Name, err)
				}
				if valueSize > 0 {
					if _, err = writer.Write(valueBytes); err != nil {
						return fmt.Errorf("CompDef '%s' prop '%s' write default value data: %w", def.Name, propDef.Name, err)
					}
				}
			}

			// Write the Root Element Template
			templateElementsIndices := getTemplateElementIndices(state, def.DefinitionRootElementIndex)
			if len(templateElementsIndices) == 0 {
				return fmt.Errorf("CompDef '%s': template is empty, cannot write. RootIdx: %d", def.Name, def.DefinitionRootElementIndex)
			}

			// The first element in templateElementsIndices is the root of *this* template.
			// Its pre-calculated offset within def.InternalTemplateElementOffsets should be 0.
			offsetOfThisTemplateRootWithinBlob := def.InternalTemplateElementOffsets[templateElementsIndices[0]]
			if offsetOfThisTemplateRootWithinBlob != 0 {
				log.Printf("Warning: CompDef '%s' root template element (idx %d) has non-zero internal offset %d. This might be unexpected.", def.Name, templateElementsIndices[0], offsetOfThisTemplateRootWithinBlob)
			}

			for _, tplElIdx := range templateElementsIndices { // Elements are sorted by SelfIndex by getTemplateElementIndices
				tplEl := &state.Elements[tplElIdx]

				// Write Element Header for this template element
				// Note: Counts for CustomProps and Events should be 0 for template elements.
				// ChildCount is for children *within this template*.
				tplEl.CustomPropCount = 0 // Ensure for template
				tplEl.EventCount = 0      // Ensure for template

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
				tplEl.ChildCount = uint8(actualTemplateChildCount)

				if err = writeElementHeader(writer, tplEl); err != nil {
					return fmt.Errorf("TplElem '%s' (in CompDef '%s') header: %w", tplEl.SourceElementName, def.Name, err)
				}
				if err = writeElementProperties(writer, tplEl.KrbProperties, fmt.Sprintf("TplElem '%s' Prop", tplEl.SourceElementName)); err != nil {
					return err
				}
				// No custom props or events for template elements.

				// Write Child Relative Offsets for template children
				// These offsets are relative to the start of *this template's root element header*
				// within this specific Component Definition Entry.
				for childNum, childIdxInState := range tplEl.SourceChildrenIndices {
					isChildInThisTemplate := false
					for _, tei := range templateElementsIndices {
						if childIdxInState == tei {
							isChildInThisTemplate = true
							break
						}
					}
					if !isChildInThisTemplate {
						continue
					} // Skip children not part of this template definition

					childElInTemplate := &state.Elements[childIdxInState]

					// Get the pre-calculated offset of this child *within the template blob*
					offsetOfChildWithinBlob, okChild := def.InternalTemplateElementOffsets[childElInTemplate.SelfIndex]
					if !okChild {
						return fmt.Errorf("CompDef '%s', TplParent '%s': offset for template child '%s' (idx %d) not found in precalculated map", def.Name, tplEl.SourceElementName, childElInTemplate.SourceElementName, childElInTemplate.SelfIndex)
					}

					// The offset written is from the start of the *current tplEl's header* to the start of the *childElInTemplate's header*
					// *within the context of this template's sequential write*.
					// currentTplElOffsetWithinBlob is the offset of tplEl itself relative to the start of the template blob.
					currentTplElOffsetWithinBlob := def.InternalTemplateElementOffsets[tplEl.SelfIndex]
					relativeOffset := offsetOfChildWithinBlob - currentTplElOffsetWithinBlob

					if relativeOffset > math.MaxUint16 || (relativeOffset <= 0 && offsetOfChildWithinBlob != currentTplElOffsetWithinBlob) {
						return fmt.Errorf("CompDef '%s', TplParent '%s': template child '%s' invalid relative offset %d (child_blob_offset=%d, parent_blob_offset=%d)",
							def.Name, tplEl.SourceElementName, childElInTemplate.SourceElementName, relativeOffset, offsetOfChildWithinBlob, currentTplElOffsetWithinBlob)
					}
					if err = writeUint16(writer, uint16(relativeOffset)); err != nil {
						return fmt.Errorf("CompDef '%s', TplParent '%s' child #%d ('%s') write rel offset: %w", def.Name, tplEl.SourceElementName, childNum, childElInTemplate.SourceElementName, err)
					}
				}
			}

			if err = writer.Flush(); err != nil {
				return fmt.Errorf("CompDef '%s' flush after template: %w", def.Name, err)
			}
			currentFilePos, err = file.Seek(0, io.SeekCurrent)
			if err != nil {
				return fmt.Errorf("CompDef '%s' seek after template: %w", def.Name, err)
			}

			bytesWrittenForThisDef := uint32(currentFilePos - startPosDef)
			if bytesWrittenForThisDef != def.CalculatedSize {
				log.Printf("Warning: CompDef '%s' size mismatch: wrote %d, expected %d. Review CalculatedSize logic.", def.Name, bytesWrittenForThisDef, def.CalculatedSize)
			}
		}
	}

	// --- Write Animation Table Section ---
	log.Printf("    Writing 0 animations at offset %d (actual: %d)\n", state.AnimOffset, currentFilePos)
	if uint32(currentFilePos) != state.AnimOffset {
		// Allow if no anims and it's where the next section (strings) starts
		if !(state.AnimOffset == state.StringOffset && (state.HeaderFlags&FlagHasAnimations) == 0) {
			return fmt.Errorf("file pos %d != AnimOffset %d before writing (empty) animation section", currentFilePos, state.AnimOffset)
		}
	}
	// No data to write if Animation Count is 0.

	// --- Write String Table Section ---
	log.Printf("    Writing %d strings at offset %d (actual: %d)\n", len(state.Strings), state.StringOffset, currentFilePos)
	if uint32(currentFilePos) != state.StringOffset {
		return fmt.Errorf("file pos %d != StringOffset %d before writing strings", currentFilePos, state.StringOffset)
	}
	if err = writeUint16(writer, uint16(len(state.Strings))); err != nil {
		return fmt.Errorf("write string count: %w", err)
	} // String Count field
	for i := range state.Strings {
		s := &state.Strings[i]
		if err = writeUint8(writer, uint8(s.Length)); err != nil {
			return fmt.Errorf("Str%d ('%s') write len: %w", i, s.Text, err)
		} // Length byte
		if s.Length > 0 {
			if _, err = writer.WriteString(s.Text); err != nil {
				return fmt.Errorf("Str%d ('%s') write text: %w", i, s.Text, err)
			}
		}
	}
	if err = writer.Flush(); err != nil {
		return fmt.Errorf("flush strings: %w", err)
	}
	currentFilePos, err = file.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("seek after strings: %w", err)
	}

	// --- Write Resource Table Section ---
	log.Printf("    Writing %d resources at offset %d (actual: %d)\n", len(state.Resources), state.ResourceOffset, currentFilePos)
	if (state.HeaderFlags&FlagHasResources) != 0 && len(state.Resources) > 0 {
		if uint32(currentFilePos) != state.ResourceOffset {
			return fmt.Errorf("file pos %d != ResourceOffset %d before writing resource section header", currentFilePos, state.ResourceOffset)
		}
		if err = writeUint16(writer, uint16(len(state.Resources))); err != nil {
			return fmt.Errorf("write resource count: %w", err)
		} // Resource Count field
		for _, r := range state.Resources {
			if err = writeUint8(writer, r.Type); err != nil {
				return fmt.Errorf("write res type: %w", err)
			}
			if err = writeUint8(writer, r.NameIndex); err != nil {
				return fmt.Errorf("write res name idx: %w", err)
			}
			if err = writeUint8(writer, r.Format); err != nil {
				return fmt.Errorf("write res format: %w", err)
			}
			if r.Format == ResFormatExternal {
				if err = writeUint8(writer, r.DataStringIndex); err != nil {
					return fmt.Errorf("write res data str idx: %w", err)
				}
			} else {
				return fmt.Errorf("unsupported resource format %d during write for resource '%s'", r.Format, state.Strings[r.NameIndex].Text)
			}
		}
	} else { // No resources or flag not set
		if uint32(currentFilePos) != state.ResourceOffset {
			return fmt.Errorf("file pos %d != ResourceOffset %d (no resources expected, indicates prev section end error)", currentFilePos, state.ResourceOffset)
		}
		// If ResourceOffset is different from TotalSize here, it means TotalSize implies more (empty) sections.
		// This is fine as long as currentFilePos matches where the (empty) resource section starts.
	}
	if err = writer.Flush(); err != nil {
		return fmt.Errorf("flush after resources (or empty res section): %w", err)
	}
	currentFilePos, _ = file.Seek(0, io.SeekCurrent) // Update for final check

	// --- Final Flush and Size Verification ---
	finalFileSize := currentFilePos
	if uint32(finalFileSize) != state.TotalSize {
		return fmt.Errorf("final write size mismatch: actual %d != calculated total %d", finalFileSize, state.TotalSize)
	}

	log.Printf("   Successfully wrote %d bytes.\n", finalFileSize)
	return nil
}

// --- Helper functions for writing parts of an element block ---

// writeElementHeader writes the 17-byte KRB element header.
func writeElementHeader(w *bufio.Writer, el *Element) error {
	// Counts should be finalized on `el` before calling this
	var err error
	if err = writeUint8(w, el.Type); err != nil {
		return err
	}
	if err = writeUint8(w, el.IDStringIndex); err != nil {
		return err
	}
	if err = writeUint16(w, el.PosX); err != nil {
		return err
	}
	if err = writeUint16(w, el.PosY); err != nil {
		return err
	}
	if err = writeUint16(w, el.Width); err != nil {
		return err
	}
	if err = writeUint16(w, el.Height); err != nil {
		return err
	}
	if err = writeUint8(w, el.Layout); err != nil {
		return err
	}
	if err = writeUint8(w, el.StyleID); err != nil {
		return err
	}
	if err = writeUint8(w, el.PropertyCount); err != nil {
		return err
	} // Standard KRB Property Count
	if err = writeUint8(w, el.ChildCount); err != nil {
		return err
	} // KRB Child Count
	if err = writeUint8(w, el.EventCount); err != nil {
		return err
	}
	if err = writeUint8(w, el.AnimationCount); err != nil {
		return err
	}
	if err = writeUint8(w, el.CustomPropCount); err != nil {
		return err
	} // KRB Custom Property Count
	return nil
}

// writeElementProperties writes standard KRB properties.
func writeElementProperties(w *bufio.Writer, props []KrbProperty, logPrefix string) error {
	for i, prop := range props {
		var err error
		if err = writeUint8(w, prop.PropertyID); err != nil {
			return fmt.Errorf("%s #%d PropID 0x%X: %w", logPrefix, i, prop.PropertyID, err)
		}
		if err = writeUint8(w, prop.ValueType); err != nil {
			return fmt.Errorf("%s #%d ValType 0x%X: %w", logPrefix, i, prop.ValueType, err)
		}
		if err = writeUint8(w, prop.Size); err != nil {
			return fmt.Errorf("%s #%d Size %d: %w", logPrefix, i, prop.Size, err)
		}
		if prop.Size > 0 {
			if prop.Value == nil {
				return fmt.Errorf("%s #%d PropID 0x%X: nil value for data size %d", logPrefix, i, prop.PropertyID, prop.Size)
			}
			n, writeErr := w.Write(prop.Value)
			if writeErr != nil {
				return fmt.Errorf("%s #%d PropID 0x%X write value: %w", logPrefix, i, prop.PropertyID, writeErr)
			}
			if n != int(prop.Size) {
				return fmt.Errorf("%s #%d PropID 0x%X short write: %d/%d", logPrefix, i, prop.PropertyID, n, prop.Size)
			}
		}
	}
	return nil
}

// writeElementCustomProperties writes custom KRB properties.
func writeElementCustomProperties(w *bufio.Writer, props []KrbCustomProperty, logPrefix string) error {
	for i, prop := range props {
		var err error
		if err = writeUint8(w, prop.KeyIndex); err != nil {
			return fmt.Errorf("%s #%d KeyIdx %d: %w", logPrefix, i, prop.KeyIndex, err)
		}
		if err = writeUint8(w, prop.ValueType); err != nil {
			return fmt.Errorf("%s #%d ValType 0x%X: %w", logPrefix, i, prop.ValueType, err)
		}
		if err = writeUint8(w, prop.Size); err != nil {
			return fmt.Errorf("%s #%d Size %d: %w", logPrefix, i, prop.Size, err)
		}
		if prop.Size > 0 {
			if prop.Value == nil {
				return fmt.Errorf("%s #%d KeyIdx %d: nil value for data size %d", logPrefix, i, prop.KeyIndex, prop.Size)
			}
			n, writeErr := w.Write(prop.Value)
			if writeErr != nil {
				return fmt.Errorf("%s #%d KeyIdx %d write value: %w", logPrefix, i, prop.KeyIndex, writeErr)
			}
			if n != int(prop.Size) {
				return fmt.Errorf("%s #%d KeyIdx %d short write: %d/%d", logPrefix, i, prop.KeyIndex, n, prop.Size)
			}
		}
	}
	return nil
}

// writeElementEvents writes KRB events.
func writeElementEvents(w *bufio.Writer, events []KrbEvent) error {
	for i, event := range events {
		var err error
		if err = writeUint8(w, event.EventType); err != nil {
			return fmt.Errorf("Event #%d type 0x%X: %w", i, event.EventType, err)
		}
		if err = writeUint8(w, event.CallbackID); err != nil {
			return fmt.Errorf("Event #%d cbID %d: %w", i, event.CallbackID, err)
		}
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
