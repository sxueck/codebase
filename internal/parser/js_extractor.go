package parser

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type jsFunctionExtractor struct {
	code        []byte
	tsAware     bool
	pos         int
	lineOffsets []int
	functions   []FunctionNode
	imports     []string
}

func extractJSFunctions(code []byte, tsAware bool) []FunctionNode {
	extractor := &jsFunctionExtractor{
		code:        code,
		tsAware:     tsAware,
		lineOffsets: buildLineOffsets(code),
		imports:     extractJSImports(code),
	}
	extractor.scan()
	return extractor.functions
}

func (e *jsFunctionExtractor) scan() {
	for e.pos < len(e.code) {
		if e.skipWhitespaceCommentsOrStrings() {
			continue
		}
		if e.tryMatchClass() {
			continue
		}
		if e.tryMatchFunction() {
			continue
		}
		if e.tryMatchVariableFunction() {
			continue
		}
		e.pos++
	}
}

func (e *jsFunctionExtractor) tryMatchFunction() bool {
	start := e.matchKeyword("function")
	if start >= 0 {
		return e.captureNamedFunction(start)
	}

	asyncStart := e.matchKeyword("async")
	if asyncStart >= 0 {
		e.skipWhitespaceComments()
		innerStart := e.matchKeyword("function")
		if innerStart >= 0 {
			return e.captureNamedFunction(asyncStart)
		}
		e.pos = asyncStart + len("async")
	}

	return false
}

func (e *jsFunctionExtractor) tryMatchClass() bool {
	start := e.matchKeyword("class")
	if start < 0 {
		return false
	}

	className := ""
	e.skipWhitespaceComments()
	name, ok := e.readIdentifier()
	if ok {
		className = name
	}

	e.skipWhitespaceComments()
	// Skip extends clause if present
	lookahead := e.matchKeyword("extends")
	if lookahead >= 0 {
		e.skipBalancedExpression()
		e.skipWhitespaceComments()
	}

	if e.pos >= len(e.code) || e.code[e.pos] != '{' {
		return false
	}

	bodyStart := e.pos
	bodyEnd := e.scanBalanced(bodyStart, '{', '}')
	if bodyEnd < 0 {
		return false
	}
	e.extractClassMethods(className, bodyStart, bodyEnd)
	e.pos = bodyEnd
	return true
}

func (e *jsFunctionExtractor) tryMatchVariableFunction() bool {
	start := e.matchAnyKeyword("const", "let", "var")
	if start < 0 {
		return false
	}

	funcStart := start
	e.skipWhitespaceComments()

	name, ok := e.readIdentifier()
	if !ok {
		return false
	}

	e.skipWhitespaceComments()
	if e.pos >= len(e.code) || e.code[e.pos] != '=' {
		return false
	}
	e.pos++
	e.skipWhitespaceComments()

	// Allow async arrow functions
	if asyncStart := e.matchKeyword("async"); asyncStart >= 0 {
		e.skipWhitespaceComments()
		funcStart = start
		_ = asyncStart
	}

	if e.lookaheadKeyword("function") {
		e.matchKeyword("function")
		return e.captureFunctionExpression(funcStart, name)
	}

	return e.captureArrowFunction(funcStart, name)
}

func (e *jsFunctionExtractor) captureNamedFunction(start int) bool {
	funcStart := start
	e.skipWhitespaceComments()
	if e.pos < len(e.code) && e.code[e.pos] == '*' {
		e.pos++
		e.skipWhitespaceComments()
	}

	name, ok := e.readIdentifier()
	if !ok {
		return false
	}

	e.skipWhitespaceComments()
	if e.pos >= len(e.code) || e.code[e.pos] != '(' {
		return false
	}

	paramStart := e.pos
	paramsEnd := e.scanBalanced(e.pos, '(', ')')
	if paramsEnd < 0 {
		return false
	}
	e.pos = paramsEnd
	e.skipWhitespaceComments()
	returnType := e.skipOptionalTypeAnnotation()
	e.skipWhitespaceComments()

	if e.pos >= len(e.code) || e.code[e.pos] != '{' {
		return false
	}

	bodyEnd := e.scanBalanced(e.pos, '{', '}')
	if bodyEnd < 0 {
		return false
	}

	paramsText := string(e.code[paramStart:paramsEnd])
	e.appendFunction(name, "function", funcStart, bodyEnd, paramsText, returnType)
	e.pos = bodyEnd
	return true
}

