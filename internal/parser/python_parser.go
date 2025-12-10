package parser

import (
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

			functions = append(functions, FunctionNode{
				Name:      name,
				NodeType:  nodeType,
				StartLine: startLine,
				EndLine:   endLine,
				Content:   string(code[startOffset:endOffset]),
				StartByte: startOffset,
				EndByte:   endOffset,
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
