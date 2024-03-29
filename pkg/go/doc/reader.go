// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"go/ast"
	"go/token"
	"regexp"
	"sort"
	"strconv"
)

// ----------------------------------------------------------------------------
// function/method sets
//
// Internally, we treat functions like methods and collect them in method sets.

// methodSet describes a set of methods. Entries where Decl == nil are conflict
// entries (more then one method with the same name at the same embedding level).
//
type methodSet map[string]*Func

// recvString returns a string representation of recv of the
// form "T", "*T", or "BADRECV" (if not a proper receiver type).
//
func recvString(recv ast.Expr) string {
	switch t := recv.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + recvString(t.X)
	}
	return "BADRECV"
}

// set creates the corresponding Func for f and adds it to mset.
// If there are multiple f's with the same name, set keeps the first
// one with documentation; conflicts are ignored.
//
func (mset methodSet) set(f *ast.FuncDecl) {
	name := f.Name.Name
	if g := mset[name]; g != nil && g.Doc != "" {
		// A function with the same name has already been registered;
		// since it has documentation, assume f is simply another
		// implementation and ignore it. This does not happen if the
		// caller is using go/build.ScanDir to determine the list of
		// files implementing a package. 
		return
	}
	// function doesn't exist or has no documentation; use f
	recv := ""
	if f.Recv != nil {
		var typ ast.Expr
		// be careful in case of incorrect ASTs
		if list := f.Recv.List; len(list) == 1 {
			typ = list[0].Type
		}
		recv = recvString(typ)
	}
	mset[name] = &Func{
		Doc:  f.Doc.Text(),
		Name: name,
		Decl: f,
		Recv: recv,
		Orig: recv,
	}
	f.Doc = nil // doc consumed - remove from AST
}

// add adds method m to the method set; m is ignored if the method set
// already contains a method with the same name at the same or a higher
// level then m.
//
func (mset methodSet) add(m *Func) {
	old := mset[m.Name]
	if old == nil || m.Level < old.Level {
		mset[m.Name] = m
		return
	}
	if old != nil && m.Level == old.Level {
		// conflict - mark it using a method with nil Decl
		mset[m.Name] = &Func{
			Name:  m.Name,
			Level: m.Level,
		}
	}
}

// ----------------------------------------------------------------------------
// Base types

// baseTypeName returns the name of the base type of x (or "")
// and whether the type is imported or not.
//
func baseTypeName(x ast.Expr) (name string, imported bool) {
	switch t := x.(type) {
	case *ast.Ident:
		return t.Name, false
	case *ast.SelectorExpr:
		if _, ok := t.X.(*ast.Ident); ok {
			// only possible for qualified type names;
			// assume type is imported
			return t.Sel.Name, true
		}
	case *ast.StarExpr:
		return baseTypeName(t.X)
	}
	return
}

// embeddedType describes the type of an anonymous field.
//
type embeddedType struct {
	typ *baseType // the corresponding base type
	ptr bool      // if set, the anonymous field type is a pointer
}

type baseType struct {
	doc  string       // doc comment for type
	name string       // local type name (excluding package qualifier)
	decl *ast.GenDecl // nil if declaration hasn't been seen yet

	// associated declarations
	values  []*Value // consts and vars
	funcs   methodSet
	methods methodSet

	isEmbedded bool           // true if this type is embedded
	isStruct   bool           // true if this type is a struct
	embedded   []embeddedType // list of embedded types
}

func (typ *baseType) addEmbeddedType(e *baseType, isPtr bool) {
	e.isEmbedded = true
	typ.embedded = append(typ.embedded, embeddedType{e, isPtr})
}

// ----------------------------------------------------------------------------
// AST reader

// reader accumulates documentation for a single package.
// It modifies the AST: Comments (declaration documentation)
// that have been collected by the reader are set to nil
// in the respective AST nodes so that they are not printed
// twice (once when printing the documentation and once when
// printing the corresponding AST node).
//
type reader struct {
	mode Mode

	// package properties
	doc       string // package documentation, if any
	filenames []string
	bugs      []string

	// declarations
	imports map[string]int
	values  []*Value // consts and vars
	types   map[string]*baseType
	funcs   methodSet
}

