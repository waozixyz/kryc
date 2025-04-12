package main

// --- KRB v0.3 Constants ---
const (
	KRBMagic             = "KRB1"
	KRBVersionMajor      = 0
	KRBVersionMinor      = 3
	KRBHeaderSize        = 42
	KRBElementHeaderSize = 17 // Updated for v0.3 Custom Prop Count byte
)

// Header Flags (Bit 0-6)
const (
	FlagHasStyles     uint16 = 1 << 0
	FlagHasAnimations uint16 = 1 << 1 // Not implemented
	FlagHasResources  uint16 = 1 << 2
	FlagCompressed    uint16 = 1 << 3 // Not implemented
	FlagFixedPoint    uint16 = 1 << 4
	FlagExtendedColor uint16 = 1 << 5
	FlagHasApp        uint16 = 1 << 6
)

// Element Types (0x00 - 0x30+, 0xFE=Internal Placeholder)
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
	ElemTypeInternalComponentUsage uint8 = 0xFE // Marker for unexpanded component
	ElemTypeUnknown                uint8 = 0xFF // Internal marker for unknown type name
	ElemTypeCustomBase             uint8 = 0x31 // Base for custom types found
)

// Property IDs (0x01 - 0x28) - Standard KRB Properties
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
	PropIDMaxWidth       uint8 = 0x13
	PropIDMaxHeight      uint8 = 0x14
	PropIDAspectRatio    uint8 = 0x15
	PropIDTransform      uint8 = 0x16
	PropIDShadow         uint8 = 0x17
	PropIDOverflow       uint8 = 0x18
	PropIDCustomDataBlob uint8 = 0x19
	PropIDLayoutFlags    uint8 = 0x1A
	PropIDWindowWidth    uint8 = 0x20
	PropIDWindowHeight   uint8 = 0x21
	PropIDWindowTitle    uint8 = 0x22
	PropIDResizable      uint8 = 0x23
	PropIDKeepAspect     uint8 = 0x24
	PropIDScaleFactor    uint8 = 0x25
	PropIDIcon           uint8 = 0x26
	PropIDVersion        uint8 = 0x27
	PropIDAuthor         uint8 = 0x28
)

// Value Types (0x00 - 0x0B + Internal Hints)
const (
	ValTypeNone       uint8 = 0x00
	ValTypeByte       uint8 = 0x01 // Also used for Bool in KRB
	ValTypeShort      uint8 = 0x02 // Also used for Int in KRB
	ValTypeColor      uint8 = 0x03
	ValTypeString     uint8 = 0x04 // Represents String Index
	ValTypeResource   uint8 = 0x05 // Represents Resource Index
	ValTypePercentage uint8 = 0x06 // Represents 8.8 Fixed Point (uint16)
	ValTypeRect       uint8 = 0x07 // Example: 4 shorts (8 bytes)
	ValTypeEdgeInsets uint8 = 0x08 // Example: 4 bytes/shorts
	ValTypeEnum       uint8 = 0x09 // Specific meaning depends on PropID
	ValTypeVector     uint8 = 0x0A // Example: 2 shorts (4 bytes)
	ValTypeCustom     uint8 = 0x0B // Application-specific binary data

	// --- Internal Compiler Hint Types (Not written directly as ValueType in KRB) ---
	// These hints help the resolver convert source values for custom properties.
	ValTypeStyleID uint8 = 0x0C // Hint for a string representing a Style Name
	ValTypeFloat   uint8 = 0x0D // Hint for parsing standard float strings
	ValTypeInt     uint8 = 0x0E // Hint for parsing standard integer strings (maps to ValTypeShort in KRB)
	ValTypeBool    uint8 = 0x0F // Hint for parsing "true"/"false" strings (maps to ValTypeByte in KRB)
)
// Event Types (0x01 - ...)
const (
	EventTypeClick uint8 = 0x01
)

// Layout Byte Bits
const (
	LayoutDirectionMask     uint8 = 0x03
	LayoutDirectionRow      uint8 = 0
	LayoutDirectionColumn   uint8 = 1
	LayoutDirectionRowRev   uint8 = 2
	LayoutDirectionColRev   uint8 = 3
	LayoutAlignmentMask     uint8 = 0x0C
	LayoutAlignmentStart    uint8 = (0 << 2)
	LayoutAlignmentCenter   uint8 = (1 << 2)
	LayoutAlignmentEnd      uint8 = (2 << 2)
	LayoutAlignmentSpaceBtn uint8 = (3 << 2)
	LayoutWrapBit           uint8 = (1 << 4)
	LayoutGrowBit           uint8 = (1 << 5)
	LayoutAbsoluteBit       uint8 = (1 << 6)
)

// Resource Types & Formats
const (
	ResTypeImage      uint8 = 0x01
	ResFormatExternal uint8 = 0x00
	ResFormatInline   uint8 = 0x01 // Not implemented
)

// --- Limits ---
const (
	MaxElements      = 1024
	MaxStrings       = 1024
	MaxProperties    = 64 // Max per element/style/component def
	MaxStyles        = 256
	MaxChildren      = 256 // Limit for source children during parse, final children slice dynamic
	MaxEvents        = 16
	MaxLineLength    = 2048 // Check during read
	MaxResources     = 256
	MaxIncludeDepth  = 16
	MaxComponentDefs = 128
	MaxBlockDepth    = 64
	MaxPathLen       = 4096 // Corresponds to PATH_MAX
)