func (e *jsFunctionExtractor) captureFunctionExpression(start int, name string) bool {
	e.skipWhitespaceComments()
	if e.pos < len(e.code) && isIdentifierStart(e.code[e.pos]) {
		// Skip optional inner name
		e.readIdentifier()
	}

	e.skipWhitespaceComments()
	if e.pos >= len(e.code) || e.code[e.pos] != '(' {
		return false
	}

	paramStart := e.pos
	paramsEnd := e.scanBalanced(e.pos, '(', ')')
	if paramsEnd < 0 {
		return false
	}
	e.pos = paramsEnd
	e.skipWhitespaceComments()
	returnType := e.skipOptionalTypeAnnotation()
	e.skipWhitespaceComments()

	if e.pos >= len(e.code) || e.code[e.pos] != '{' {
		return false
	}

	bodyEnd := e.scanBalanced(e.pos, '{', '}')
	if bodyEnd < 0 {
		return false
	}

	paramsText := string(e.code[paramStart:paramsEnd])
	e.appendFunction(name, "function", start, bodyEnd, paramsText, returnType)
	e.pos = bodyEnd
	return true
}

func (e *jsFunctionExtractor) captureArrowFunction(start int, name string) bool {
	paramStart := e.pos
	paramsText := ""
	if e.pos < len(e.code) && e.code[e.pos] == '(' {
		paramsEnd := e.scanBalanced(e.pos, '(', ')')
		if paramsEnd < 0 {
			return false
		}
		e.pos = paramsEnd
		paramsText = string(e.code[paramStart:paramsEnd])
	} else {
		_, ok := e.readIdentifier()
		if !ok {
			return false
		}
		paramsText = string(e.code[paramStart:e.pos])
	}

	e.skipWhitespaceComments()
	returnType := e.skipOptionalTypeAnnotation()
	e.skipWhitespaceComments()

	if e.pos+1 >= len(e.code) || e.code[e.pos] != '=' || e.code[e.pos+1] != '>' {
		e.pos = paramStart
		return false
	}
	e.pos += 2
	e.skipWhitespaceComments()

	if e.pos >= len(e.code) || e.code[e.pos] != '{' {
		return false
	}

	bodyEnd := e.scanBalanced(e.pos, '{', '}')
	if bodyEnd < 0 {
		return false
	}

	e.appendFunction(name, "function", start, bodyEnd, paramsText, returnType)
	e.pos = bodyEnd
	return true
}

func (e *jsFunctionExtractor) extractClassMethods(className string, bodyStart, bodyEnd int) {
	pos := bodyStart + 1
	for pos < bodyEnd {
		e.pos = pos
		if e.skipWhitespaceCommentsOrStrings() {
			pos = e.pos
			continue
		}

		start := e.pos
		e.skipDecorators()
		e.skipWhitespaceComments()
		for {
			if kw := e.matchAnyKeyword("public", "private", "protected", "static", "async", "get", "set", "readonly", "override", "abstract"); kw >= 0 {
				e.skipWhitespaceComments()
				continue
			}
			break
		}

		if e.pos >= len(e.code) {
			break
		}

		if e.code[e.pos] == '*' {
			e.pos++
			e.skipWhitespaceComments()
		}

		methodName := ""
		if e.pos < len(e.code) && e.code[e.pos] == '#' {
			e.pos++
		}

		if e.pos < len(e.code) && e.code[e.pos] == '[' {
			end := e.scanBalanced(e.pos, '[', ']')
			if end < 0 {
				break
			}
			e.pos = end
			e.skipWhitespaceComments()
		} else {
			name, ok := e.readIdentifier()
			if !ok {
				pos = start + 1
				continue
			}
			methodName = name
		}

		e.skipWhitespaceComments()
		if e.pos >= len(e.code) {
			break
		}

		if e.code[e.pos] != '(' {
			pos = start + 1
			continue
		}

		paramStart := e.pos
		paramsEnd := e.scanBalanced(e.pos, '(', ')')
		if paramsEnd < 0 {
			break
		}
		e.pos = paramsEnd
		e.skipWhitespaceComments()
		returnType := e.skipOptionalTypeAnnotation()
		e.skipWhitespaceComments()

		if e.pos >= len(e.code) || e.code[e.pos] != '{' {
			pos = start + 1
			continue
		}

		methodEnd := e.scanBalanced(e.pos, '{', '}')
		if methodEnd < 0 {
			break
		}

		funcName := methodName
		if className != "" && funcName != "" {
			funcName = className + "." + funcName
		}
		if funcName == "" {
			funcName = className
		}

		paramsText := string(e.code[paramStart:paramsEnd])
		e.appendFunction(funcName, "method", start, methodEnd, paramsText, returnType)
		pos = methodEnd
	}
}