// isVisible reports whether name is visible in the documentation.
//
func (r *reader) isVisible(name string) bool {
	return r.mode&AllDecls != 0 || ast.IsExported(name)
}

// lookupType returns the base type with the given name.
// If the base type has not been encountered yet, a new
// type with the given name but no associated declaration
// is added to the type map.
//
func (r *reader) lookupType(name string) *baseType {
	if name == "" || name == "_" {
		return nil // no type docs for anonymous types
	}
	if typ, found := r.types[name]; found {
		return typ
	}
	// type not found - add one without declaration
	typ := &baseType{
		name:    name,
		funcs:   make(methodSet),
		methods: make(methodSet),
	}
	r.types[name] = typ
	return typ
}

func (r *reader) readDoc(comment *ast.CommentGroup) {
	// By convention there should be only one package comment
	// but collect all of them if there are more then one.
	text := comment.Text()
	if r.doc == "" {
		r.doc = text
		return
	}
	r.doc += "\n" + text
}

func specNames(specs []ast.Spec) []string {
	names := make([]string, 0, len(specs)) // reasonable estimate
	for _, s := range specs {
		// s guaranteed to be an *ast.ValueSpec by readValue
		for _, ident := range s.(*ast.ValueSpec).Names {
			names = append(names, ident.Name)
		}
	}
	return names
}

// readValue processes a const or var declaration.
//
func (r *reader) readValue(decl *ast.GenDecl) {
	// determine if decl should be associated with a type
	// Heuristic: For each typed entry, determine the type name, if any.
	//            If there is exactly one type name that is sufficiently
	//            frequent, associate the decl with the respective type.
	domName := ""
	domFreq := 0
	prev := ""
	n := 0
	for _, spec := range decl.Specs {
		s, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue // should not happen, but be conservative
		}
		name := ""
		switch {
		case s.Type != nil:
			// a type is present; determine its name
			if n, imp := baseTypeName(s.Type); !imp && r.isVisible(n) {
				name = n
			}
		case decl.Tok == token.CONST:
			// no type is present but we have a constant declaration;
			// use the previous type name (w/o more type information
			// we cannot handle the case of unnamed variables with
			// initializer expressions except for some trivial cases)
			name = prev
		}
		if name != "" {
			// entry has a named type
			if domName != "" && domName != name {
				// more than one type name - do not associate
				// with any type
				domName = ""
				break
			}
			domName = name
			domFreq++
		}
		prev = name
		n++
	}

	// nothing to do w/o a legal declaration
	if n == 0 {
		return
	}

	// determine values list with which to associate the Value for this decl
	values := &r.values
	const threshold = 0.75
	if domName != "" && domFreq >= int(float64(len(decl.Specs))*threshold) {
		// typed entries are sufficiently frequent
		typ := r.lookupType(domName)
		if typ != nil {
			values = &typ.values // associate with that type
		}
	}

	*values = append(*values, &Value{
		Doc:   decl.Doc.Text(),
		Names: specNames(decl.Specs),
		Decl:  decl,
		order: len(*values),
	})
	decl.Doc = nil // doc consumed - remove from AST
}

// fields returns a struct's fields or an interface's methods.
//
func fields(typ ast.Expr) (list []*ast.Field, isStruct bool) {
	var fields *ast.FieldList
	switch t := typ.(type) {
	case *ast.StructType:
		fields = t.Fields
		isStruct = true
	case *ast.InterfaceType:
		fields = t.Methods
	}
	if fields != nil {
		list = fields.List
	}
	return
}

