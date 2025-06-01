// types.go
package main

import (
	"fmt"
)

// --- KRB v0.4 Constants ---
const (
	KRBMagic             = "KRB1"
	KRBVersionMajor      = 0
	KRBVersionMinor      = 4
	KRBHeaderSize        = 48 // Corresponds to KRB v0.4 File Header specification
	KRBElementHeaderSize = 17 // Includes Custom Prop Count from v0.3
)

// Header Flags (Bit 0-7)
const (
	FlagHasStyles        uint16 = 1 << 0
	FlagHasComponentDefs uint16 = 1 << 1
	FlagHasAnimations    uint16 = 1 << 2 // Not fully implemented in compiler logic
	FlagHasResources     uint16 = 1 << 3
	FlagCompressed       uint16 = 1 << 4 // Not implemented
	FlagFixedPoint       uint16 = 1 << 5
	FlagExtendedColor    uint16 = 1 << 6
	FlagHasApp           uint16 = 1 << 7
)

// Element Types
const (
	ElemTypeApp                    uint8 = 0x00
	ElemTypeContainer              uint8 = 0x01
	ElemTypeText                   uint8 = 0x02
	ElemTypeImage                  uint8 = 0x03
	ElemTypeCanvas                 uint8 = 0x04
	ElemTypeButton                 uint8 = 0x10
	ElemTypeInput                  uint8 = 0x11
	ElemTypeList                   uint8 = 0x20
	ElemTypeGrid                   uint8 = 0x21
	ElemTypeScrollable             uint8 = 0x22
	ElemTypeVideo                  uint8 = 0x30
	ElemTypeInternalComponentUsage uint8 = 0xFE // Compiler internal: Marker for unexpanded component usage
	ElemTypeUnknown                uint8 = 0xFF // Compiler internal: Marker for unknown KRY element name
	ElemTypeCustomBase             uint8 = 0x31 // KRB: Base for custom types (0x31-0xFF)
)

// Standard KRB Property IDs
const (
	PropIDInvalid        uint8 = 0x00
	PropIDBgColor        uint8 = 0x01
	PropIDFgColor        uint8 = 0x02 // Also text_color
	PropIDBorderColor    uint8 = 0x03
	PropIDBorderWidth    uint8 = 0x04
	PropIDBorderRadius   uint8 = 0x05
	PropIDPadding        uint8 = 0x06
	PropIDMargin         uint8 = 0x07
	PropIDTextContent    uint8 = 0x08
	PropIDFontSize       uint8 = 0x09
	PropIDFontWeight     uint8 = 0x0A
	PropIDTextAlignment  uint8 = 0x0B
	PropIDImageSource    uint8 = 0x0C
	PropIDOpacity        uint8 = 0x0D
	PropIDZindex         uint8 = 0x0E
	PropIDVisibility     uint8 = 0x0F
	PropIDGap            uint8 = 0x10
	PropIDMinWidth       uint8 = 0x11
	PropIDMinHeight      uint8 = 0x12
	PropIDMaxWidth       uint8 = 0x13 // KRY 'width' often maps here
	PropIDMaxHeight      uint8 = 0x14 // KRY 'height' often maps here
	PropIDAspectRatio    uint8 = 0x15
	PropIDTransform      uint8 = 0x16
	PropIDShadow         uint8 = 0x17
	PropIDOverflow       uint8 = 0x18
	PropIDCustomDataBlob uint8 = 0x19
	PropIDLayoutFlags    uint8 = 0x1A // KRY 'layout' property effect is encoded in Element Header, not usually written as a KRB prop.
	// App-Specific Properties (on ELEM_TYPE_APP)
	PropIDWindowWidth  uint8 = 0x20
	PropIDWindowHeight uint8 = 0x21
	PropIDWindowTitle  uint8 = 0x22
	PropIDResizable    uint8 = 0x23
	PropIDKeepAspect   uint8 = 0x24
	PropIDScaleFactor  uint8 = 0x25
	PropIDIcon         uint8 = 0x26
	PropIDVersion      uint8 = 0x27
	PropIDAuthor       uint8 = 0x28
)

