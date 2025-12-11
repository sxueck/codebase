package parser

import (
	"path/filepath"
	"regexp"
	"strings"
)

// PythonParser implements LanguageParser for Python language
type PythonParser struct{}

// NewPythonParser creates a new Python parser
func NewPythonParser() *PythonParser {
	return &PythonParser{}
}

// Language returns the language name
func (p *PythonParser) Language() string {
	return string(LanguagePython)
}

var (
	pyFuncRegex  = regexp.MustCompile(`^\s*def\s+([A-Za-z_]\w*)\s*\(`)
	pyClassRegex = regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`)
)

type pythonLine struct {
	text       string
	indent     int
	startByte  int
	endByte    int
	lineNumber int
}

type pythonBlock struct {
	indent int
	name   string
}

// ExtractFunctions extracts function and class method definitions from Python source code.
func (p *PythonParser) ExtractFunctions(filePath string, code []byte) ([]FunctionNode, error) {
	lines := splitPythonLines(code)
	var functions []FunctionNode
	var classStack []pythonBlock

	// Derive a lightweight package/module name from the file path
	pkgName := derivePythonPackageName(filePath)
	imports := extractPythonImports(lines)

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line.text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		for len(classStack) > 0 && line.indent <= classStack[len(classStack)-1].indent {
			classStack = classStack[:len(classStack)-1]
		}

		if matches := pyClassRegex.FindStringSubmatch(line.text); matches != nil {
			classStack = append(classStack, pythonBlock{
				indent: line.indent,
				name:   matches[1],
			})
			continue
		}

		if matches := pyFuncRegex.FindStringSubmatch(line.text); matches != nil {
			name := matches[1]
			nodeType := "function"
			if len(classStack) > 0 {
				nodeType = "method"
				name = classStack[len(classStack)-1].name + "." + name
			}

			endIdx := findPythonBlockEnd(lines, i, line.indent)
			startOffset := line.startByte
			endOffset := lines[endIdx].endByte
			if endOffset <= startOffset {
				continue
			}

			startLine := line.lineNumber
			endLine := lines[endIdx].lineNumber
			if endLine-startLine < 2 {
				continue
			}

			// Best-effort extraction of signature, parameter types and return
			// annotation from the "def" line.
			signature, paramTypes, returnTypes := parsePythonSignature(line.text)

			// Collect simple callee names from the function body to enrich
			// cross-reference metadata.
			callees := extractPythonCallees(code, startOffset, endOffset)

			functions = append(functions, FunctionNode{
				Name:        name,
				NodeType:    nodeType,
				StartLine:   startLine,
				EndLine:     endLine,
				Content:     string(code[startOffset:endOffset]),
				StartByte:   startOffset,
				EndByte:     endOffset,
				PackageName: pkgName,
				Imports:     append([]string(nil), imports...),
				Signature:   signature,
				// Receiver is not modeled separately for Python; methods are
				// encoded via the qualified Name (Class.method).
				Doc:            "", // Python docstrings are not yet extracted
				Callees:        callees,
				ParamTypes:     paramTypes,
				ReturnTypes:    returnTypes,
				HasErrorReturn: false,
			})
		}
	}

	return functions, nil
}

func splitPythonLines(code []byte) []pythonLine {
	var lines []pythonLine
	start := 0
	lineNumber := 1
	for i := 0; i < len(code); i++ {
		if code[i] == '\n' {
			lines = append(lines, buildPythonLine(code, start, i+1, lineNumber))
			start = i + 1
			lineNumber++
		}
	}
	if start < len(code) {
		lines = append(lines, buildPythonLine(code, start, len(code), lineNumber))
	} else if len(code) == 0 {
		lines = append(lines, pythonLine{
			text:       "",
			indent:     0,
			startByte:  0,
			endByte:    0,
			lineNumber: 1,
		})
	}
	return lines
}

func buildPythonLine(code []byte, start, end, lineNumber int) pythonLine {
	length := end - start
	text := code[start:end]
	if length > 0 && text[length-1] == '\r' {
		text = text[:length-1]
	}
	return pythonLine{
		text:       string(text),
		indent:     countPythonIndent(text),
		startByte:  start,
		endByte:    end,
		lineNumber: lineNumber,
	}
}

func countPythonIndent(line []byte) int {
	count := 0
	for _, b := range line {
		if b == ' ' {
			count++
		} else if b == '\t' {
			count += 4
		} else {
			break
		}
	}
	return count
}

func findPythonBlockEnd(lines []pythonLine, startIdx, indent int) int {
	endIdx := startIdx
	for i := startIdx + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line.text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			endIdx = i
			continue
		}
		if line.indent <= indent {
			break
		}
		endIdx = i
	}
	return endIdx
}

// derivePythonPackageName computes a simple module name from the file path,
// e.g. "pkg/module.py" -> "module".
func derivePythonPackageName(filePath string) string {
	if filePath == "" {
		return ""
	}
	base := filepath.Base(filePath)
	// Strip extension if present
	if dot := strings.LastIndexByte(base, '.'); dot > 0 {
		base = base[:dot]
	}
	return base
}

// extractPythonImports collects import/module references at file level so
// they can be attached to each FunctionNode for richer retrieval context.
func extractPythonImports(lines []pythonLine) []string {
	var imports []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line.text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(trimmed, "import ") && !strings.HasPrefix(trimmed, "from ") {
			continue
		}

		if strings.HasPrefix(trimmed, "import ") {
			// Handle patterns like "import a", "import a as b", "import a, b"
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "import "))
			parts := strings.Split(rest, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if strings.Contains(part, " as ") {
					aliasParts := strings.SplitN(part, " as ", 2)
					mod := strings.TrimSpace(aliasParts[0])
					alias := strings.TrimSpace(aliasParts[1])
					if alias != "" && mod != "" {
						imports = append(imports, alias+"="+mod)
						continue
					}
				}
				imports = append(imports, part)
			}
			continue
		}

		// from pkg.subpkg import a, b as c
		if strings.HasPrefix(trimmed, "from ") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "from "))
			parts := strings.SplitN(rest, " import ", 2)
			if len(parts) != 2 {
				continue
			}
			module := strings.TrimSpace(parts[0])
			namesPart := strings.TrimSpace(parts[1])
			if module == "" || namesPart == "" {
				continue
			}
			// Remove enclosing parentheses if any: from x import (a, b)
			if strings.HasPrefix(namesPart, "(") && strings.HasSuffix(namesPart, ")") {
				namesPart = strings.TrimSpace(namesPart[1 : len(namesPart)-1])
			}
			for _, nameSpec := range strings.Split(namesPart, ",") {
				nameSpec = strings.TrimSpace(nameSpec)
				if nameSpec == "" {
					continue
				}
				alias := ""
				name := nameSpec
				if strings.Contains(nameSpec, " as ") {
					aliasParts := strings.SplitN(nameSpec, " as ", 2)
					name = strings.TrimSpace(aliasParts[0])
					alias = strings.TrimSpace(aliasParts[1])
				}
				full := module
				if name != "*" && name != "" {
					full = module + "." + name
				}
				if alias != "" {
					imports = append(imports, alias+"="+full)
				} else {
					imports = append(imports, full)
				}
			}
		}
	}
	return imports
}

// parsePythonSignature extracts a readable signature along with simple
// parameter and return type information from a "def" line.
func parsePythonSignature(lineText string) (string, []string, []string) {
	trimmed := strings.TrimSpace(lineText)
	if !strings.HasPrefix(trimmed, "def ") {
		return "", nil, nil
	}

	// Use the portion from "def" up to the trailing colon as the signature.
	sigEnd := strings.Index(trimmed, ":")
	if sigEnd < 0 {
		sigEnd = len(trimmed)
	}
	signature := trimmed[:sigEnd]

	open := strings.Index(trimmed, "(")
	close := strings.LastIndex(trimmed, ")")
	var paramTypes []string
	var returnTypes []string
	if open >= 0 && close > open {
		paramsPart := trimmed[open+1 : close]
		for _, p := range strings.Split(paramsPart, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			// Strip default value
			if eq := strings.Index(p, "="); eq >= 0 {
				p = strings.TrimSpace(p[:eq])
			}
			if colon := strings.Index(p, ":"); colon >= 0 {
				typePart := strings.TrimSpace(p[colon+1:])
				if typePart != "" {
					paramTypes = append(paramTypes, typePart)
					continue
				}
			}
			// No explicit type; still record a placeholder for arity.
			paramTypes = append(paramTypes, "")
		}

		// Look for return annotation: ") -> T:"
		arrowIdx := strings.Index(trimmed[close:], "->")
		if arrowIdx >= 0 {
			arrowStart := close + arrowIdx + len("->")
			retPart := strings.TrimSpace(trimmed[arrowStart:])
			if colon := strings.Index(retPart, ":"); colon >= 0 {
				retPart = strings.TrimSpace(retPart[:colon])
			}
			if retPart != "" {
				returnTypes = append(returnTypes, retPart)
			}
		}
	}

	return signature, paramTypes, returnTypes
}

// extractPythonCallees performs a lightweight scan inside a function body
// to collect simple call-site identifiers like "foo" in "foo(bar)".
func extractPythonCallees(code []byte, startOffset, endOffset int) []string {
	if startOffset < 0 || endOffset > len(code) || endOffset <= startOffset {
		return nil
	}
	body := string(code[startOffset:endOffset])
	callRe := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	matches := callRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var callees []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		name := m[1]
		if name == "if" || name == "for" || name == "while" || name == "return" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		callees = append(callees, name)
	}
	return callees
}
