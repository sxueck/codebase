# Codebase Analysis Tool

A powerful CLI tool for semantic code search and duplicate detection using vector embeddings and LLM.

## Features

- **Semantic Code Search**: Natural language queries to find relevant code
- **Duplicate Detection**: Find logically similar code across your codebase
- **Multi-language Support**: Go, Python, TypeScript, JavaScript
- **MCP Integration**: Model Context Protocol server for LLM integration
- **Vector Database**: Uses Qdrant for efficient similarity search

## Hybrid Retrieval Progress

- **Go AST Metadata**: `internal/parser/go_parser.go` now captures package names, imports, signatures, doc comments, and callees for every function/method. The indexer (`internal/indexer/indexer.go`) injects this metadata into both embeddings and Qdrant payloads so hybrid queries can combine semantic similarity with structured filters.

## Roadmap: AST-Aware Semantic Search

This project already uses Go's built-in AST packages to extract function and method definitions for indexing. We plan to extend this further to get closer to tools like `claude-context` that use AST-based splitting for richer semantic understanding.

Planned directions:

- **Richer AST-Derived Metadata**
  - For Go code (`internal/parser/go_parser.go`), extract and embed additional structural information:
    - Package name and imports (e.g. `go/ast`, `go/parser`, `go/token`, `go/types`).
    - Function signatures (parameter and return types).
    - Method receiver types.
    - Doc comments and key callees inside each function.
  - Include these fields in the text we send to the embedding model so that queries like "where do we construct Go ASTs" can match based on imports and API usage, not just raw code text.

- **AST-Based Code Chunking**
  - Move beyond "one function = one chunk" by using AST structure to define more semantic chunks:
    - Top-level declarations (functions, methods, types, etc.).
    - File-level summary chunks that describe the purpose of a file, its imports, and exported symbols.
    - Optional sub-chunking of very large functions by control-flow blocks.
  - This mirrors the `AstCodeSplitter` approach used in `claude-context`, improving recall for module-level queries.

- **Structure-Aware Query Planning and Filtering**
  - Extend the query planning step (LLM-powered `QueryPlan`) to:
    - Recognise structured signals in user queries (e.g. mentions of `go/ast`, `go/parser`, specific APIs).
    - Generate sub-queries or filters that target files/functions with matching imports, languages, or node types.
  - Store additional AST-derived fields (like imports or symbol names) in the vector payload, and use them in filter logic before ranking by semantic similarity.

These changes aim to combine AST structure with vector search so that the system can answer more precise questions about where particular APIs, patterns, or language features are used in a codebase.

## Installation

```bash
go build -o codebase main.go
```

## Prerequisites

- Go 1.22+
- Qdrant (running on localhost:6334 or configured via QDRANT_URL)
- OpenAI API Key

## Configuration

Set environment variables:

```bash
export OPENAI_API_KEY=your_key_here
export OPENAI_BASE_URL=https://api.openai.com/v1              # optional custom endpoint
export OPENAI_EMBEDDING_MODEL=text-embedding-3-large          # optional embedding model
export OPENAI_LLM_MODEL=gpt-4-turbo-preview                   # optional chat model
export QDRANT_URL=localhost:6334
export QDRANT_API_KEY=your_qdrant_password                    # optional auth secret
```

## Usage

### Index a codebase

```bash
codebase index --dir ./path/to/project
```

### Run as MCP server

```bash
codebase mcp
```

Configure in Claude Desktop:

```json
{
  "mcpServers": {
    "codebase-cli": {
      "command": "codebase",
      "args": ["mcp"]
    }
  }
}
```

### Query with natural language

```bash
codebase query --q "找到逻辑高度重复的代码"
```

## License

MIT
