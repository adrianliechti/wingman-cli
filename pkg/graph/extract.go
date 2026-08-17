package graph

import (
	"path"
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

var importQueries = map[string]string{
	"go": `(import_spec path: (interpreted_string_literal) @path)`,

	"python": `(import_statement (dotted_name) @path)
(import_statement (aliased_import (dotted_name) @path))
(import_from_statement module_name: (dotted_name) @path)
(import_from_statement module_name: (relative_import) @rel)`,

	"javascript": `(import_statement source: (string) @path)
(export_statement source: (string) @path)`,

	"typescript": `(import_statement source: (string) @path)
(export_statement source: (string) @path)`,

	"tsx": `(import_statement source: (string) @path)
(export_statement source: (string) @path)`,
}

var tagsAugment = map[string]string{
	"go": `(type_spec name: (type_identifier) @name) @definition.type`,
}

var hierarchyQueries = map[string]string{
	"go": `(field_declaration !name (type_identifier) @inherits)
(field_declaration !name (qualified_type name: (type_identifier) @inherits))
(interface_type (type_elem (type_identifier) @inherits))`,

	"python": `(class_definition superclasses: (argument_list (identifier) @inherits))
(class_definition superclasses: (argument_list (attribute attribute: (identifier) @inherits)))`,

	"javascript": `(class_heritage (identifier) @inherits)
(class_heritage (member_expression property: (property_identifier) @inherits))`,

	"typescript": `(extends_clause (identifier) @inherits)
(extends_clause (member_expression property: (property_identifier) @inherits))
(implements_clause (type_identifier) @implements)
(extends_type_clause (type_identifier) @inherits)`,

	"tsx": `(extends_clause (identifier) @inherits)
(extends_clause (member_expression property: (property_identifier) @inherits))
(implements_clause (type_identifier) @implements)
(extends_type_clause (type_identifier) @inherits)`,
}

type rawImport struct {
	norm string
	rel  bool
}

type rawHierRef struct {
	name      string
	kind      EdgeKind
	startByte uint32
}

type auxExtractor struct {
	importQ map[string]*ts.Query
	hierQ   map[string]*ts.Query
}

func newAuxExtractor() *auxExtractor {
	return &auxExtractor{
		importQ: map[string]*ts.Query{},
		hierQ:   map[string]*ts.Query{},
	}
}

func (ax *auxExtractor) query(lang string, langObj *ts.Language, srcs map[string]string, cache map[string]*ts.Query) *ts.Query {
	if q, ok := cache[lang]; ok {
		return q
	}
	var q *ts.Query
	if s := srcs[lang]; s != "" {
		if compiled, err := ts.NewQuery(s, langObj); err == nil {
			q = compiled
		}
	}
	cache[lang] = q
	return q
}

// extractFromTree runs the import and hierarchy queries against an
// already-parsed tree, so the caller can share a single parse with the tagger.
func (ax *auxExtractor) extractFromTree(lang string, langObj *ts.Language, root *ts.Node, src []byte) ([]rawImport, []rawHierRef) {
	iq := ax.query(lang, langObj, importQueries, ax.importQ)
	hq := ax.query(lang, langObj, hierarchyQueries, ax.hierQ)
	if iq == nil && hq == nil {
		return nil, nil
	}

	var imps []rawImport
	if iq != nil {
		for _, m := range iq.ExecuteNode(root, langObj, src) {
			for _, cap := range m.Captures {
				text := cap.Node.Text(src)
				var norm string
				var rel bool
				if lang == "python" {
					rel = cap.Name == "rel"
					norm = normalizePython(text, rel)
				} else {
					s := strings.Trim(text, "\"'`")
					rel = strings.HasPrefix(s, ".")
					norm = s
				}
				if norm != "" {
					imps = append(imps, rawImport{norm: norm, rel: rel})
				}
			}
		}
	}

	var hiers []rawHierRef
	if hq != nil {
		for _, m := range hq.ExecuteNode(root, langObj, src) {
			for _, cap := range m.Captures {
				kind := EdgeInherits
				if cap.Name == "implements" {
					kind = EdgeImplements
				}
				hiers = append(hiers, rawHierRef{name: cap.Node.Text(src), kind: kind, startByte: cap.Node.StartByte()})
			}
		}
	}

	return imps, hiers
}

func normalizePython(text string, rel bool) string {
	if !rel {
		return strings.ReplaceAll(text, ".", "/")
	}
	level := 0
	for level < len(text) && text[level] == '.' {
		level++
	}
	rest := strings.ReplaceAll(text[level:], ".", "/")
	return strings.Repeat("../", level-1) + rest
}

func resolveImport(fromFile, norm string, rel bool, localDirs map[string]bool) string {
	if rel {
		base := path.Dir(fromFile)
		target := path.Clean(path.Join(base, norm))
		if localDirs[target] {
			return target
		}
		if d := path.Dir(target); localDirs[d] {
			return d
		}
		return ""
	}

	best := ""
	match := func(target string) {
		for d := range localDirs {
			if d == "." {
				continue
			}
			if target == d || strings.HasSuffix(target, "/"+d) {
				if len(d) > len(best) {
					best = d
				}
			}
		}
	}
	match(norm)
	if best == "" {
		if parent := path.Dir(norm); parent != "." && parent != "/" {
			match(parent)
		}
	}
	return best
}