// KRB Value Types
const (
	ValTypeNone       uint8 = 0x00
	ValTypeByte       uint8 = 0x01 // Also used for Bool in KRB
	ValTypeShort      uint8 = 0x02 // Also used for Int in KRB
	ValTypeColor      uint8 = 0x03 // 1 byte (palette index) or 4 bytes (RGBA)
	ValTypeString     uint8 = 0x04 // Represents String Table Index (typically 1 byte)
	ValTypeResource   uint8 = 0x05 // Represents Resource Table Index (typically 1 byte)
	ValTypePercentage uint8 = 0x06 // Represents 8.8 Fixed Point (uint16)
	ValTypeRect       uint8 = 0x07 // Example: 4 shorts (x,y,w,h) -> 8 bytes
	ValTypeEdgeInsets uint8 = 0x08 // Example: 4 bytes (t,r,b,l) or 4 shorts
	ValTypeEnum       uint8 = 0x09 // Typically 1 byte, meaning depends on PropID
	ValTypeVector     uint8 = 0x0A // Example: 2 shorts (x,y) -> 4 bytes
	ValTypeCustom     uint8 = 0x0B // Application-specific binary data, often with PROP_ID_CUSTOM_DATA_BLOB

	// --- Internal Compiler Hint Types (Not written directly as ValueType in KRB) ---
	// These guide the KRY parser/resolver for KRY source property values.
	ValTypeStyleID uint8 = 0x0C // KRY source: "style_name_string" -> KRB: StyleID in Element Header
	ValTypeFloat   uint8 = 0x0D // KRY source: "0.5" -> KRB: ValTypePercentage (8.8 fixed point)
	ValTypeInt     uint8 = 0x0E // KRY source: "100" -> KRB: ValTypeShort (or Byte if small enough)
	ValTypeBool    uint8 = 0x0F // KRY source: "true" -> KRB: ValTypeByte (0 or 1)
)

// Event Types
const (
	EventTypeClick uint8 = 0x01
	// Add other event types as needed (Press, Release, Change, etc.)
)

// Layout Byte Bit Definitions
const (
	LayoutDirectionMask     uint8 = 0x03 // Bits 0-1
	LayoutDirectionRow      uint8 = 0
	LayoutDirectionColumn   uint8 = 1
	LayoutDirectionRowRev   uint8 = 2
	LayoutDirectionColRev   uint8 = 3
	LayoutAlignmentMask     uint8 = 0x0C // Bits 2-3
	LayoutAlignmentStart    uint8 = (0 << 2)
	LayoutAlignmentCenter   uint8 = (1 << 2)
	LayoutAlignmentEnd      uint8 = (2 << 2)
	LayoutAlignmentSpaceBtn uint8 = (3 << 2)
	LayoutWrapBit           uint8 = (1 << 4) // Bit 4
	LayoutGrowBit           uint8 = (1 << 5) // Bit 5
	LayoutAbsoluteBit       uint8 = (1 << 6) // Bit 6
	// Bit 7: Reserved
)

// Resource Types & Formats
const (
	ResTypeImage      uint8 = 0x01
	ResTypeFont       uint8 = 0x02
	ResTypeSound      uint8 = 0x03
	ResTypeVideo      uint8 = 0x04
	ResTypeCustom     uint8 = 0x05
	ResFormatExternal uint8 = 0x00 // Data is string index to path
	ResFormatInline   uint8 = 0x01 // Data is size + raw bytes (not fully implemented in compiler)
)

// --- Compiler Limits ---
const (
	MaxElements         = 1024 // Max elements in the main UI tree + all template definitions
	MaxStrings          = 1024
	MaxProperties       = 64  // Max standard KRB properties per element/style/component property def
	MaxStyleProperties  = 128 // Max source properties for a style during parsing/resolution
	MaxCustomProperties = 32  // Max custom KRB properties per element instance
	MaxStyles           = 256
	MaxChildren         = 256 // Max source children for an element during parsing
	MaxEvents           = 16
	MaxLineLength       = 2048
	MaxResources        = 256
	MaxIncludeDepth     = 16
	MaxComponentDefs    = 128
	MaxBlockDepth       = 64 // Max nesting of KRY blocks {}
	MaxPathLen          = 4096
)

// --- Go Data Structures for KRB Compilation ---

// KrbProperty represents a standard KRB property entry.
type KrbProperty struct {
	PropertyID uint8
	ValueType  uint8
	Size       uint8
	Value      []byte // Final binary value for the property
}

// SourceProperty represents a property as parsed from the .kry source file.
type SourceProperty struct {
	Key      string
	ValueStr string // Raw string value from source
	LineNum  int
}

// ComponentPropertyDef describes a property declared in a `Define ComponentName { Properties { ... } }` block.
type ComponentPropertyDef struct {
	Name            string
	ValueTypeHint   uint8  // VAL_TYPE_* hint for parsing/validation of its value
	DefaultValueStr string // Default value as a string from KRY source
	// ParsedDefaultValueData []byte // Optional: Store pre-parsed binary default value after resolver
}

// KrbEvent represents an event entry in an Element Block.
type KrbEvent struct {
	EventType  uint8
	CallbackID uint8 // String table index (0-based) for the callback function name
}