// readType processes a type declaration.
//
func (r *reader) readType(decl *ast.GenDecl, spec *ast.TypeSpec) {
	typ := r.lookupType(spec.Name.Name)
	if typ == nil {
		return // no name or blank name - ignore the type
	}

	// A type should be added at most once, so info.decl
	// should be nil - if it is not, simply overwrite it.
	typ.decl = decl

	// compute documentation
	doc := spec.Doc
	spec.Doc = nil // doc consumed - remove from AST
	if doc == nil {
		// no doc associated with the spec, use the declaration doc, if any
		doc = decl.Doc
	}
	decl.Doc = nil // doc consumed - remove from AST
	typ.doc = doc.Text()

	// look for anonymous fields that might contribute methods
	var list []*ast.Field
	list, typ.isStruct = fields(spec.Type)
	for _, field := range list {
		if len(field.Names) == 0 {
			// anonymous field - add corresponding field type to typ
			n, imp := baseTypeName(field.Type)
			if imp {
				// imported type - we don't handle this case
				// at the moment
				return
			}
			if embedded := r.lookupType(n); embedded != nil {
				_, ptr := field.Type.(*ast.StarExpr)
				typ.addEmbeddedType(embedded, ptr)
			}
		}
	}
}

// readFunc processes a func or method declaration.
//
func (r *reader) readFunc(fun *ast.FuncDecl) {
	// strip function body
	fun.Body = nil

	// determine if it should be associated with a type
	if fun.Recv != nil {
		// method
		recvTypeName, imp := baseTypeName(fun.Recv.List[0].Type)
		if imp {
			// should not happen (incorrect AST);
			// don't show this method
			return
		}
		var typ *baseType
		if r.isVisible(recvTypeName) {
			// visible recv type: if not found, add it to r.types
			typ = r.lookupType(recvTypeName)
		} else {
			// invisible recv type: if not found, do not add it
			// (invisible embedded types are added before this
			// phase, so if the type doesn't exist yet, we don't
			// care about this method)
			typ = r.types[recvTypeName]
		}
		if typ != nil {
			// associate method with the type
			// (if the type is not exported, it may be embedded
			// somewhere so we need to collect the method anyway)
			typ.methods.set(fun)
		}
		// otherwise don't show the method
		// TODO(gri): There may be exported methods of non-exported types
		// that can be called because of exported values (consts, vars, or
		// function results) of that type. Could determine if that is the
		// case and then show those methods in an appropriate section.
		return
	}

	// perhaps a factory function
	// determine result type, if any
	if fun.Type.Results.NumFields() >= 1 {
		res := fun.Type.Results.List[0]
		if len(res.Names) <= 1 {
			// exactly one (named or anonymous) result associated
			// with the first type in result signature (there may
			// be more than one result)
			if n, imp := baseTypeName(res.Type); !imp && r.isVisible(n) {
				if typ := r.lookupType(n); typ != nil {
					// associate Func with typ
					typ.funcs.set(fun)
					return
				}
			}
		}
	}

	// just an ordinary function
	r.funcs.set(fun)
}

var (
	bug_markers = regexp.MustCompile("^/[/*][ \t]*BUG\\(.*\\):[ \t]*") // BUG(uid):
	bug_content = regexp.MustCompile("[^ \n\r\t]+")                    // at least one non-whitespace char
)

// readFile adds the AST for a source file to the reader.
//
func (r *reader) readFile(src *ast.File) {
	// add package documentation
	if src.Doc != nil {
		r.readDoc(src.Doc)
		src.Doc = nil // doc consumed - remove from AST
	}

	// add all declarations
	for _, decl := range src.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			switch d.Tok {
			case token.IMPORT:
				// imports are handled individually
				for _, spec := range d.Specs {
					if s, ok := spec.(*ast.ImportSpec); ok {
						if import_, err := strconv.Unquote(s.Path.Value); err == nil {
							r.imports[import_] = 1
						}
					}
				}
			case token.CONST, token.VAR:
				// constants and variables are always handled as a group
				r.readValue(d)
			case token.TYPE:
				// types are handled individually
				for _, spec := range d.Specs {
					if s, ok := spec.(*ast.TypeSpec); ok {
						// use an individual (possibly fake) declaration
						// for each type; this also ensures that each type
						// gets to (re-)use the declaration documentation
						// if there's none associated with the spec itself
						fake := &ast.GenDecl{
							d.Doc, d.Pos(), token.TYPE, token.NoPos,
							[]ast.Spec{s}, token.NoPos,
						}
						r.readType(fake, s)
					}
				}
			}
		case *ast.FuncDecl:
			r.readFunc(d)
		}
	}

	// collect BUG(...) comments
	for _, c := range src.Comments {
		text := c.List[0].Text
		if m := bug_markers.FindStringIndex(text); m != nil {
			// found a BUG comment; maybe empty
			if btxt := text[m[1]:]; bug_content.MatchString(btxt) {
				// non-empty BUG comment; collect comment without BUG prefix
				list := append([]*ast.Comment(nil), c.List...) // make a copy
				list[0].Text = text[m[1]:]
				r.bugs = append(r.bugs, (&ast.CommentGroup{list}).Text())
			}
		}
	}
	src.Comments = nil // consumed unassociated comments - remove from AST
}

