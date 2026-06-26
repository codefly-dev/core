package code

// SymbolKind classifies an internal code symbol. It is deliberately not part
// of the Codefly plugin proto; Mind owns semantic code-intelligence APIs.
type SymbolKind int32

const (
	SymbolKindUnknown SymbolKind = iota
	SymbolKindFunction
	SymbolKindMethod
	SymbolKindStruct
	SymbolKindInterface
	SymbolKindConstant
	SymbolKindVariable
	SymbolKindTypeAlias
	SymbolKindPackage
	SymbolKindField
	SymbolKindEnum
	SymbolKindClass
)

func (k SymbolKind) String() string {
	switch k {
	case SymbolKindFunction:
		return "function"
	case SymbolKindMethod:
		return "method"
	case SymbolKindStruct:
		return "struct"
	case SymbolKindInterface:
		return "interface"
	case SymbolKindConstant:
		return "constant"
	case SymbolKindVariable:
		return "variable"
	case SymbolKindTypeAlias:
		return "type_alias"
	case SymbolKindPackage:
		return "package"
	case SymbolKindField:
		return "field"
	case SymbolKindEnum:
		return "enum"
	case SymbolKindClass:
		return "class"
	default:
		return "unknown"
	}
}

type Location struct {
	File      string
	Line      int32
	Column    int32
	EndLine   int32
	EndColumn int32
}

type Symbol struct {
	Name          string
	Kind          SymbolKind
	Location      *Location
	Signature     string
	Documentation string
	Parent        string
	Children      []*Symbol
	QualifiedName string
	BodyHash      string
	SignatureHash string
}
