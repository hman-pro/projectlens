---
name: debug-go-test
description: Investigate failing or relevant tests and map them to production code
---

## Steps

1. Use `search_go_context` with the test name or behavior description
2. Use `get_symbol_context` on the production symbol under test to understand dependencies
3. Use `get_package_summary` for the test's package
4. Open the test file and the production code file
5. Explain: what the test expects, what the production code does, where they diverge

## When to use
- "Why is this test failing?"
- "What tests cover this behavior?"
- "What does this test expect?"