func (r *reader) readPackage(pkg *ast.Package, mode Mode) {
	// initialize reader
	r.filenames = make([]string, len(pkg.Files))
	r.imports = make(map[string]int)
	r.mode = mode
	r.types = make(map[string]*baseType)
	r.funcs = make(methodSet)

	// sort package files before reading them so that the
	// result result does not depend on map iteration order
	i := 0
	for filename := range pkg.Files {
		r.filenames[i] = filename
		i++
	}
	sort.Strings(r.filenames)

	// process files in sorted order
	for _, filename := range r.filenames {
		f := pkg.Files[filename]
		if mode&AllDecls == 0 {
			r.fileExports(f)
		}
		r.readFile(f)
	}
}

// ----------------------------------------------------------------------------
// Types

var predeclaredTypes = map[string]bool{
	"bool":       true,
	"byte":       true,
	"complex64":  true,
	"complex128": true,
	"float32":    true,
	"float64":    true,
	"int":        true,
	"int8":       true,
	"int16":      true,
	"int32":      true,
	"int64":      true,
	"string":     true,
	"uint":       true,
	"uint8":      true,
	"uint16":     true,
	"uint32":     true,
	"uint64":     true,
	"uintptr":    true,
}

func customizeRecv(f *Func, recvTypeName string, embeddedIsPtr bool, level int) *Func {
	if f == nil || f.Decl == nil || f.Decl.Recv == nil || len(f.Decl.Recv.List) != 1 {
		return f // shouldn't happen, but be safe
	}

	// copy existing receiver field and set new type
	newField := *f.Decl.Recv.List[0]
	_, origRecvIsPtr := newField.Type.(*ast.StarExpr)
	var typ ast.Expr = ast.NewIdent(recvTypeName)
	if !embeddedIsPtr && origRecvIsPtr {
		typ = &ast.StarExpr{token.NoPos, typ}
	}
	newField.Type = typ

	// copy existing receiver field list and set new receiver field
	newFieldList := *f.Decl.Recv
	newFieldList.List = []*ast.Field{&newField}

	// copy existing function declaration and set new receiver field list
	newFuncDecl := *f.Decl
	newFuncDecl.Recv = &newFieldList

	// copy existing function documentation and set new declaration
	newF := *f
	newF.Decl = &newFuncDecl
	newF.Recv = recvString(typ)
	// the Orig field never changes
	newF.Level = level

	return &newF
}

// collectEmbeddedMethods collects the embedded methods from
// all processed embedded types found in info in mset.
//
func collectEmbeddedMethods(mset methodSet, typ *baseType, recvTypeName string, embeddedIsPtr bool, level int) {
	for _, e := range typ.embedded {
		// Once an embedded type is embedded as a pointer type
		// all embedded types in those types are treated like
		// pointer types for the purpose of the receiver type
		// computation; i.e., embeddedIsPtr is sticky for this
		// embedding hierarchy.
		thisEmbeddedIsPtr := embeddedIsPtr || e.ptr
		for _, m := range e.typ.methods {
			// only top-level methods are embedded
			if m.Level == 0 {
				mset.add(customizeRecv(m, recvTypeName, thisEmbeddedIsPtr, level))
			}
		}
		collectEmbeddedMethods(mset, e.typ, recvTypeName, thisEmbeddedIsPtr, level+1)
	}
}

