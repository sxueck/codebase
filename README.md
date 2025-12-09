# Codebase Analysis Tool

A powerful CLI tool for semantic code search and duplicate detection using vector embeddings and LLM.

## Features

- **Semantic Code Search**: Natural language queries to find relevant code
- **Duplicate Detection**: Find logically similar code across your codebase
- **Multi-language Support**: Go, Python, TypeScript, JavaScript
- **MCP Integration**: Model Context Protocol server for LLM integration
- **Vector Database**: Uses Qdrant for efficient similarity search

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
export QDRANT_URL=localhost:6334
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
