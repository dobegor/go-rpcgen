// Copyright 2012 Alec Thomas
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"strings"
	"text/template"
)

var rpcTemplate = `// Generated by go-rpcgen. Do not modify.

package {{.Package}}

import (
	"net/rpc"
{{range .Imports}}  "{{.}}"
{{end}})
{{$type := .Type}}
type {{.Type}}Service struct {
	impl {{.Type}}
}

func New{{.Type}}Service(impl {{.Type}}) *{{.Type}}Service {
	return &{{.Type}}Service{impl}
}

func Register{{.Type}}Service(impl {{.Type}}) error {
	return rpc.RegisterName("{{.Type}}", New{{.Type}}Service(impl))
}
{{range .Methods}}
type {{$type}}{{.Name}}Request struct {
	{{.Parameters | publicfields}}
}

type {{$type}}{{.Name}}Response struct {
	{{.Results | publicfields}}
}

func (s *{{$type}}Service) {{.Name}}(request *{{$type}}{{.Name}}Request, response *{{$type}}{{.Name}}Response) (err error) {
	{{.Results | publicrefswithprefix "response."}}{{if .Results}}, {{end}}err = s.impl.{{.Name}}({{.Parameters | publicrefswithprefix "request."}})
	return
}
{{end}}
type {{.Type}}Client struct {
	client {{.RpcType}}
	service string
}

func New{{.Type}}Client(client {{.RpcType}}) *{{.Type}}Client {
	return &{{.Type}}Client{client, "{{.Type}}"}
}

func (_c *{{$type}}Client) Close() error {
	return _c.client.Close()
}
{{range .Methods}}
func (_c *{{$type}}Client) {{.Name}}({{.Parameters | functionargs}}) ({{.Results | functionargs}}{{if .Results}}, {{end}}err error) {
	_request := &{{$type}}{{.Name}}Request{{"{"}}{{.Parameters | refswithprefix ""}}{{"}"}}
	_response := &{{$type}}{{.Name}}Response{}
	err = _c.client.Call(_c.service + ".{{.Name}}", _request, _response)
	return {{.Results | publicrefswithprefix "_response."}}{{if .Results}}, {{end}}err
}
{{end}}`

var usage = `usage: %s --source=<source.go> --type=<interface_type_name>

This utility generates server and client RPC stubs from a Go interface.

If you had a file "arith.go" containing this interface:

  package arith

  type Arith interface {
  	Add(a, b int)
  }

The following command will generate stubs for the interface:

  ./%s --source=arith.go --type=Arith

That will generate a file containing two types, ArithService and ArithClient,
that can be used with the Go RPC system, and as a client for the system,
respectively.

Flags:
`

var source = flag.String("source", "", "source file to parse RPC interface from")
var rpcType = flag.String("type", "", "type to generate RPC interface from")
var target = flag.String("target", "", "target file to write stubs to")
var importsFlag = flag.String("imports", "net/rpc", "list of imports to add")
var packageFlag = flag.String("package", "", "package to export under")
var rpcClientTypeFlag = flag.String("rpc_client_type", "*rpc.Client", "type to use for RPC client interfaces")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, usage, os.Args[0], os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *source == "" || *rpcType == "" {
		fatal("expected --source and --type")
	}
	if *target == "" {
		parts := strings.Split(*source, ".")
		parts = parts[:len(parts)-1]
		*target = strings.Join(parts, ".") + "rpc.go"
	}

	fileset := token.NewFileSet()
	f, err := parser.ParseFile(fileset, *source, nil, 0)
	if err != nil {
		fatal("failed to parse %s: %s", *source, err)
	}
	var imports []string
	if *importsFlag != "" {
		imports = strings.Split(*importsFlag, ",")
	}
	if *packageFlag == "" {
		*packageFlag = f.Name.Name
	}
	gen := &RpcGen{
		Type:    *rpcType,
		RpcType: *rpcClientTypeFlag,
		Package: *packageFlag,
		Methods: make([]*Method, 0),
		Imports: imports,
		fileset: fileset,
	}
	ast.Walk(gen, f)
	funcs := map[string]interface{}{
		"publicfields":         func(fields []*Type) string { return FieldList(fields, "", "\n\t", true, true) },
		"refswithprefix":       func(prefix string, fields []*Type) string { return FieldList(fields, prefix, ", ", false, false) },
		"publicrefswithprefix": func(prefix string, fields []*Type) string { return FieldList(fields, prefix, ", ", false, true) },
		"functionargs":         func(fields []*Type) string { return FieldList(fields, "", ", ", true, false) },
	}
	t, err := template.New("rpc").Funcs(funcs).Parse(rpcTemplate)
	if err != nil {
		fatal("failed to parse template: %s", err)
	}
	out, err := os.Create(*target)
	if err != nil {
		fatal("failed to create output file %s: %s", *target, err)
	}
	err = t.Execute(out, gen)
	if err != nil {
		fatal("failed to execute template: %s", err)
	}
	fmt.Printf("%s: wrote RPC stubs for %s to %s\n", os.Args[0], *rpcType, *target)
	if out, err := exec.Command("go", "fmt", *target).CombinedOutput(); err != nil {
		fatal("failed to run go fmt on %s: %s: %s", *target, err, string(out))
	}
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s: error: %s\n", os.Args[0], fmt.Sprintf(format, args...))
	os.Exit(1)
}

