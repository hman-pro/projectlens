---
name: explain-go-impact
description: Estimate what breaks if a symbol or interface is changed
---

## Steps

1. Use `find_symbol` to locate the target symbol
2. Use `get_symbol_context` to get all callers and interface implementors
3. Use `get_package_summary` for each affected package (up to 5)
4. Summarize:
   - Direct callers that would break
   - Interface implementors that would need updating
   - Packages that depend on this symbol's package
   - Confidence level (high/medium/low based on graph completeness)

## When to use
- "What breaks if I change this interface?"
- "What's the impact of modifying X?"
- "Who depends on this?"
