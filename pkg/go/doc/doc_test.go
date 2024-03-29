// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"bytes"
	"flag"
	"fmt"
	"go/parser"
	"go/printer"
	"go/token"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

var update = flag.Bool("update", false, "update golden (.out) files")

const dataDir = "testdata"

var templateTxt = readTemplate("template.txt")

func readTemplate(filename string) *template.Template {
	t := template.New(filename)
	t.Funcs(template.FuncMap{
		"node":     nodeFmt,
		"synopsis": synopsisFmt,
	})
	return template.Must(t.ParseFiles(filepath.Join(dataDir, filename)))
}

func nodeFmt(node interface{}, fset *token.FileSet) string {
	var buf bytes.Buffer
	printer.Fprint(&buf, fset, node)
	return strings.Replace(strings.TrimSpace(buf.String()), "\n", "\n\t", -1)
}

func synopsisFmt(s string) string {
	const n = 64
	if len(s) > n {
		// cut off excess text and go back to a word boundary
		s = s[0:n]
		if i := strings.LastIndexAny(s, "\t\n "); i >= 0 {
			s = s[0:i]
		}
		s = strings.TrimSpace(s) + " ..."
	}
	return "// " + strings.Replace(s, "\n", " ", -1)
}

func isGoFile(fi os.FileInfo) bool {
	name := fi.Name()
	return !fi.IsDir() &&
		len(name) > 0 && name[0] != '.' && // ignore .files
		filepath.Ext(name) == ".go"
}

type bundle struct {
	*Package
	FSet *token.FileSet
}

func test(t *testing.T, mode Mode) {
	// get all packages
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dataDir, isGoFile, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	// test all packages
	for _, pkg := range pkgs {
		importpath := dataDir + "/" + pkg.Name
		doc := New(pkg, importpath, mode)

		// golden files always use / in filenames - canonicalize them
		for i, filename := range doc.Filenames {
			doc.Filenames[i] = filepath.ToSlash(filename)
		}

		// print documentation
		var buf bytes.Buffer
		if err := templateTxt.Execute(&buf, bundle{doc, fset}); err != nil {
			t.Error(err)
			continue
		}
		got := buf.Bytes()

		// update golden file if necessary
		golden := filepath.Join(dataDir, fmt.Sprintf("%s.%d.golden", pkg.Name, mode))
		if *update {
			err := ioutil.WriteFile(golden, got, 0644)
			if err != nil {
				t.Error(err)
			}
			continue
		}

		// get golden file
		want, err := ioutil.ReadFile(golden)
		if err != nil {
			t.Error(err)
			continue
		}

		// compare
		if bytes.Compare(got, want) != 0 {
			t.Errorf("package %s\n\tgot:\n%s\n\twant:\n%s", pkg.Name, got, want)
		}
	}
}

func Test(t *testing.T) {
	test(t, 0)
	test(t, AllDecls)
}