func (e *jsFunctionExtractor) skipDecorators() {
	for {
		e.skipWhitespaceComments()
		if e.pos >= len(e.code) || e.code[e.pos] != '@' {
			return
		}
		e.pos++
		for e.pos < len(e.code) {
			ch := e.code[e.pos]
			if ch == '\n' || ch == '\r' {
				e.pos++
				break
			}
			if ch == '(' {
				end := e.scanBalanced(e.pos, '(', ')')
				if end < 0 {
					return
				}
				e.pos = end
			} else {
				e.pos++
			}
		}
	}
}

func (e *jsFunctionExtractor) skipWhitespaceComments() {
	for e.pos < len(e.code) {
		switch e.code[e.pos] {
		case ' ', '\t', '\r', '\n':
			e.pos++
		case '/':
			if e.pos+1 < len(e.code) {
				next := e.code[e.pos+1]
				if next == '/' {
					e.pos += 2
					for e.pos < len(e.code) && e.code[e.pos] != '\n' {
						e.pos++
					}
				} else if next == '*' {
					e.pos += 2
					for e.pos+1 < len(e.code) && !(e.code[e.pos] == '*' && e.code[e.pos+1] == '/') {
						e.pos++
					}
					if e.pos+1 < len(e.code) {
						e.pos += 2
					}
				} else {
					return
				}
			} else {
				return
			}
		default:
			return
		}
	}
}

func (e *jsFunctionExtractor) skipWhitespaceCommentsOrStrings() bool {
	if e.pos >= len(e.code) {
		return false
	}

	switch e.code[e.pos] {
	case ' ', '\t', '\r', '\n':
		e.skipWhitespaceComments()
		return true
	case '/':
		before := e.pos
		e.skipWhitespaceComments()
		return before != e.pos
	case '"', '\'':
		e.skipStringLiteral(e.code[e.pos])
		return true
	case '`':
		e.skipTemplateLiteral()
		return true
	default:
		return false
	}
}

func (e *jsFunctionExtractor) skipStringLiteral(quote byte) {
	if e.pos >= len(e.code) {
		return
	}
	e.pos++
	for e.pos < len(e.code) {
		ch := e.code[e.pos]
		if ch == '\\' {
			e.pos += 2
			continue
		}
		e.pos++
		if ch == quote {
			break
		}
	}
}

func (e *jsFunctionExtractor) skipTemplateLiteral() {
	if e.pos >= len(e.code) || e.code[e.pos] != '`' {
		return
	}
	e.pos++
	for e.pos < len(e.code) {
		ch := e.code[e.pos]
		if ch == '\\' {
			e.pos += 2
			continue
		}
		if ch == '`' {
			e.pos++
			break
		}
		if ch == '$' && e.pos+1 < len(e.code) && e.code[e.pos+1] == '{' {
			blockEnd := e.scanBalanced(e.pos+1, '{', '}')
			if blockEnd < 0 {
				e.pos = len(e.code)
				return
			}
			e.pos = blockEnd
			continue
		}
		e.pos++
	}
}

func (e *jsFunctionExtractor) skipBalancedExpression() {
	depth := 0
	for e.pos < len(e.code) {
		ch := e.code[e.pos]
		switch ch {
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			if depth == 0 {
				return
			}
			depth--
		case '"', '\'':
			e.skipStringLiteral(ch)
			continue
		case '`':
			e.skipTemplateLiteral()
			continue
		}
		e.pos++
		if depth == 0 && (ch == '{' || ch == '(' || ch == '[') {
			break
		}
	}
}