// ResourceEntry represents an entry in the KRB Resource Table.
type ResourceEntry struct {
	Type            uint8
	NameIndex       uint8 // String table index for resource name/identifier
	Format          uint8
	DataStringIndex uint8  // For RES_FORMAT_EXTERNAL: string table index of the resource path/URL
	Index           uint8  // 0-based index of this resource in the KRB Resource Table
	CalculatedSize  uint32 // Calculated size of this entry in the KRB file
}

// StringEntry represents an entry in the KRB String Table.
type StringEntry struct {
	Text   string // The actual UTF-8 string content
	Length int    // Length in bytes (Go `len()`)
	Index  uint8  // 0-based index of this string in the KRB String Table
}

// ComponentDefinition represents a parsed `Define ComponentName { ... }` block.
type ComponentDefinition struct {
	Name                       string
	Properties                 []ComponentPropertyDef // Properties declared in its `Properties {}` block
	DefinitionStartLine        int                    // Line number in KRY source where `Define` started
	DefinitionRootElementIndex int                    // Index in `CompilerState.Elements` of the root element of this component's template structure
	// RootElementTemplate     Element // This field is somewhat redundant if DefinitionRootElementIndex is used directly.
	//                            // It could hold a copy or be a pointer if needed for specific processing.
	CalculatedSize uint32 // Calculated size of this entire component definition entry in the KRB file

	// Stores pre-calculated offsets for template elements relative to this template's data blob start.
	// Key is el.SelfIndex of a template element, Value is its offset from the beginning of this template's RootElementTemplate data.
	InternalTemplateElementOffsets map[int]uint32
}

// StyleEntry represents a parsed `style "name" { ... }` block.
type StyleEntry struct {
	ID                uint8            // 1-based ID for this style in KRB
	SourceName        string           // Name of the style from KRY source (e.g., "my_button_style")
	NameIndex         uint8            // String table index for SourceName
	ExtendsStyleNames []string         // Names of base styles this style extends
	Properties        []KrbProperty    // Final resolved KRB properties for this style
	SourceProperties  []SourceProperty // Raw properties from KRY source before resolution
	CalculatedSize    uint32           // Calculated size of this style block in the KRB file
	IsResolved        bool             // Flag used during style inheritance resolution
	IsResolving       bool             // Flag used during style inheritance resolution for cycle detection
}

// addSourceProperty adds a raw key-value pair from the .kry source to a style entry.
// The last definition of a property key in the source for a given style wins.
func (style *StyleEntry) addSourceProperty(key, value string, lineNum int) error {
	for i := range style.SourceProperties {
		if style.SourceProperties[i].Key == key {
			style.SourceProperties[i].ValueStr = value
			style.SourceProperties[i].LineNum = lineNum
			return nil
		}
	}
	if len(style.SourceProperties) >= MaxStyleProperties {
		return fmt.Errorf("L%d: maximum source properties (%d) exceeded for style '%s'", lineNum, MaxStyleProperties, style.SourceName)
	}
	prop := SourceProperty{Key: key, ValueStr: value, LineNum: lineNum}
	style.SourceProperties = append(style.SourceProperties, prop)
	return nil
}

// KrbCustomProperty represents a custom key-value property entry in an Element Block.
type KrbCustomProperty struct {
	KeyIndex  uint8  // String table index for the property key name
	ValueType uint8  // VAL_TYPE_* for the value
	Size      uint8  // Size of the value data in bytes
	Value     []byte // The actual value data
}

// Element represents a UI element in the compiler's internal abstract syntax tree.
// This includes both elements instantiated in the main UI tree and elements forming component templates.
type Element struct {
	// Data that maps directly to the KRB Element Header
	Type            uint8 // ELEM_TYPE_*
	IDStringIndex   uint8 // String table index for KRY `id` or custom element type name
	PosX            uint16
	PosY            uint16
	Width           uint16
	Height          uint16
	Layout          uint8 // Final calculated layout byte (derived from KRY `layout` property)
	StyleID         uint8 // 1-based index into Style Blocks; 0 for no style
	PropertyCount   uint8 // Final count of *standard* KRB properties for this element
	ChildCount      uint8 // Final count of children for this element in its context (main tree or template)
	EventCount      uint8
	AnimationCount  uint8 // Typically 0 in this compiler version
	CustomPropCount uint8 // Final count of *custom* KRB properties for this element

	// Resolved KRB data for this element's block sections
	KrbProperties       []KrbProperty       // Standard properties
	KrbCustomProperties []KrbCustomProperty // Custom properties
	KrbEvents           []KrbEvent
	Children            []*Element // Pointers to child elements within `CompilerState.Elements`
	// For main tree: actual children. For templates: children within that template.

	// Compiler Internal State & Source Information
	ParentIndex           int                  // Index of parent in `CompilerState.Elements`; -1 for root or template root
	SelfIndex             int                  // Index of this element in `CompilerState.Elements`
	IsComponentInstance   bool                 // True if this element is an instance of a `Define`d component
	ComponentDef          *ComponentDefinition // If IsComponentInstance, points to its definition
	IsDefinitionRoot      bool                 // True if this element is the root of a component template's structure
	SourceElementName     string               // Element name from KRY source (e.g., "Container", "MyComponent")
	SourceIDName          string               // Value of `id` property from KRY source (e.g., "my_button")
	SourceProperties      []SourceProperty     // Properties as parsed from KRY source block before resolution
	SourceChildrenIndices []int                // Indices of children as parsed, before `Children` pointers are resolved
	SourceLineNum         int                  // Line number in KRY source where this element started
	LayoutFlagsSource     uint8                // Layout byte derived *directly* from KRY `layout` property string, before style merge
	PositionHint          string               // KRY `position` property value (e.g., "top", "bottom"), used by resolver/writer
	OrientationHint       string               // KRY `orientation` property value (e.g., "row"), used by resolver/writer

	// Data for KRB Writing Pass
	CalculatedSize uint32 // Calculated total size of this element's block in KRB bytes
	AbsoluteOffset uint32 // Calculated absolute byte offset of this element's header from start of KRB file

	// State for Compiler Passes
	ProcessedInPass15 bool // Flag for component expansion and property resolution pass
}

