// Package configref builds docs/configuration.md, the reference of every
// setting a Līdza app reads, from the framework's source: the env tags of
// the packs' Config structs, the Env constants, and the settings read by
// name, each with its doc comment. `go generate ./pkg/configref` writes
// it; the package test fails while the file is stale or a setting read by
// name is undocumented.
package configref

//go:generate go run ./gen

import (
	"fmt"

	"github.com/agim/lidza/pkg/env"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// File is the reference, relative to the repository root.
const File = "docs/configuration.md"

// AdminFile is the admin pack's table of the app settings, relative to
// the repository root: the Settings page edits each one.
const AdminFile = "packs/admin/appsettings_gen.go"

// Setting is one environment variable.
type Setting struct {
	Name     string
	Default  string
	Required bool
	Doc      string
	Area     string // "packs/auth", "core"
	// Kind is env.KindInfra (the deployment's environment) or
	// env.KindApp (stored in the app).
	Kind string
}

// skipped are directories whose settings belong to the CLI or tests, not
// to a running app.
var skipped = map[string]bool{
	"cmd": true, "evals": true, "examples": true, "templates": true, "core": true, "scripts": true, "docs": true,
	"pkg/scaffold": true, "pkg/snippets": true, "pkg/recipes": true, "pkg/mcpserver": true, "pkg/inspect": true,
	"pkg/diag": true, "pkg/apidoc": true, "pkg/lidzatest": true, "pkg/sdk": true, "pkg/report": true,
	"pkg/brief": true, "pkg/decisions": true, "pkg/version": true, "pkg/pack": true, "pkg/configref": true,
}

// byName documents the settings read by name, outside a Config struct or
// an Env constant.
var byName = map[string]Setting{
	"APP_URL":                            {Area: "core", Doc: "The app's public address (https://app.example.com): links in mail, sign-in redirects, billing return pages, security.txt, HSTS when https."},
	"SECURITY_CONTACT":                   {Area: "core", Doc: "Where to report a vulnerability (mailto: or https:); with it the app serves /.well-known/security.txt."},
	"SECURITY_POLICY":                    {Area: "core", Doc: "The URL of the security policy security.txt links to."},
	"AUTH_PROVIDERS":                     {Area: "packs/auth", Doc: "The sign-in providers auth.Mount serves, comma-separated (google, github, microsoft, or any name with AUTH_<NAME>_ISSUER for an OIDC issuer); each needs AUTH_<NAME>_CLIENT_ID and AUTH_<NAME>_CLIENT_SECRET, and Microsoft may take AUTH_MICROSOFT_TENANT."},
	"AUTH_CONNECT":                       {Area: "packs/auth", Doc: "The external accounts a signed-in user can connect, comma-separated; each needs AUTH_CONNECT_<NAME>_CLIENT_ID and AUTH_CONNECT_<NAME>_CLIENT_SECRET."},
	"PGHOST":                             {Area: "packs/db", Doc: "Read by the local database lookup (LocalURL) when DATABASE_URL is unset in development."},
	"PGPORT":                             {Area: "packs/db", Doc: "Read with PGHOST by the local database lookup."},
	"OTEL_EXPORTER_OTLP_ENDPOINT":        {Area: "pkg/tracing", Doc: "OTLP/HTTP collector address (http://collector:4318); setting it turns tracing on. The other standard OTEL_* variables apply (OTEL_SERVICE_NAME, OTEL_TRACES_SAMPLER, OTEL_EXPORTER_OTLP_HEADERS, OTEL_RESOURCE_ATTRIBUTES)."},
	"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": {Area: "pkg/tracing", Doc: "The traces endpoint alone, in place of OTEL_EXPORTER_OTLP_ENDPOINT."},
	"OTEL_SERVICE_NAME":                  {Area: "pkg/tracing", Doc: "The service name on spans; default: the app's name."},
	"OTEL_SDK_DISABLED":                  {Area: "pkg/tracing", Doc: "true turns tracing off even with an endpoint set."},
}

var envName = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$`)

// Collect reads the settings under root.
func Collect(root string) ([]Setting, error) {
	found := map[string]Setting{}
	add := func(s Setting) {
		if old, ok := found[s.Name]; ok && old.Doc != "" {
			if old.Default == "" {
				old.Default = s.Default
			}
			found[s.Name] = old
			return
		}
		found[s.Name] = s
	}
	read := map[string]bool{}
	goNames := map[string]string{} // EnvLogLevel -> LIDZA_LOG_LEVEL
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skipped[rel] || strings.HasPrefix(d.Name(), ".") && rel != "." || d.Name() == "testdata" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), "_") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		area := filepath.ToSlash(filepath.Dir(rel))
		if area == "." {
			area = "core"
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.StructType:
				// A field without a comment shares the one before it when
				// that names it ("LoginRPS and LoginBurst bound ...").
				prev := ""
				local := map[string]string{} // this struct's field names
				for _, field := range n.Fields.List {
					if field.Tag != nil && len(field.Names) > 0 {
						if env := reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Get("env"); env != "" {
							local[field.Names[0].Name] = env
						}
					}
				}
				for _, field := range n.Fields.List {
					doc := docOf(field.Doc, field.Comment)
					if len(field.Names) > 0 && doc == "" && strings.Contains(prev, field.Names[0].Name) {
						doc = prev
					}
					if doc != "" {
						prev = doc
					}
					if field.Tag == nil || len(field.Names) == 0 {
						continue
					}
					tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
					name := tag.Get("env")
					if name == "" {
						continue
					}
					add(Setting{Name: name, Default: tag.Get("default"), Required: tag.Get("required") == "true",
						Doc: withNames(clean(field.Names[0].Name, doc), local), Area: area})
				}
			case *ast.GenDecl:
				if n.Tok != token.CONST {
					return true
				}
				prev := ""
				for _, spec := range n.Specs {
					vs := spec.(*ast.ValueSpec)
					specDoc := docOf(vs.Doc, vs.Comment)
					if specDoc == "" && len(vs.Names) > 0 && strings.Contains(prev, vs.Names[0].Name) {
						specDoc = prev
					}
					if specDoc != "" {
						prev = specDoc
					}
					for i, id := range vs.Names {
						if i >= len(vs.Values) || !(strings.HasPrefix(id.Name, "Env") || strings.HasSuffix(id.Name, "Setting")) {
							continue
						}
						lit, ok := vs.Values[i].(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						v, _ := strconv.Unquote(lit.Value)
						if !envName.MatchString(v) {
							continue
						}
						doc := specDoc
						if doc == "" && len(n.Specs) == 1 {
							doc = docOf(n.Doc, nil)
						}
						goNames[id.Name] = v
						add(Setting{Name: v, Doc: clean(id.Name, doc), Area: area})
					}
				}
			case *ast.IndexExpr:
				// values["X"]: a setting read from the merged map.
				if lit, ok := n.Index.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, _ := strconv.Unquote(lit.Value); envName.MatchString(v) {
						if id, ok := n.X.(*ast.Ident); ok && (id.Name == "values" || id.Name == "settings") {
							read[v] = true
						}
					}
				}
			case *ast.CallExpr:
				// os.Getenv("X"), settings("X"), values["X"] and the like.
				if len(n.Args) == 1 {
					if lit, ok := n.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, _ := strconv.Unquote(lit.Value); envName.MatchString(v) && isSettingCall(n.Fun) {
							read[v] = true
						}
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	var missing []string
	for name := range read {
		if _, ok := found[name]; ok {
			continue
		}
		s, ok := byName[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		s.Name = name
		found[name] = s
	}
	// byName wins for the settings it names (APP_URL is the app's, not
	// the first pack's that reads it), keeping a default found in a tag.
	for name, s := range byName {
		s.Name = name
		s.Default = found[name].Default
		found[name] = s
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("configref: settings read by name with no entry in byName: %s", strings.Join(missing, ", "))
	}
	// Env constant names in the text become the settings they hold.
	out := make([]Setting, 0, len(found))
	for _, s := range found {
		s.Doc = withNames(s.Doc, goNames)
		s.Kind = env.Kind(s.Name)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Area != out[j].Area {
			return areaOrder(out[i].Area) < areaOrder(out[j].Area)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// withNames replaces Go names in doc with the settings they stand for,
// longest first.
func withNames(doc string, names map[string]string) string {
	keys := make([]string, 0, len(names))
	for n := range names {
		keys = append(keys, n)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, n := range keys {
		doc = regexp.MustCompile(`\b`+regexp.QuoteMeta(n)+`\b`).ReplaceAllString(doc, names[n])
	}
	return doc
}

func isSettingCall(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		return f.Sel.Name == "Getenv" || f.Sel.Name == "LookupEnv"
	case *ast.Ident:
		return f.Name == "settings" || f.Name == "setting"
	}
	return false
}

func areaOrder(a string) string {
	switch {
	case a == "core":
		return "0"
	case strings.HasPrefix(a, "pkg/"):
		return "1" + a
	}
	return "2" + a
}

func docOf(groups ...*ast.CommentGroup) string {
	for _, g := range groups {
		if g != nil {
			if t := strings.TrimSpace(g.Text()); t != "" {
				return strings.Join(strings.Fields(t), " ")
			}
		}
	}
	return ""
}

// clean drops a leading Go name ("MaxConns bounds the pool" becomes
// "Bounds the pool") and escapes table pipes.
func clean(goName, doc string) string {
	if rest, ok := strings.CutPrefix(doc, goName+" "); ok && rest != "" && !strings.HasPrefix(rest, "and ") {
		for _, verb := range []string{"is ", "are "} {
			rest = strings.TrimPrefix(rest, verb)
		}
		r := []rune(rest)
		if first, _, _ := strings.Cut(rest, " "); !strings.Contains(first, "://") {
			r[0] = unicode.ToUpper(r[0])
		}
		doc = string(r)
	}
	return strings.ReplaceAll(doc, "|", `\|`)
}

// Render is the Markdown reference.
func Render(settings []Setting) string {
	var b strings.Builder
	b.WriteString("# Configuration\n\n" +
		"Every setting a Līdza app reads, from the framework's source (`go generate ./pkg/configref` writes this file).\n" +
		"An app reads `.env`, then `.env.<mode>`, then the sealed credentials file and the values saved in its database, then the process environment; later wins.\n" +
		"A pack's settings matter only when the pack is enabled in `lidza.json`.\n\n" +
		"**Kind** says where a setting belongs. `app`: the app's configuration (keys, providers, addresses, senders), stored in the app's\n" +
		"database (the admin pages, `lidza settings set`), so the app moves with its database; the sealed credentials file seeds development.\n" +
		"`infra`: the deployment's environment: the database, the master key, addresses, logging, tracing and tuning.\n")
	area := ""
	for _, s := range settings {
		if s.Area != area {
			area = s.Area
			title := "Core"
			if area != "core" {
				title = "`" + area + "`"
			}
			b.WriteString("\n## " + title + "\n\n| Setting | Kind | Default | Description |\n|---|---|---|---|\n")
		}
		def := "`" + s.Default + "`"
		switch {
		case s.Required:
			def = "required"
		case s.Default == "":
			def = ""
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", s.Name, s.Kind, def, s.Doc)
	}
	return b.String()
}

// RenderAdmin is the admin pack's table of app settings (env.KindApp),
// as Go: name, area, default, description.
func RenderAdmin(settings []Setting) string {
	var b strings.Builder
	b.WriteString("// Code generated by go generate ./pkg/configref. DO NOT EDIT.\n\npackage admin\n\n" +
		"// appSettings are the app settings the framework reads (env.KindApp),\n" +
		"// from docs/configuration.md: the Settings page offers each one the\n" +
		"// pack pages do not.\n" +
		"var appSettings = []appSetting{\n")
	for _, s := range settings {
		if s.Kind != "app" {
			continue
		}
		fmt.Fprintf(&b, "\t{Name: %q, Area: %q, Default: %q, Doc: %q},\n", s.Name, s.Area, s.Default, strings.ReplaceAll(s.Doc, `\|`, "|"))
	}
	b.WriteString("}\n")
	return b.String()
}
