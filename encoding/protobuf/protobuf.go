// Copyright 2019 CUE Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package protobuf defines functionality for parsing protocol buffer
// definitions and instances.
//
// Protobuf definitions can be annotated with CUE constraints that are
// included in the generated CUE:
//    (cue.val)     string        CUE expression defining a constraint for this
//                                field. The string may refer to other fields
//                                in a message definition using their JSON name.
//
//    (cue.opt)     FieldOptions
//       required   bool          Defines the field is required. Use with
//                                caution.
//
package protobuf

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"
	"github.com/mpvl/unique"
)

// Config specifies the environment into which to parse a proto definition file.
type Config struct {
	// Root specifies the root of the CUE project, which typically coincides
	// with, for example, a version control repository root or the Go module.
	// Any imports of proto files within the directory tree of this of this root
	// are considered to be "project files" and are generated at the
	// corresponding location with this hierarchy. Any other imports are
	// considered to be external. Files for such imports are rooted under the
	// $Root/pkg/, using the Go package path specified in the .proto file.
	Root string

	// Module is the Go package import path of the module root. It is the value
	// as after "module" in a go.mod file, if a module file is present.
	Module string // TODO: determine automatically if unspecified.

	// Paths defines the include directory in which to search for imports.
	Paths []string
}

// An Extractor converts a collection of proto files, typically belonging to one
// repo or module, to CUE. It thereby observes the CUE package layout.
//
// CUE observes the same package layout as Go and requires .proto files to have
// the go_package directive. Generated CUE files are put in the same directory
// as their corresponding .proto files if the .proto files are located in the
// specified Root (or current working directory if none is specified).
// All other imported files are assigned to the CUE pkg dir ($Root/pkg)
// according to their Go package import path.
//
type Extractor struct {
	root   string
	cwd    string
	module string
	paths  []string

	fileCache map[string]result
	instCache map[string]*build.Instance
	imports   map[string]*build.Instance

	errs errors.Error
	done bool
}

type result struct {
	p   *protoConverter
	err error
}

// NewExtractor creates an Extractor. If the configuration contained any errors
// it will be observable by the Err method fo the Extractor. It is safe,
// however, to only check errors after building the output.
func NewExtractor(c *Config) *Extractor {
	cwd, _ := os.Getwd()
	b := &Extractor{
		root:      c.Root,
		cwd:       cwd,
		paths:     c.Paths,
		module:    c.Module,
		fileCache: map[string]result{},
		imports:   map[string]*build.Instance{},
	}

	if b.root == "" {
		b.root = b.cwd
	}

	return b
}

// Err returns the errors accumulated during testing. The returned error may be
// of type cuelang.org/go/cue/errors.List.
func (b *Extractor) Err() error {
	return b.errs
}

func (b *Extractor) addErr(err error) {
	b.errs = errors.Append(b.errs, errors.Promote(err, "unknown error"))
}

// AddFile adds a proto definition file to be converted into CUE by the builder.
// Relatives paths are always taken relative to the Root with which the b is
// configured.
//
// AddFile assumes that the proto file compiles with protoc and may not report
// an error if it does not. Imports are resolved using the paths defined in
// Config.
//
func (b *Extractor) AddFile(filename string, src interface{}) error {
	if b.done {
		err := errors.Newf(token.NoPos,
			"protobuf: cannot call AddFile: Instances was already called")
		b.errs = errors.Append(b.errs, err)
		return err
	}
	if b.root != b.cwd && !filepath.IsAbs(filename) {
		filename = filepath.Join(b.root, filename)
	}
	_, err := b.parse(filename, src)
	return err
}

// TODO: some way of (recursively) adding multiple proto files with filter.

// Files returns a File for each proto file that was added or imported,
// recursively.
func (b *Extractor) Files() (files []*ast.File, err error) {
	defer func() { err = b.Err() }()
	b.done = true

	instances, err := b.Instances()
	if err != nil {
		return nil, err
	}

	for _, p := range instances {
		for _, f := range p.Files {
			files = append(files, f)
		}
	}
	return files, nil
}

