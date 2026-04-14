---
name: trace-go-flow
description: Locate the implementation path for a behavior or symbol in the Go codebase
---

## Steps

1. Use `find_symbol` to locate the target symbol by name
   - If no exact match, use `search_go_context` with a natural language description
2. Use `get_symbol_context` on the found symbol to get callers, callees, and interface implementations
3. Use `get_package_summary` for the symbol's package to understand its role
4. Open only the top 1-2 files to verify the implementation
5. Summarize the implementation flow: entry point → key steps → exit point

## When to use
- "Where is X implemented?"
- "How does feature Y work?"
- "Trace the flow of Z"
