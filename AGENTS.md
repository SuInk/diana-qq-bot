# Agent Instructions

## Semantic Routing

- Do not implement message triggering, reply selection, relevance detection, intent recognition, or contextual reference resolution with keyword lists, regular expressions, substring matching, or other hard-coded lexical rules.
- Use model-based semantic judgment for these decisions, including passive reply routing and deciding whether a message needs a response.
- Performance optimizations must preserve semantic judgment. Use batching, debouncing, context caching, or a dedicated faster routing model instead of keyword-based shortcuts.
- Existing user-configured trigger phrases may only be treated as explicit product configuration. Do not expand them into implicit keyword heuristics.
