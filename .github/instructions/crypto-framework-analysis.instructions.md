---
name: crypto-framework-efficient-analysis
description: Optimize agent performance when analyzing and managing the crypto-framework
applyTo:
  - "**/*crypto-framework*"
  - "**/*crypto_*"
---

# Efficient Crypto-Framework Analysis Instructions

## Core Performance Rule

**ALWAYS prioritize speed over comprehensiveness.**

When analyzing the crypto-framework:
1. Start with `fast_framework_scan` (instant, no large file reads)
2. Use existing analysis tools (`framework_status`, `list_framework_components`)
3. Search code/knowledge base before reading files
4. Use `batch_read` for multiple files (parallel, not sequential)
5. Use subagents (`Explore`) for complex exploration

**Never** chain 5+ sequential `read_file` calls. That kills performance.

---

## Tool Selection Guide

### For Understanding Structure
- ❌ Don't: Read cmd/, internal/, pkg/ individually
- ✅ Do: `fast_framework_scan` → `list_framework_components`

### For Finding Code/Patterns
- ❌ Don't: Read entire source files
- ✅ Do: `search_code "pattern"` → read only matches

### For Module Analysis
- ❌ Don't: Read all module directories
- ✅ Do: `framework_status` → analyze summary

### For Multiple File Context
- ❌ Don't: Three separate `read_file` calls
- ✅ Do: `batch_read --files=[f1, f2, f3]`

### For Complex Discovery
- ❌ Don't: Run 10 tool calls in main agent
- ✅ Do: `runSubagent("Explore", "Find all X patterns")`

---

## Anti-Patterns (Performance Killers)

### ❌ Sequential Reads Loop
```
for each module in modules/:
  read_file(module)  ← Context grows 50KB per iteration
  read_file(module_test)  ← Slower each time
```
**Fix**: Use `fast_framework_scan`, `search_code`, or `batch_read`

### ❌ Reading Without Searching First
```
User: "Where is crypto extraction?"
Agent: read_file(crypto.go) + read_file(extraction.go) + ...
```
**Fix**: `search_code "crypto.*extract"` first

### ❌ No Subagent Offloading
```
Agent conducts 15+ file reads directly to understand module patterns
Context bloats to 35,000+ tokens
```
**Fix**: `runSubagent("Explore", "Analyze module patterns and report gaps")`

---

## Recommended Workflow

### Phase 1: Discovery (1-2 minutes)
```bash
# Get instant overview
fast_framework_scan --include_todos

# Get comprehensive module status
framework_status --regenerate
```
→ Result: Clear understanding of framework layout and outstanding work

### Phase 2: Targeted Analysis (3-5 minutes)
Based on Phase 1 findings:
```bash
# Search for specific code
search_code "type ModuleInterface"

# Read specific files (use batch if multiple)
batch_read --files=["internal/implant/core/implant.go", "..."]

# Or use knowledge base
search_knowledge "module interface implementation"
```
→ Result: Deep understanding of specific areas

### Phase 3: Implementation
```bash
# Make changes with focused context
edit_file(...)
run_command(...)
```
→ Result: Quick, reliable implementation

---

## Context Budget

| Phase | Token Budget | Tools | Outcome |
|-------|-------------|-------|---------|
| Discovery | 5,000-8,000 | `fast_framework_scan`, `framework_status` | Full layout overview |
| Analysis | 10,000-15,000 | `search_code`, `batch_read`, `search_knowledge` | Targeted deep dive |
| Implementation | 8,000-12,000 | `edit_file`, `run_command` | Make changes |
| **Total** | **23,000-35,000** | *Avoid exceeding this* | Efficient workflow |

**Previous problematic session**: 40,000+ tokens by request #4 ⚠️
**Recommended session**: Stay under 35,000 total ✅

---

## Token-Saving Tips

1. **Use specialized tools, not manual reads**
   - `framework_status` analyzes modules better than reading 20 files
   - `search_code` is more efficient than reading and searching manually

2. **Batch operations**
   - One `batch_read` call (3 files) vs. three `read_file` calls
   - Saves context, faster execution

3. **Leverage knowledge base**
   - `search_knowledge` for patterns, documentation, examples
   - Often faster than reading source files

4. **Offload with subagents**
   - Complex analysis → `runSubagent("Explore", "...")`
   - You get summary, not full context dump

5. **Summarize between major phases**
   - After discovery: "Summarize the top 3 outstanding issues"
   - After analysis: "Give me the top 5 implementation priorities"
   - Prunes non-critical context

---

## When Token Count Gets High

If you notice requests taking 20+ seconds or token counts hitting 25,000+:

1. **Ask for a summary** → "Summarize your findings so far"
2. **Narrow scope** → "Focus only on crypto/extraction module"
3. **Use subagents** → Offload remaining analysis to `Explore` subagent
4. **Take a break** → Finish this task, start fresh conversation

---

## Example: Good Analysis Request

```
User: "What's the status of crypto module implementation?"

Agent:
  1. fast_framework_scan --target_dir=internal/implant/modules/crypto
     Result: Directory structure, file counts, estimated LOC
  
  2. framework_status --regenerate
     Result: Module-by-module implementation status
  
  3. search_code "TODO.*crypto" | search_code "FIXME.*extraction"
     Result: Specific outstanding work items
  
  Total: ~8,000 tokens, <10 seconds, clear answer ✅
```

---

## Checklist Before Complex Analysis

- [ ] Start with `fast_framework_scan` to understand scope
- [ ] Use analysis tools (`framework_status`) before reading
- [ ] Search code/knowledge before `read_file`
- [ ] Use `batch_read` for 2+ files
- [ ] Reserve `read_file` for specific, necessary reads only
- [ ] Use subagents for exploration-heavy tasks
- [ ] Monitor token count (stay under 35,000 total)
- [ ] Summarize between major phases