// --- Go Data Structures ---

type KrbProperty struct {
	PropertyID uint8
	ValueType  uint8
	Size       uint8
	Value      []byte // Stores the final binary value
}

type SourceProperty struct {
	Key      string
	ValueStr string
	LineNum  int
}

type ComponentPropertyDef struct {
	Name            string
	ValueTypeHint   uint8 // VAL_TYPE_* hint for parsing/validation
	DefaultValueStr string
}

type KrbEvent struct {
	EventType  uint8
	CallbackID uint8 // String table index (0-based)
}

type ResourceEntry struct {
	Type            uint8
	NameIndex       uint8 // String index for name/path
	Format          uint8
	DataStringIndex uint8 // For external: index into string table for path
	Index           uint8 // 0-based index in resource table
	CalculatedSize  uint32
}

type StringEntry struct {
	Text   string // The actual string content
	Length int    // Length in bytes (Go len())
	Index  uint8  // 0-based index
}

type ComponentDefinition struct {
	Name                     string
	Properties               []ComponentPropertyDef // Use slice
	DefinitionRootType       string                 // e.g., "Container"
	DefinitionRootProperties []SourceProperty       // Properties defined *on* the root element in the Define block
	DefinitionStartLine      int
	// Removed explicit counts, use len() on slices
}

type StyleEntry struct {
	ID               uint8         // 1-based ID
	NameIndex        uint8         // 0-based string index
	Properties       []KrbProperty // Resolved KRB properties (AFTER inheritance)
	SourceName       string        // Keep source name for lookup
	CalculatedSize   uint32
	SourceProperties []SourceProperty // Store raw properties read from source BEFORE resolving inheritance
	ExtendsStyleName string           // Name of the style this one extends (if any)
	BaseStyleID      uint8            // 1-based ID of the base style (resolved later)
	IsResolved       bool             // Flag used during inheritance resolution pass
	IsResolving      bool             // Flag for cycle detection during resolution
}

type KrbCustomProperty struct {
	KeyIndex   uint8  // String table index for the property key name
	ValueType  uint8  // VAL_TYPE_* for the value
	ValueSize  uint8  // Size of the value data in bytes
	Value      []byte // The actual value data
}


// Represents a UI element in the compiler's internal state
type Element struct {
	// Header Data (KRB v0.3 compatible - filled during write phase)
	Type            uint8
	IDStringIndex   uint8
	PosX            uint16
	PosY            uint16
	Width           uint16
	Height          uint16
	Layout          uint8 // Final calculated layout byte
	StyleID         uint8 // 0 if no style
	PropertyCount   uint8 // Final count of *standard* KRB properties
	ChildCount      uint8
	EventCount      uint8
	AnimationCount  uint8 // Usually 0
	CustomPropCount uint8 // Count of *custom* KRB properties *** (Added in v0.3 spec) ***

	// Resolved Data (converted to KRB format)
	KrbProperties       []KrbProperty       // Slice for STANDARD properties
	KrbCustomProperties []KrbCustomProperty // *** NEW *** Slice for CUSTOM properties
	KrbEvents           []KrbEvent
	Children            []*Element // Points to elements in the *final* resolved array

	// Compiler Internal State & Source Info
	ParentIndex         int // Index of parent in CompilerState.Elements, -1 for root
	SelfIndex           int // Index of this element in CompilerState.Elements
	IsComponentInstance bool
	ComponentDef        *ComponentDefinition // Points to definition if is_component_instance
	SourceElementName   string               // "App", "Container", "TabBar", etc. from .kry
	SourceIDName        string               // "my_button" from `id: "my_button"`
	SourceProperties    []SourceProperty     // Store props found in .kry block
	SourceChildrenIndices []int              // Store indices during parsing
	SourceLineNum         int
	LayoutFlagsSource     uint8  // Layout byte derived *directly* from .kry 'layout:' property
	PositionHint          string // Stores "top", "bottom", etc. (Only used for simple child reorder hint during write)
	OrientationHint       string // Stores "row", "column" etc. (No longer directly sets Layout byte)

	// Pass 2/3 Data
	CalculatedSize uint32 // Calculated size in bytes for the KRB output
	AbsoluteOffset uint32 // Calculated offset in the final KRB file

	// Pass 1.5 Tracking
	ProcessedInPass15 bool
}

// Represents the overall compiler state
type CompilerState struct {
	Elements      []Element // Use slice
	Strings       []StringEntry
	Styles        []StyleEntry
	Resources     []ResourceEntry
	ComponentDefs []ComponentDefinition

	HasApp      bool
	HeaderFlags uint16

	// Parsing state
	CurrentLineNum  int
	CurrentFilePath string

	// Calculated Offsets & Size for Header
	ElementOffset  uint32
	StyleOffset    uint32
	AnimOffset     uint32
	StringOffset   uint32
	ResourceOffset uint32
	TotalSize      uint32
}

// --- Parser specific types ---

type BlockContextType int

const (
	CtxNone BlockContextType = iota
	CtxElement
	CtxStyle
	CtxComponentDef
	CtxProperties       // Inside Define -> Properties { }
	CtxComponentDefBody // Inside Define -> after RootType { }
)

type BlockStackEntry struct {
	Indent  int
	Context interface{} // Can be *Element, *StyleEntry, *ComponentDefinition, or nil
	Type    BlockContextType
}
