package symbols

// ScopeKind 标识词法作用域的层级分类
type ScopeKind int

const (
	ScopeModule ScopeKind = iota
	ScopeFunction
	ScopeBlock
	ScopeMatchArm
	ScopeImpl
)

// Scope 表示层次化词法作用域符号表
type Scope struct {
	Kind    ScopeKind
	Parent  *Scope
	Symbols map[string]*Symbol
	OwnerFn *Symbol // 当前所在的直接外层函数符号（用于 return 类型检查）
}

// NewScope 创建指定类别与父作用域的新作用域实例
func NewScope(kind ScopeKind, parent *Scope) *Scope {
	var owner *Symbol
	if parent != nil {
		owner = parent.OwnerFn
	}
	return &Scope{
		Kind:    kind,
		Parent:  parent,
		Symbols: make(map[string]*Symbol),
		OwnerFn: owner,
	}
}

// Insert 尝试在当前作用域插入符号。
// 遵循规范的同作用域去重规则：同一词法作用域内不允许重复声明同名绑定/符号，
// 而内层子作用域（如嵌套 if / while / block / match 分支）允许遮蔽外层符号。
func (s *Scope) Insert(sym *Symbol) (*Symbol, bool) {
	if existing, exists := s.Symbols[sym.Name]; exists {
		return existing, false
	}
	s.Symbols[sym.Name] = sym
	return nil, true
}

// Lookup 沿着父作用域链向上检索可见符号
func (s *Scope) Lookup(name string) *Symbol {
	for cur := s; cur != nil; cur = cur.Parent {
		if sym, ok := cur.Symbols[name]; ok {
			return sym
		}
	}
	return nil
}

// LookupLocal 仅在当前词法作用域内检索符号（用于检测同层重定义）
func (s *Scope) LookupLocal(name string) *Symbol {
	if sym, ok := s.Symbols[name]; ok {
		return sym
	}
	return nil
}