func (e *jsFunctionExtractor) scanBalanced(start int, open, close byte) int {
	if start >= len(e.code) || e.code[start] != open {
		return -1
	}
	pos := start
	depth := 0
	for pos < len(e.code) {
		ch := e.code[pos]
		if ch == open {
			depth++
			pos++
			continue
		}
		if ch == close {
			depth--
			pos++
			if depth == 0 {
				return pos
			}
			continue
		}
		switch ch {
		case '"', '\'':
			pos = skipStringLiteralFrom(e.code, pos)
		case '`':
			pos = skipTemplateLiteralFrom(e.code, pos)
		case '/':
			next := skipCommentFrom(e.code, pos)
			if next == pos {
				pos++
			} else {
				pos = next
			}
		default:
			pos++
		}
	}
	return -1
}

func (e *jsFunctionExtractor) skipOptionalTypeAnnotation() string {
	if !e.tsAware {
		return ""
	}
	if e.pos >= len(e.code) || e.code[e.pos] != ':' {
		return ""
	}

	e.pos++
	start := e.pos
	depth := 0
	firstToken := true
	for e.pos < len(e.code) {
		ch := e.code[e.pos]
		switch ch {
		case ' ', '\t', '\r':
			e.pos++
			continue
		}
		if ch == '\n' && depth == 0 {
			return strings.TrimSpace(string(e.code[start:e.pos]))
		}
		switch ch {
		case '{':
			if depth == 0 && !firstToken {
				return strings.TrimSpace(string(e.code[start:e.pos]))
			}
			firstToken = false
			depth++
		case '}':
			if depth > 0 {
				depth--
			} else {
				return strings.TrimSpace(string(e.code[start:e.pos]))
			}
		case '<':
			firstToken = false
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		case '(':
			firstToken = false
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '[':
			firstToken = false
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '"', '\'':
			firstToken = false
			e.skipStringLiteral(ch)
			continue
		case '`':
			firstToken = false
			e.skipTemplateLiteral()
			continue
		case '\n', ';':
			if depth == 0 {
				return strings.TrimSpace(string(e.code[start:e.pos]))
			}
		case '=':
			if depth == 0 {
				return strings.TrimSpace(string(e.code[start:e.pos]))
			}
		default:
			if firstToken && ch != '\n' {
				firstToken = false
			}
		}
		e.pos++
	}
	return strings.TrimSpace(string(e.code[start:e.pos]))
}

func (e *jsFunctionExtractor) matchKeyword(word string) int {
	if e.pos < 0 || e.pos+len(word) > len(e.code) {
		return -1
	}
	if !bytes.HasPrefix(e.code[e.pos:], []byte(word)) {
		return -1
	}
	if !e.keywordBoundaryBefore(e.pos) {
		return -1
	}
	after := e.pos + len(word)
	if after < len(e.code) && isIdentifierPart(e.code[after]) {
		return -1
	}
	start := e.pos
	e.pos = after
	return start
}

func (e *jsFunctionExtractor) matchAnyKeyword(words ...string) int {
	for _, word := range words {
		saved := e.pos
		if pos := e.matchKeyword(word); pos >= 0 {
			return pos
		}
		e.pos = saved
	}
	return -1
}

func (e *jsFunctionExtractor) keywordBoundaryBefore(pos int) bool {
	if pos == 0 {
		return true
	}
	return !isIdentifierPart(e.code[pos-1])
}

func (e *jsFunctionExtractor) lookaheadKeyword(word string) bool {
	saved := e.pos
	defer func() { e.pos = saved }()
	return e.matchKeyword(word) >= 0
}

func (e *jsFunctionExtractor) readIdentifier() (string, bool) {
	if e.pos >= len(e.code) {
		return "", false
	}
	if !isIdentifierStart(e.code[e.pos]) {
		return "", false
	}
	start := e.pos
	e.pos++
	for e.pos < len(e.code) && isIdentifierPart(e.code[e.pos]) {
		e.pos++
	}
	return string(e.code[start:e.pos]), true
}

