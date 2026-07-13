package httpapi

import (
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
	"testing"
)

// The canonical error.code vocabulary — the machine-branchable discriminator of
// the frozen /v1 contract. This is the drift gate for that vocabulary: every
// code the server can emit must be listed here, and every listed code must be
// documented in the ErrorBody.Code doc tag (which is what `make spec` publishes
// into the OpenAPI description). Adding a NewError/OutboundError/WriteError
// site with a new code fails this test until the code is added BOTH here and to
// the published doc — new codes are allowed (the contract is an open set), but
// silent, undocumented vocabulary growth is not. Renaming or removing a code is
// BREAKING for clients that branch on it; treat any red diff here as a contract
// review, not a test to appease.
var emittedErrorCodes = []string{
	// auth
	"unauthorized",
	"forbidden",
	"blocked_by_policy",
	// validation
	"invalid_request",
	"invalid_cursor",
	"invalid_filter",
	"invalid_domain",
	"invalid_slug",
	"invalid_recipient",
	"invalid_attachment",
	"invalid_template",
	"invalid_event_type",
	"invalid_webhook_url",
	"invalid_expires_at",
	"invalid_scope",
	"reserved_domain",
	"too_many_recipients",
	"template_render_failed",
	"template_rendered_empty",
	"recipient_suppressed",
	// not found / gone
	"not_found",
	"attachment_not_found",
	"template_not_found",
	"starter_template_not_found",
	"gone",
	// conflict / state
	"conflict",
	"agent_taken",
	"domain_taken",
	"alias_taken",
	"message_not_pending",
	"webhook_disabled",
	"webhook_cooldown",
	"domain_not_registered",
	"domain_has_agents",
	"domain_not_verified",
	// capacity
	"limit_exceeded",
	"rate_limited",
	"template_limit_reached",
	"webhook_limit_reached",
	// idempotency
	"idempotency_in_flight",
	"idempotency_key_reuse",
	// size
	"payload_too_large",
	"attachment_too_large",
	// availability
	"not_implemented",
	"events_log_disabled",
	"limits_unavailable",
	// server
	"internal_error",
}

// fallbackOnlyErrorCodes are produced only by defaultCodeForStatus (no literal
// call site passes them), for statuses Huma or middleware can surface without a
// handler-chosen code. They are part of the published vocabulary too.
var fallbackOnlyErrorCodes = []string{
	"method_not_allowed",
	"unsupported_media_type",
	"error", // generic <500 fallback (e.g. 406 content negotiation)
}

var snakeCase = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

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
	seen := map[string]bool{}
	for _, code := range all {
		if seen[code] {
			continue
		}
		seen[code] = true
		if !snakeCase.MatchString(code) {
			t.Errorf("code %q violates snake_case", code)
		}
		if !strings.Contains(doc, code) {
			t.Errorf("code %q is not documented in the ErrorBody.Code doc tag (the published error.code catalog)", code)
		}
	}
}