func fatalNode(fileset *token.FileSet, node ast.Node, format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s: error: %s: %s\n", os.Args[0], fileset.Position(node.Pos()).String(), fmt.Sprintf(format, args...))
	os.Exit(1)
}

type Type struct {
	Names      []string
	LowerNames []string
	Type       string
}

func (t *Type) NamesString() string {
	return strings.Join(t.Names, ", ")
}

func (t *Type) LowerNamesString() string {
	return strings.Join(t.LowerNames, ", ")
}

type Method struct {
	Name       string
	Parameters []*Type
	Results    []*Type
}

func FieldList(fields []*Type, prefix string, delim string, withTypes bool, public bool) string {
	var out []string
	for _, p := range fields {
		suffix := ""
		if withTypes {
			suffix = " " + p.Type
		}
		names := p.LowerNames
		if public {
			names = p.Names
		}
		var field []string
		for _, n := range names {
			field = append(field, prefix+n)
		}
		out = append(out, strings.Join(field, ", ")+suffix)
	}
	return strings.Join(out, delim)
}

type RpcGen struct {
	Type    string
	Package string
	Methods []*Method
	Imports []string
	RpcType string
	fileset *token.FileSet
}

func (r *RpcGen) Visit(node ast.Node) (w ast.Visitor) {
	switch n := node.(type) {
	case *ast.TypeSpec:
		name := n.Name.Name
		if name == r.Type {
			return &InterfaceGen{r}
		}
	}
	return r
}

type InterfaceGen struct {
	*RpcGen
}

func (r *InterfaceGen) Visit(node ast.Node) (w ast.Visitor) {
	switch n := node.(type) {
	case *ast.InterfaceType:
		for _, m := range n.Methods.List {
			switch t := m.Type.(type) {
			case *ast.FuncType:
				method := &Method{
					Name:       m.Names[0].Name,
					Parameters: make([]*Type, 0),
					Results:    make([]*Type, 0),
				}
				for _, v := range t.Params.List {
					method.Parameters = append(method.Parameters, formatType(r.fileset, v))
				}
				hasError := false
				if t.Results != nil {
					for _, v := range t.Results.List {
						result := formatType(r.fileset, v)
						if result.Type == "error" {
							hasError = true
						} else {
							method.Results = append(method.Results, result)
						}
					}
				}
				if !hasError {
					fatalNode(r.fileset, m, "method %s must have error as last return value", method.Name)
				}
				r.Methods = append(r.Methods, method)
			}
		}
	}
	return r.RpcGen
}

func formatType(fileset *token.FileSet, field *ast.Field) *Type {
	var typeBuf bytes.Buffer
	printer.Fprint(&typeBuf, fileset, field.Type)
	if len(field.Names) == 0 {
		fatalNode(fileset, field, "RPC interface parameters and results must all be named")
	}
	t := &Type{make([]string, 0), make([]string, 0), typeBuf.String()}
	for _, n := range field.Names {
		lowerName := n.Name
		name := strings.ToUpper(lowerName[0:1]) + lowerName[1:]
		t.Names = append(t.Names, name)
		t.LowerNames = append(t.LowerNames, lowerName)
	}
	return t
}