func (e *jsFunctionExtractor) appendFunction(name, nodeType string, start, end int, paramsText, returnAnnotation string) {
	if end <= start {
		return
	}
	startLine := e.lineForOffset(start)
	endLine := e.lineForOffset(end - 1)
	if endLine-startLine < 2 {
		return
	}
	content := string(e.code[start:end])
	// Enrich JS/TS function nodes with import list, a lightweight
	// signature and detected callees so they carry similar metadata
	// as Go functions into the vector index.
	imports := append([]string(nil), e.imports...)
	signature := deriveJSSignature(content, name)
	callees := extractJSCallees(content)
	doc := e.extractDocComment(start)
	paramTypes := parseJSParamTypes(paramsText, e.tsAware)
	returnTypes := parseJSReturnTypes(returnAnnotation)

	e.functions = append(e.functions, FunctionNode{
		Name:        name,
		NodeType:    nodeType,
		StartLine:   startLine,
		EndLine:     endLine,
		Content:     content,
		StartByte:   start,
		EndByte:     end,
		PackageName: "", // Not modeled for JS/TS
		Imports:     imports,
		Signature:   signature,
		Receiver:    "", // Methods are encoded in Name as Class.method
		Doc:         doc,
		Callees:     callees,
		ParamTypes:  paramTypes,
		ReturnTypes: returnTypes,
	})
}

func parseJSReturnTypes(annotation string) []string {
	annotation = strings.TrimSpace(annotation)
	if annotation == "" {
		return nil
	}
	return []string{annotation}
}

func parseJSParamTypes(paramsText string, tsAware bool) []string {
	paramsText = strings.TrimSpace(paramsText)
	if paramsText == "" {
		return nil
	}
	if strings.HasPrefix(paramsText, "(") && strings.HasSuffix(paramsText, ")") {
		paramsText = paramsText[1 : len(paramsText)-1]
	}
	parts := splitJSParameters(paramsText)
	if len(parts) == 0 {
		return nil
	}
	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(stripJSDefaultValue(part))
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "...") {
			part = strings.TrimSpace(part[3:])
		}
		part = stripJSParamModifiers(part)
		descriptor := ""
		if tsAware {
			if idx := findTopLevelColon(part); idx >= 0 {
				descriptor = strings.TrimSpace(part[idx+1:])
			}
		}
		if descriptor == "" {
			descriptor = sanitizeJSParamName(part)
		}
		result = append(result, descriptor)
	}
	return result
}

func splitJSParameters(params string) []string {
	var parts []string
	var current strings.Builder
	depth := 0
	inString := false
	inLineComment := false
	inBlockComment := false
	var quote byte

	flushPart := func() {
		part := strings.TrimSpace(current.String())
		if part != "" {
			parts = append(parts, part)
		} else {
			parts = append(parts, "")
		}
		current.Reset()
	}

	for i := 0; i < len(params); i++ {
		ch := params[i]

		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				current.WriteByte(ch)
			} else if ch == '\r' {
				inLineComment = false
				if i+1 >= len(params) || params[i+1] != '\n' {
					current.WriteByte('\n')
				}
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(params) && params[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}

		if inString {
			current.WriteByte(ch)
			if ch == '\\' {
				if i+1 < len(params) {
					current.WriteByte(params[i+1])
					i++
				}
				continue
			}
			if ch == quote {
				inString = false
			}
			continue
		}

		if ch == '/' && i+1 < len(params) {
			next := params[i+1]
			if next == '/' {
				inLineComment = true
				i++
				continue
			}
			if next == '*' {
				inBlockComment = true
				i++
				continue
			}
		}

		switch ch {
		case '\'', '"', '`':
			inString = true
			quote = ch
			current.WriteByte(ch)
		case '(', '[', '{', '<':
			depth++
			current.WriteByte(ch)
		case ')', ']', '}', '>':
			if depth > 0 {
				depth--
			}
			current.WriteByte(ch)
		case ',':
			if depth == 0 {
				flushPart()
				continue
			}
			current.WriteByte(ch)
		default:
			current.WriteByte(ch)
		}
	}

	if current.Len() > 0 {
		last := strings.TrimSpace(current.String())
		if last != "" {
			parts = append(parts, last)
		}
	}
	return parts
}

