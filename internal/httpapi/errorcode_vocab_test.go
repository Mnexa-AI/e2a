package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var emittedErrorCodes = catalogCodes(false)
var fallbackOnlyErrorCodes = catalogCodes(true)

var snakeCase = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var documentedErrorCode = regexp.MustCompile("`([a-z][a-z0-9_]*)`")

// scanEmittedErrorCodes walks every non-test .go file under internal/ and
// collects the string-literal codes passed to the envelope constructors:
// NewError(status, code, …), WriteError(w, r, status, code, …),
// writeRawError(w, r, status, code, …) and OutboundError{status, code, …}
// composite literals (positional or keyed Code:). Dynamic (non-literal) code
// arguments — e.g. NewError(derr.Status, derr.Code, …) forwarding an
// OutboundError — are intentionally skipped: their codes are literals at the
// OutboundError construction site, which IS scanned.
func scanEmittedErrorCodes(t *testing.T) map[string][]string {
	t.Helper()
	root := filepath.Join("..", "..") // module root from internal/httpapi
	found := map[string][]string{}    // code -> emitting sites
	fset := token.NewFileSet()

	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		record := func(lit ast.Expr, node ast.Node) {
			bl, ok := lit.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				return // dynamic code (forwarded from an OutboundError site)
			}
			code, uerr := strconv.Unquote(bl.Value)
			if uerr != nil {
				return
			}
			pos := fset.Position(node.Pos())
			rel, _ := filepath.Rel(root, pos.Filename)
			found[code] = append(found[code], rel+":"+strconv.Itoa(pos.Line))
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				name := calleeName(node.Fun)
				switch name {
				case "NewError":
					if len(node.Args) >= 3 {
						record(node.Args[1], node)
					}
				case "WriteError", "writeRawError":
					if len(node.Args) >= 5 {
						record(node.Args[3], node)
					}
				}
			case *ast.CompositeLit:
				if typeName(node.Type) != "OutboundError" {
					return true
				}
				for i, elt := range node.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Code" {
							record(kv.Value, node)
						}
						continue
					}
					if i == 1 { // positional {status, code, msg}
						record(elt, node)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scanning internal/ for error-code call sites: %v", err)
	}
	return found
}

func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func typeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.StarExpr:
		return typeName(t.X)
	}
	return ""
}

type emittedErrorStatus struct {
	Code   string
	Status int
	Site   string
}

func scanLiteralErrorStatuses(t *testing.T) []emittedErrorStatus {
	t.Helper()
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var found []emittedErrorStatus
	record := func(codeExpr, statusExpr ast.Expr, node ast.Node) {
		codeLit, ok := codeExpr.(*ast.BasicLit)
		if !ok || codeLit.Kind != token.STRING {
			return
		}
		code, err := strconv.Unquote(codeLit.Value)
		if err != nil {
			return
		}
		status, ok := staticHTTPStatus(statusExpr)
		if !ok {
			return
		}
		pos := fset.Position(node.Pos())
		rel, _ := filepath.Rel(root, pos.Filename)
		found = append(found, emittedErrorStatus{Code: code, Status: status, Site: rel + ":" + strconv.Itoa(pos.Line)})
	}

	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				switch calleeName(node.Fun) {
				case "NewError":
					if len(node.Args) >= 3 {
						record(node.Args[1], node.Args[0], node)
					}
				case "WriteError", "writeRawError":
					if len(node.Args) >= 5 {
						record(node.Args[3], node.Args[2], node)
					}
				}
			case *ast.CompositeLit:
				if typeName(node.Type) != "OutboundError" {
					return true
				}
				var statusExpr, codeExpr ast.Expr
				for i, elt := range node.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if id, ok := kv.Key.(*ast.Ident); ok {
							switch id.Name {
							case "Status":
								statusExpr = kv.Value
							case "Code":
								codeExpr = kv.Value
							}
						}
					} else if i == 0 {
						statusExpr = elt
					} else if i == 1 {
						codeExpr = elt
					}
				}
				if statusExpr != nil && codeExpr != nil {
					record(codeExpr, statusExpr, node)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scanning literal error statuses: %v", err)
	}
	return found
}