// VariableDef stores information about a defined variable.
type VariableDef struct {
	Value       string // Final, literal value after inter-variable resolution
	RawValue    string // Value as parsed from KRY, might contain $otherVar
	DefLine     int    // Line number where this variable was defined (latest if redefined)
	IsResolving bool   // For cycle detection during inter-variable resolution
	IsResolved  bool   // True if Value holds the final literal
}

// CompilerState holds the entire state of the compilation process.
type CompilerState struct {
	Elements      []Element // Flat list of all elements (main UI tree instances and component template elements)
	Strings       []StringEntry
	Styles        []StyleEntry
	Resources     []ResourceEntry
	ComponentDefs []ComponentDefinition  // Parsed component definitions
	Variables     map[string]VariableDef // Stores all defined variables

	HasApp      bool   // True if the main UI tree has an `App` root (or implicit via root component)
	HeaderFlags uint16 // KRB File Header flags, accumulated during compilation

	// State for KRY Parser
	CurrentLineNum  int
	CurrentFilePath string

	// Calculated Offsets & Sizes for KRB File Header
	ElementOffset      uint32 // Byte offset to Element Blocks (main UI tree)
	StyleOffset        uint32 // Byte offset to Style Blocks
	ComponentDefOffset uint32 // Byte offset to Component Definition Table
	AnimOffset         uint32 // Byte offset to Animation Table (currently unused)
	StringOffset       uint32 // Byte offset to String Table
	ResourceOffset     uint32 // Byte offset to Resource Table
	TotalSize          uint32 // Total KRB file size in bytes

	// Calculated total sizes for data within each section (excluding section headers like counts)
	TotalElementDataSize      uint32
	TotalStyleDataSize        uint32
	TotalComponentDefDataSize uint32
	TotalStringDataSize       uint32 // Size of string data (length prefixes + UTF-8 bytes)
	TotalResourceTableSize    uint32 // Size of all resource entries
}

// --- Parser-Specific Internal Types ---

// BlockContextType identifies the current type of KRY block being parsed.
type BlockContextType int

const (
	CtxNone              BlockContextType = iota // Outside any block
	CtxElement                                   // Inside an Element { } block (main tree or template part)
	CtxStyle                                     // Inside a style "name" { } block
	CtxComponentDef                              // Inside a Define Name { } block (specifically, before its Properties or root element)
	CtxProperties                                // Inside a Define -> Properties { } sub-block
	CtxEdgeInsetProperty                         // Inside a padding: { } or margin: { } sub-block
)

// EdgeInsetParseState manages parsing of multi-value KRY properties like `padding: { top: N; ... }`.
type EdgeInsetParseState struct {
	ParentKey     string           // "padding" or "margin"
	ParentCtx     interface{}      // Points to the *Element or *StyleEntry containing this block
	ParentCtxType BlockContextType // CtxElement or CtxStyle
	Indent        int              // Indentation level where this edge inset block started
	StartLine     int              // Line number where this edge inset block started
	Top           *string          // String pointers differentiate not-set from explicitly "0"
	Right         *string
	Bottom        *string
	Left          *string
}

// BlockStackEntry is used by the KRY parser to manage nested block structures.
type BlockStackEntry struct {
	Indent  int
	Context interface{} // Can be *Element, *StyleEntry, *ComponentDefinition, *EdgeInsetParseState
	Type    BlockContextType
}