func stripJSDefaultValue(param string) string {
	param = strings.TrimSpace(param)
	if param == "" {
		return ""
	}
	depth := 0
	inString := false
	var quote byte
	for i := 0; i < len(param); i++ {
		ch := param[i]
		if inString {
			if ch == '\\' {
				i++
				continue
			}
			if ch == quote {
				inString = false
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			inString = true
			quote = ch
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			if depth > 0 {
				depth--
			}
		case '=':
			if depth == 0 {
				return strings.TrimSpace(param[:i])
			}
		}
	}
	return param
}

func stripJSParamModifiers(param string) string {
	param = strings.TrimSpace(param)
	if param == "" {
		return ""
	}
	modifiers := []string{"public", "private", "protected", "readonly", "override", "abstract", "declare", "static"}
	for {
		trimmed := strings.TrimSpace(param)
		removed := false
		for _, mod := range modifiers {
			if strings.HasPrefix(trimmed, mod+" ") {
				param = strings.TrimSpace(trimmed[len(mod):])
				removed = true
				break
			}
		}
		if !removed {
			return trimmed
		}
	}
}

func sanitizeJSParamName(param string) string {
	param = strings.TrimSpace(param)
	if param == "" {
		return ""
	}
	if idx := findTopLevelColon(param); idx >= 0 {
		param = strings.TrimSpace(param[:idx])
	}
	param = strings.TrimSpace(strings.TrimSuffix(param, "?"))
	return param
}

func findTopLevelColon(param string) int {
	depth := 0
	inString := false
	var quote byte
	for i := 0; i < len(param); i++ {
		ch := param[i]
		if inString {
			if ch == '\\' {
				i++
				continue
			}
			if ch == quote {
				inString = false
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			inString = true
			quote = ch
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			if depth > 0 {
				depth--
			}
		case ':':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func (e *jsFunctionExtractor) extractDocComment(start int) string {
	line := e.lineForOffset(start)
	if line <= 1 {
		return ""
	}
	prevLine := line - 1
	trimmed := strings.TrimSpace(e.lineText(prevLine))
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "//") {
		return e.collectLineCommentDoc(prevLine)
	}
	if strings.HasSuffix(trimmed, "*/") {
		return e.collectBlockCommentDoc(prevLine)
	}
	return ""
}

func (e *jsFunctionExtractor) collectLineCommentDoc(line int) string {
	var lines []string
	for line >= 1 {
		text := strings.TrimSpace(e.lineText(line))
		if !strings.HasPrefix(text, "//") {
			break
		}
		comment := strings.TrimSpace(strings.TrimPrefix(text, "//"))
		lines = append([]string{comment}, lines...)
		line--
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (e *jsFunctionExtractor) collectBlockCommentDoc(line int) string {
	var lines []string
	foundStart := false
	for line >= 1 {
		text := e.lineText(line)
		lines = append([]string{text}, lines...)
		if strings.Contains(text, "/*") {
			foundStart = true
			break
		}
		line--
	}
	if !foundStart {
		return ""
	}
	raw := strings.Join(lines, "\n")
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "/**") {
		return ""
	}
	return cleanJSDocComment(trimmed)
}

func cleanJSDocComment(comment string) string {
	comment = strings.TrimSpace(comment)
	comment = strings.TrimPrefix(comment, "/**")
	comment = strings.TrimPrefix(comment, "/*")
	comment = strings.TrimSuffix(comment, "*/")
	lines := strings.Split(comment, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimSpace(line)
		lines[i] = line
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (e *jsFunctionExtractor) lineText(line int) string {
	if line <= 0 || line >= len(e.lineOffsets) {
		return ""
	}
	start := e.lineOffsets[line-1]
	end := e.lineOffsets[line] - 1
	if end > len(e.code) {
		end = len(e.code)
	}
	if end < start {
		end = start
	}
	return string(e.code[start:end])
}

func (e *jsFunctionExtractor) lineForOffset(offset int) int {
	if offset < 0 {
		return 1
	}
	idx := sort.Search(len(e.lineOffsets), func(i int) bool {
		return e.lineOffsets[i] > offset
	})
	if idx == 0 {
		return 1
	}
	return idx
}

func buildLineOffsets(code []byte) []int {
	offsets := []int{0}
	for i, b := range code {
		if b == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	offsets = append(offsets, len(code)+1)
	return offsets
}

// extractJSImports performs a lightweight scan of the whole file to gather
// import/module references usable as metadata for each function.
func extractJSImports(code []byte) []string {
	var imports []string
	lines := strings.Split(string(code), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		if strings.HasPrefix(trimmed, "import ") {
			// ES module imports: import x from 'mod'; import {a} from 'mod';
			// We keep the module specifier in quotes.
			if idx := strings.LastIndexAny(trimmed, "'\""); idx >= 0 {
				q := trimmed[idx]
				start := strings.LastIndex(trimmed[:idx], string(q))
				if start >= 0 && start < idx {
					mod := trimmed[start+1 : idx]
					if mod != "" {
						imports = append(imports, mod)
					}
				}
			}
			continue
		}
		if strings.HasPrefix(trimmed, "const ") || strings.HasPrefix(trimmed, "let ") || strings.HasPrefix(trimmed, "var ") {
			// CommonJS require: const x = require('mod')
			if strings.Contains(trimmed, "require(") {
				re := regexp.MustCompile(`require\(["']([^"']+)["']\)`)
				if m := re.FindStringSubmatch(trimmed); len(m) == 2 {
					imports = append(imports, m[1])
				}
			}
		}
	}
	if len(imports) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(imports))
	var dedup []string
	for _, imp := range imports {
		if _, ok := seen[imp]; ok {
			continue
		}
		seen[imp] = struct{}{}
		dedup = append(dedup, imp)
	}
	sort.Strings(dedup)
	return dedup
}

// deriveJSSignature tries to build a minimal, readable signature of a
// function from its source snippet and name.
func deriveJSSignature(content, name string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		if strings.Contains(trimmed, "function ") || strings.Contains(trimmed, "=>") {
			// Use the line up to the opening brace or first "=>" as the signature
			if idx := strings.Index(trimmed, "{"); idx >= 0 {
				trimmed = strings.TrimSpace(trimmed[:idx])
			}
			if idx := strings.Index(trimmed, "=>"); idx >= 0 {
				trimmed = strings.TrimSpace(trimmed[:idx+2])
			}
			return trimmed
		}
	}
	if name == "" {
		return ""
	}
	return name + "()"
}

// extractJSCallees performs a simple scan to find identifiers followed by
// "(" which likely correspond to function calls.
func extractJSCallees(content string) []string {
	re := regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	matches := re.FindAllStringSubmatch(content, -1)
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
		if name == "if" || name == "for" || name == "while" || name == "switch" || name == "return" {
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

func isIdentifierStart(ch byte) bool {
	return ch == '_' || ch == '$' || unicode.IsLetter(rune(ch))
}

func isIdentifierPart(ch byte) bool {
	return ch == '_' || ch == '$' || unicode.IsLetter(rune(ch)) || unicode.IsDigit(rune(ch))
}

func skipStringLiteralFrom(code []byte, pos int) int {
	quote := code[pos]
	pos++
	for pos < len(code) {
		ch := code[pos]
		if ch == '\\' {
			pos += 2
			continue
		}
		pos++
		if ch == quote {
			break
		}
	}
	return pos
}

func skipTemplateLiteralFrom(code []byte, pos int) int {
	pos++
	for pos < len(code) {
		ch := code[pos]
		if ch == '\\' {
			pos += 2
			continue
		}
		if ch == '`' {
			pos++
			break
		}
		if ch == '$' && pos+1 < len(code) && code[pos+1] == '{' {
			blockEnd := skipBalancedFrom(code, pos+1, '{', '}')
			if blockEnd < 0 {
				return len(code)
			}
			pos = blockEnd
			continue
		}
		pos++
	}
	return pos
}

func skipCommentFrom(code []byte, pos int) int {
	if pos+1 >= len(code) {
		return pos
	}
	if code[pos+1] == '/' {
		pos += 2
		for pos < len(code) && code[pos] != '\n' {
			pos++
		}
		return pos
	}
	if code[pos+1] == '*' {
		pos += 2
		for pos+1 < len(code) && !(code[pos] == '*' && code[pos+1] == '/') {
			pos++
		}
		if pos+1 < len(code) {
			pos += 2
		}
		return pos
	}
	return pos
}

func skipBalancedFrom(code []byte, start int, open, close byte) int {
	if start >= len(code) || code[start] != open {
		return -1
	}
	pos := start
	depth := 0
	for pos < len(code) {
		ch := code[pos]
		if ch == open {
			depth++
			pos++
			continue
		}
		if ch == close {
			depth--
			pos++
			if depth == 0 {
				return pos
			}
			continue
		}
		switch ch {
		case '"', '\'':
			pos = skipStringLiteralFrom(code, pos)
		case '`':
			pos = skipTemplateLiteralFrom(code, pos)
		case '/':
			next := skipCommentFrom(code, pos)
			if next == pos {
				pos++
			} else {
				pos = next
			}
		default:
			pos++
		}
	}
	return -1
}