// computeMethodSets determines the actual method sets for each type encountered.
//
func (r *reader) computeMethodSets() {
	for _, t := range r.types {
		// collect embedded methods for t
		if t.isStruct {
			// struct
			collectEmbeddedMethods(t.methods, t, t.name, false, 1)
		} else {
			// interface
			// TODO(gri) fix this
		}
	}
}

// cleanupTypes removes the association of functions and methods with
// types that have no declaration. Instead, these functions and methods
// are shown at the package level. It also removes types with missing
// declarations or which are not visible.
// 
func (r *reader) cleanupTypes() {
	for _, t := range r.types {
		visible := r.isVisible(t.name)
		if t.decl == nil && (predeclaredTypes[t.name] || t.isEmbedded && visible) {
			// t.name is a predeclared type (and was not redeclared in this package),
			// or it was embedded somewhere but its declaration is missing (because
			// the AST is incomplete): move any associated values, funcs, and methods
			// back to the top-level so that they are not lost.
			// 1) move values
			r.values = append(r.values, t.values...)
			// 2) move factory functions
			for name, f := range t.funcs {
				r.funcs[name] = f
			}
			// 3) move methods
			for name, m := range t.methods {
				// don't overwrite functions with the same name - drop them
				if _, found := r.funcs[name]; !found {
					r.funcs[name] = m
				}
			}
		}
		// remove types w/o declaration or which are not visible
		if t.decl == nil || !visible {
			delete(r.types, t.name)
		}
	}
}

// ----------------------------------------------------------------------------
// Sorting

type data struct {
	n    int
	swap func(i, j int)
	less func(i, j int) bool
}

func (d *data) Len() int           { return d.n }
func (d *data) Swap(i, j int)      { d.swap(i, j) }
func (d *data) Less(i, j int) bool { return d.less(i, j) }

// sortBy is a helper function for sorting
func sortBy(less func(i, j int) bool, swap func(i, j int), n int) {
	sort.Sort(&data{n, swap, less})
}

func sortedKeys(m map[string]int) []string {
	list := make([]string, len(m))
	i := 0
	for key := range m {
		list[i] = key
		i++
	}
	sort.Strings(list)
	return list
}

// sortingName returns the name to use when sorting d into place.
//
func sortingName(d *ast.GenDecl) string {
	if len(d.Specs) == 1 {
		if s, ok := d.Specs[0].(*ast.ValueSpec); ok {
			return s.Names[0].Name
		}
	}
	return ""
}

func sortedValues(m []*Value, tok token.Token) []*Value {
	list := make([]*Value, len(m)) // big enough in any case
	i := 0
	for _, val := range m {
		if val.Decl.Tok == tok {
			list[i] = val
			i++
		}
	}
	list = list[0:i]

	sortBy(
		func(i, j int) bool {
			if ni, nj := sortingName(list[i].Decl), sortingName(list[j].Decl); ni != nj {
				return ni < nj
			}
			return list[i].order < list[j].order
		},
		func(i, j int) { list[i], list[j] = list[j], list[i] },
		len(list),
	)

	return list
}

func sortedTypes(m map[string]*baseType) []*Type {
	list := make([]*Type, len(m))
	i := 0
	for _, t := range m {
		list[i] = &Type{
			Doc:     t.doc,
			Name:    t.name,
			Decl:    t.decl,
			Consts:  sortedValues(t.values, token.CONST),
			Vars:    sortedValues(t.values, token.VAR),
			Funcs:   sortedFuncs(t.funcs),
			Methods: sortedFuncs(t.methods),
		}
		i++
	}

	sortBy(
		func(i, j int) bool { return list[i].Name < list[j].Name },
		func(i, j int) { list[i], list[j] = list[j], list[i] },
		len(list),
	)

	return list
}

func sortedFuncs(m methodSet) []*Func {
	list := make([]*Func, len(m))
	i := 0
	for _, m := range m {
		// exclude conflict entries
		if m.Decl != nil {
			list[i] = m
			i++
		}
	}
	list = list[0:i]
	sortBy(
		func(i, j int) bool { return list[i].Name < list[j].Name },
		func(i, j int) { list[i], list[j] = list[j], list[i] },
		len(list),
	)
	return list
}
