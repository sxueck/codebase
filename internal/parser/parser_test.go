package parser

import (
	"testing"
)

func TestGoParser(t *testing.T) {
	code := []byte(`package main

import "fmt"

// Simple function
func hello() {
	fmt.Println("Hello")
}

// A longer function
func calculateSum(a, b int) int {
	result := a + b
	fmt.Printf("Sum: %d\n", result)
	return result
}

type Calculator struct{}

// Method on Calculator
func (c *Calculator) Multiply(a, b int) int {
	product := a * b
	fmt.Printf("Product: %d\n", product)
	return product
}
`)

	parser := NewGoParser()
	functions, err := parser.ExtractFunctions("test.go", code)

	if err != nil {
		t.Fatalf("Failed to parse Go code: %v", err)
	}

	if len(functions) != 3 {
		t.Errorf("Expected 3 functions, got %d", len(functions))
	}

	// Check function names
	expectedNames := []string{"hello", "calculateSum", "(*Calculator).Multiply"}
	for i, fn := range functions {
		if i < len(expectedNames) {
			t.Logf("Function %d: Name=%s, Type=%s, Lines=%d-%d",
				i, fn.Name, fn.NodeType, fn.StartLine, fn.EndLine)
		}
	}
}

func TestPythonParser(t *testing.T) {
	code := []byte(`def greet(name):
    """A simple greeting function"""
    print(f"Hello, {name}")
    return f"Greeted {name}"

class Calculator:
    def add(self, a, b):
        """Add two numbers"""
        result = a + b
        print(f"Sum: {result}")
        return result

    def multiply(self, a, b):
        """Multiply two numbers"""
        product = a * b
        print(f"Product: {product}")
        return product
`)

	parser := NewPythonParser()
	functions, err := parser.ExtractFunctions("test.py", code)

	if err != nil {
		t.Fatalf("Failed to parse Python code: %v", err)
	}

	if len(functions) != 3 {
		t.Errorf("Expected 3 functions, got %d", len(functions))
	}

	for i, fn := range functions {
		t.Logf("Function %d: Name=%s, Type=%s, Lines=%d-%d",
			i, fn.Name, fn.NodeType, fn.StartLine, fn.EndLine)
	}
}

func TestJavaScriptParser(t *testing.T) {
	code := []byte(`function greet(name) {
    console.log(\`Hello, \${name}\`);
    return \`Greeted \${name}\`;
}

const add = (a, b) => {
    const result = a + b;
    console.log(\`Sum: \${result}\`);
    return result;
};

class Calculator {
    multiply(a, b) {
        const product = a * b;
        console.log(\`Product: \${product}\`);
        return product;
    }
}
`)

	parser := NewJavaScriptParser()
	functions, err := parser.ExtractFunctions("test.js", code)

	if err != nil {
		t.Fatalf("Failed to parse JavaScript code: %v", err)
	}

	if len(functions) < 2 {
		t.Errorf("Expected at least 2 functions, got %d", len(functions))
	}

	for i, fn := range functions {
		t.Logf("Function %d: Name=%s, Type=%s, Lines=%d-%d",
			i, fn.Name, fn.NodeType, fn.StartLine, fn.EndLine)
	}
}

func TestTypeScriptParser(t *testing.T) {
	code := []byte(`function greet(name: string): string {
    console.log(\`Hello, \${name}\`);
    return \`Greeted \${name}\`;
}

const add = (a: number, b: number): number => {
    const result = a + b;
    console.log(\`Sum: \${result}\`);
    return result;
};

class Calculator {
    multiply(a: number, b: number): number {
        const product = a * b;
        console.log(\`Product: \${product}\`);
        return product;
    }
}

interface ICalculator {
    add(a: number, b: number): number;
}
`)

	parser := NewTypeScriptParser()
	functions, err := parser.ExtractFunctions("test.ts", code)

	if err != nil {
		t.Fatalf("Failed to parse TypeScript code: %v", err)
	}

	if len(functions) < 2 {
		t.Errorf("Expected at least 2 functions, got %d", len(functions))
	}

	for i, fn := range functions {
		t.Logf("Function %d: Name=%s, Type=%s, Lines=%d-%d",
			i, fn.Name, fn.NodeType, fn.StartLine, fn.EndLine)
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		filePath string
		expected Language
	}{
		{"main.go", LanguageGo},
		{"script.py", LanguagePython},
		{"app.js", LanguageJavaScript},
		{"component.jsx", LanguageJavaScript},
		{"types.ts", LanguageTypeScript},
		{"component.tsx", LanguageTypeScript},
		{"unknown.txt", ""},
	}

	for _, tt := range tests {
		result := DetectLanguage(tt.filePath)
		if result != tt.expected {
			t.Errorf("DetectLanguage(%s) = %s, expected %s", tt.filePath, result, tt.expected)
		}
	}
}

func TestParserFactory(t *testing.T) {
	factory := NewParserFactory()

	// Test GetParser
	parser, err := factory.GetParser(LanguageGo)
	if err != nil {
		t.Errorf("Failed to get Go parser: %v", err)
	}
	if parser.Language() != string(LanguageGo) {
		t.Errorf("Expected Go parser, got %s", parser.Language())
	}

	// Test GetParserByFilePath
	parser, err = factory.GetParserByFilePath("test.py")
	if err != nil {
		t.Errorf("Failed to get parser by file path: %v", err)
	}
	if parser.Language() != string(LanguagePython) {
		t.Errorf("Expected Python parser, got %s", parser.Language())
	}

	// Test unsupported language
	_, err = factory.GetParser("unsupported")
	if err == nil {
		t.Error("Expected error for unsupported language")
	}
}