func staticHTTPStatus(expr ast.Expr) (int, bool) {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.INT {
		status, err := strconv.Atoi(lit.Value)
		return status, err == nil
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	statuses := map[string]int{
		"StatusBadRequest":            http.StatusBadRequest,
		"StatusUnauthorized":          http.StatusUnauthorized,
		"StatusPaymentRequired":       http.StatusPaymentRequired,
		"StatusForbidden":             http.StatusForbidden,
		"StatusNotFound":              http.StatusNotFound,
		"StatusMethodNotAllowed":      http.StatusMethodNotAllowed,
		"StatusConflict":              http.StatusConflict,
		"StatusGone":                  http.StatusGone,
		"StatusRequestEntityTooLarge": http.StatusRequestEntityTooLarge,
		"StatusUnsupportedMediaType":  http.StatusUnsupportedMediaType,
		"StatusUnprocessableEntity":   http.StatusUnprocessableEntity,
		"StatusTooManyRequests":       http.StatusTooManyRequests,
		"StatusInternalServerError":   http.StatusInternalServerError,
		"StatusNotImplemented":        http.StatusNotImplemented,
		"StatusServiceUnavailable":    http.StatusServiceUnavailable,
	}
	status, ok := statuses[sel.Sel.Name]
	return status, ok
}

func statusAllowed(contract string, status int) bool {
	if contract == "5xx" {
		return status >= 500 && status < 600
	}
	if contract == "other 4xx" {
		return status >= 400 && status < 500
	}
	for _, candidate := range strings.Split(contract, "/") {
		if strings.TrimSpace(candidate) == strconv.Itoa(status) {
			return true
		}
	}
	return false
}

// TestErrorCodeVocabularyMatchesCatalog asserts the set of codes emitted from
// source exactly equals the canonical catalog above — no unlisted code can ship,
// and no listed code can silently stop being emitted (a dead catalog entry is a
// doc lie).
func TestErrorCodeVocabularyMatchesCatalog(t *testing.T) {
	found := scanEmittedErrorCodes(t)
	if len(found) == 0 {
		t.Fatal("scanned no error-code call sites — the AST walk or the source layout changed")
	}

	want := map[string]bool{}
	for _, c := range emittedErrorCodes {
		if want[c] {
			t.Errorf("catalog lists %q twice", c)
		}
		want[c] = true
	}

	var missing, extra []string
	for code, sites := range found {
		if !want[code] {
			extra = append(extra, code+" (emitted at "+strings.Join(sites, ", ")+")")
		}
	}
	for code := range want {
		if _, ok := found[code]; !ok {
			missing = append(missing, code)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("codes emitted but not in the canonical catalog (add to emittedErrorCodes AND the ErrorBody.Code doc):\n  %s", strings.Join(extra, "\n  "))
	}
	if len(missing) > 0 {
		t.Errorf("catalog codes no longer emitted anywhere (remove from emittedErrorCodes and the published doc, or restore the emitter):\n  %s", strings.Join(missing, "\n  "))
	}
}

func TestLiteralErrorStatusesMatchCatalog(t *testing.T) {
	contracts := make(map[string]string, len(errorCodeCatalog))
	for _, entry := range errorCodeCatalog {
		contracts[entry.Code] = entry.Status
	}
	for _, emitted := range scanLiteralErrorStatuses(t) {
		contract, ok := contracts[emitted.Code]
		if !ok {
			continue // the vocabulary test reports the missing catalog entry
		}
		if !statusAllowed(contract, emitted.Status) {
			t.Errorf("%s emits %s with HTTP %d; catalog allows %s", emitted.Site, emitted.Code, emitted.Status, contract)
		}
	}
}

// TestErrorCodeVocabularyIsDocumented asserts every code in the vocabulary —
// emitted, fallback-only, and everything defaultCodeForStatus can mint —
// appears verbatim in the ErrorBody.Code doc tag, which is the text `make spec`
// publishes as the error.code description. This keeps the published catalog
// complete by construction.
func TestErrorCodeVocabularyIsDocumented(t *testing.T) {
	field, ok := reflect.TypeOf(ErrorBody{}).FieldByName("Code")
	if !ok {
		t.Fatal("ErrorBody.Code field missing")
	}
	doc := field.Tag.Get("doc")
	if doc == "" {
		t.Fatal("ErrorBody.Code has no doc tag")
	}

	all := append(append([]string{}, emittedErrorCodes...), fallbackOnlyErrorCodes...)
	// Every status the fallback mapper handles must resolve to a documented code.
	for _, status := range []int{400, 401, 403, 404, 405, 406, 409, 413, 415, 422, 429, 500, 503} {
		all = append(all, defaultCodeForStatus(status))
	}
	want := map[string]bool{}
	for _, code := range all {
		if want[code] {
			continue
		}
		want[code] = true
		if !snakeCase.MatchString(code) {
			t.Errorf("code %q violates snake_case", code)
		}
	}

	const prefix = "Exact current vocabulary (machine-checked): "
	start := strings.Index(doc, prefix)
	if start < 0 {
		t.Fatalf("ErrorBody.Code doc tag is missing %q", prefix)
	}
	section := doc[start+len(prefix):]
	end := strings.Index(section, ". Grouped semantics:")
	if end < 0 {
		t.Fatal("ErrorBody.Code exact vocabulary must end before '. Grouped semantics:'")
	}
	got := map[string]bool{}
	for _, token := range strings.Split(section[:end], ",") {
		code := strings.TrimSpace(token)
		if code == "" {
			continue
		}
		if got[code] {
			t.Errorf("ErrorBody.Code exact vocabulary lists %q twice", code)
		}
		got[code] = true
	}
	var missing, extra []string
	for code := range want {
		if !got[code] {
			missing = append(missing, code)
		}
	}
	for code := range got {
		if !want[code] {
			extra = append(extra, code)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("ErrorBody.Code exact vocabulary drift: missing=%v extra=%v", missing, extra)
	}
}

// TestDocsErrorCodeTableMatchesCatalog freezes docs/api.md as the human-facing
// view of the same contract catalog used by the server and OpenAPI. Keeping the
// status spelling exact makes ambiguous pairings (such as 400 / 422) visible.
func TestDocsErrorCodeTableMatchesCatalog(t *testing.T) {
	t.Helper()
	docBytes, err := os.ReadFile(filepath.Join("..", "..", "docs", "api.md"))
	if err != nil {
		t.Fatalf("read docs/api.md: %v", err)
	}
	doc := string(docBytes)
	start := strings.Index(doc, "## Error codes")
	end := strings.Index(doc, "## Versioning & stability")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("docs/api.md must contain an Error codes section before Versioning & stability")
	}

	documented := map[string]string{}
	for _, line := range strings.Split(doc[start:end], "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			continue
		}
		status := strings.TrimSpace(cells[2])
		for _, match := range documentedErrorCode.FindAllStringSubmatch(cells[1], -1) {
			documented[match[1]] = status
		}
	}

	want := make(map[string]string, len(errorCodeCatalog))
	for _, entry := range errorCodeCatalog {
		want[entry.Code] = entry.Status
	}
	var missing, extra, mismatched []string
	for code, status := range want {
		got, ok := documented[code]
		if !ok {
			missing = append(missing, code)
		} else if got != status {
			mismatched = append(mismatched, code+": want "+status+", got "+got)
		}
	}
	for code := range documented {
		if _, ok := want[code]; !ok {
			extra = append(extra, code)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(mismatched)
	if len(missing) > 0 {
		t.Errorf("catalog codes missing from docs/api.md: %s", strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Errorf("docs/api.md codes missing from the canonical catalog: %s", strings.Join(extra, ", "))
	}
	if len(mismatched) > 0 {
		t.Errorf("docs/api.md status drift:\n  %s", strings.Join(mismatched, "\n  "))
	}
}