// Instances creates a build.Instances for every package for which a proto file
// was added to the builder. This includes transitive dependencies. It does not
// write the generated files to disk.
//
// The returned instances can be passed to cue.Build to generated the
// corresponding CUE instances.
//
// All import paths are located within the specified Root, where external
// packages are located under $Root/pkg. Instances for builtin (like time)
// packages may be omitted, and if not will have no associated files.
func (b *Extractor) Instances() (instances []*build.Instance, err error) {
	defer func() { err = b.Err() }()
	b.done = true

	for _, r := range b.fileCache {
		if r.err != nil {
			b.addErr(r.err)
			continue
		}
		inst := b.getInst(r.p)
		if inst == nil {
			continue
		}

		// Set canonical CUE path for generated file.
		f := r.p.file
		base := filepath.Base(f.Filename)
		base = base[:len(base)-len(".proto")] + "_proto_gen.cue"
		f.Filename = filepath.Join(inst.Dir, base)
		buf, err := format.Node(f)
		if err != nil {
			b.addErr(err)
			// return nil, err
			continue
		}
		f, err = parser.ParseFile(f.Filename, buf, parser.ParseComments)
		if err != nil {
			panic(err)
			b.addErr(err)
			// return nil, err
			continue
		}

		inst.Files = append(inst.Files, f)
		// inst.CUEFiles = append(inst.CUEFiles, f.Filename)
		// err := parser.Resolve(f)
		// if err != nil {
		// 	return nil, err
		// }

		for pkg := range r.p.used {
			inst.ImportPaths = append(inst.ImportPaths, pkg)
		}
	}

	for _, p := range b.imports {
		instances = append(instances, p)
		sort.Strings(p.ImportPaths)
		unique.Strings(&p.ImportPaths)
		for _, i := range p.ImportPaths {
			if imp := b.imports[i]; imp != nil {
				p.Imports = append(p.Imports, imp)
			}
		}

		sort.Slice(p.Files, func(i, j int) bool {
			return p.Files[i].Filename < p.Files[j].Filename
		})
	}
	sort.Slice(instances, func(i, j int) bool {
		return instances[i].ImportPath < instances[j].ImportPath
	})

	if err != nil {
		return instances, err
	}
	return instances, nil
}

func (b *Extractor) getInst(p *protoConverter) *build.Instance {
	if b.errs != nil {
		return nil
	}
	importPath := p.goPkgPath
	if importPath == "" {
		err := errors.Newf(token.NoPos,
			"no go_package for proto package %q in file %s", p.id, p.file.Filename)
		b.errs = errors.Append(b.errs, err)
		// TODO: find an alternative. Is proto package good enough?
		return nil
	}

	dir := b.root
	path := importPath
	if !strings.HasPrefix(path, b.module) {
		dir = filepath.Join(dir, "pkg", path)
	} else {
		dir = filepath.Join(dir, path[len(b.module)+1:])
		want := filepath.Dir(p.file.Filename)
		if !filepath.IsAbs(want) {
			want = filepath.Join(b.root, want)
		}
		if dir != want {
			err := errors.Newf(token.NoPos,
				"file %s mapped to inconsistent path %s; module name %q may be inconsistent with root dir %s",
				want, dir, b.module, b.root,
			)
			b.errs = errors.Append(b.errs, err)
		}
	}

	inst := b.imports[importPath]
	if inst == nil {
		inst = &build.Instance{
			Root:        b.root,
			Dir:         dir,
			ImportPath:  importPath,
			PkgName:     p.goPkg,
			DisplayPath: p.protoPkg,
		}
		b.imports[importPath] = inst
	}
	return inst
}

// Extract parses a single proto file and returns its contents translated to a CUE
// file. If src is not nil, it will use this as the contents of the file. It may
// be a string, []byte or io.Reader. Otherwise Extract will open the given file
// name at the fully qualified path.
//
// Extract assumes the proto file compiles with protoc and may not report an error
// if it does not. Imports are resolved using the paths defined in Config.
//
func Extract(filename string, src interface{}, c *Config) (f *ast.File, err error) {
	if c == nil {
		c = &Config{}
	}
	b := NewExtractor(c)

	p, err := b.parse(filename, src)
	if err != nil {
		return nil, err
	}
	p.file.Filename = filename[:len(filename)-len(".proto")] + "_gen.cue"
	return p.file, b.Err()
}

// TODO
// func GenDefinition

// func MarshalText(cue.Value) (string, error) {
// 	return "", nil
// }

// func MarshalBytes(cue.Value) ([]byte, error) {
// 	return nil, nil
// }

// func UnmarshalText(descriptor cue.Value, b string) (ast.Expr, error) {
// 	return nil, nil
// }

// func UnmarshalBytes(descriptor cue.Value, b []byte) (ast.Expr, error) {
// 	return nil, nil
// }
