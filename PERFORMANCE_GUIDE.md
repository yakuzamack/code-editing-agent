# Code Editing Agent — Performance & Efficiency Guide

## ⚡ Quick Start: Avoid Context Bloat

### ❌ DON'T Do This (Slow — 50+ seconds)
```
1. read_file for file 1
2. read_file for file 2
3. read_file for file 3
... (sequential, each adds context)
```
**Result**: Token count explodes → requests slow down → hitting rate limits

### ✅ DO This Instead (Fast — 5-10 seconds)

#### Option A: Use `fast_framework_scan` First
```bash
# Quickly understand structure WITHOUT reading large files
fast_framework_scan --include_todos=true
```
Then read only the specific files you need.

#### Option B: Use `batch_read` for Multiple Files
```bash
# Read 5-10 files in parallel instead of sequentially
batch_read --files=[
  "internal/implant/core/implant.go",
  "internal/implant/modules/crypto/extraction/extraction.go",
  "pkg/shared/protocol/protocol.go"
]
```
**Benefit**: Parallel reads, single context injection, clean formatting

#### Option C: Use Subagents for Exploration
```bash
# Offload complex analysis to subagent
runSubagent("Explore", "Analyze crypto-framework module structure and find stubs")
```
**Benefit**: Subagent runs independently, you get condensed summary, context stays manageable

---

## 🎯 Best Practices by Task

### Task: Understand Framework Architecture
**Bad Approach:**
1. Read README
2. Read main.go
3. Read cmd/server/main.go
4. Read cmd/cli/main.go
5. List all modules...

**Good Approach:**
```bash
# 1. Fast scan
fast_framework_scan --include_todos

# 2. Get overview
framework_status

# 3. Read only critical entry points
batch_read --files=["cmd/server/main.go", "cmd/cli/main.go"]
```
**Time**: 5 seconds vs. 30+ seconds

---

### Task: Identify Missing Implementation
**Bad Approach:**
```bash
read_file for each module...
read_file for each submodule...
```

**Good Approach:**
```bash
# 1. Quick structural scan
fast_framework_scan --include_todos

# 2. Use framework_status (designed for this!)
framework_status --regenerate

# 3. Search knowledge base for patterns
search_knowledge "stub implementations missing"
```

---

### Task: Add New Feature/Module
**Bad Approach:**
```bash
Read all existing modules to understand patterns...
Read multiple examples...
```

**Good Approach:**
```bash
# 1. Examine one clean example module
batch_read --files=[
  "internal/implant/modules/system/system.go",
  "internal/implant/modules/system/system_test.go"
]

# 2. Use scaffold_module
scaffold_module --name="my_module" --description="..."

# 3. Reference knowledge base
search_knowledge "module interface implementation pattern"
```

---

## 📊 Token & Rate Limit Prevention

### Monitor Your Request Size
- **Good**: 10,000-15,000 tokens per request
- **Warning**: 20,000-25,000 tokens (getting expensive)
- **Critical**: 30,000+ tokens (hitting limits soon!)

### Token-Saving Strategies
1. **Use search instead of read**
   ```bash
   # Instead of: read_file("pkg/agent/agent.go")
   # Use:
   search_code "func.*NewAgent"
   ```

2. **Batch related operations**
   ```bash
   # One call for multiple files
   batch_read --files=[f1, f2, f3]
   # Instead of three separate calls
   ```

3. **Leverage tools designed for analysis**
   - `framework_status` → module overview
   - `list_framework_components` → architecture
   - `project_tree` → file structure
   - `search_code` → find specific code
   - `search_knowledge` → find patterns/docs

4. **Use subagents for deep exploration**
   ```bash
   # Instead of running 10 read_file calls in main agent:
   # Offload to subagent for a condensed response
   runSubagent("Explore", "Find all crypto extraction patterns")
   ```

---

## 🔍 Tool Selection Reference

| Goal | Best Tool(s) | Why |
|------|-------------|-----|
| Understand structure | `fast_framework_scan` | No file reads, instant overview |
| Get module status | `framework_status` | Analyzes ALL modules efficiently |
| Find specific code | `search_code` | Targeted search, small response |
| Read many files | `batch_read` | Parallel reads, single context hit |
| Analyze patterns | `search_knowledge` + `semantic_search` | Knowledge base faster than reading |
| Check architecture | `list_framework_components` or `project_tree` | Directory listing only |
| Deep analysis | `runSubagent("Explore", "...")` | Offload context burden |

---

## ⚠️ Anti-Patterns (Performance Killers)

### 1. Sequential File Reads Loop
```go
❌ for each file in directory:
     read_file(file)
     // context grows 50KB per iteration
     // request gets slower each time
```

**Fix**: Use `batch_read` or `search_code` instead

### 2. Reading Entire Large Files
```go
❌ read_file("internal/implant/core/implant.go")  // 5000+ lines
```

**Fix**: Use `search_code` to find specific functions, or `smart_read_file` for summaries

### 3. Building Context by Hand
```go
❌ // Agent reads 20 files to understand crypto extraction
   // Context bloats to 40,000+ tokens
```

**Fix**: Use `framework_status` or subagent exploration instead

### 4. No Summarization Between Phases
```go
❌ Phase 1: Explore architecture (15KB context added)
   Phase 2: Analyze modules (15KB more added)
   Phase 3: Fix bugs (15KB more added)
   // Total: 45KB context, very slow by phase 3
```

**Fix**: Ask agent to "summarize findings" between major phases, clearing non-critical context

---

## 🚀 Performance Checklist

Before asking the agent a complex question:

- [ ] Start with `fast_framework_scan` to understand scope
- [ ] Use `search_code`/`search_knowledge` before `read_file`
- [ ] Prefer `batch_read` over sequential `read_file` calls
- [ ] Use existing tools (`framework_status`, `list_framework_components`) instead of reading files
- [ ] Use subagents (`Explore`) for analysis-heavy tasks
- [ ] Ask agent to summarize between major task phases
- [ ] Monitor token count in responses

---

## Example: Good vs Bad Analysis Session

### ❌ Bad Session (50+ seconds, hits limits)
```
User: Analyze crypto-framework
Agent:
  1. read_file(README.md)              → +2000 tokens
  2. read_file(cmd/server/main.go)    → +3000 tokens
  3. read_file(cmd/cli/main.go)       → +2500 tokens
  4. list_dir(internal/implant)       → +500 tokens
  5. read_file(internal/implant/...)  → +4000 tokens
  6. read_file(another module.go)     → +3000 tokens
  7. read_file(yet another.go)        → +3500 tokens
  ...
Final: 40,000+ tokens, request #4 takes 50+ seconds ⚠️
```

### ✅ Good Session (10-15 seconds, efficient)
```
User: Analyze crypto-framework
Agent:
  1. fast_framework_scan --include_todos  → +1500 tokens, instant scan
  2. framework_status                     → +2000 tokens, comprehensive overview
  3. search_code "type.*Module interface" → +800 tokens, find patterns
  
  (Summarizes findings...)
  
  Follow-up: Deep dive into specific module
  Agent:
    1. batch_read --files=[module1, module2] → +3000 tokens, parallel
    2. search_knowledge "similar modules"     → +1200 tokens
    
Final: ~8,500 tokens total, all requests <10 seconds ✅
```

---

## Summary

**Key Wins**:
- ✅ `fast_framework_scan` for instant structure overview (no large file reads)
- ✅ `batch_read` for multiple files (parallel instead of sequential)
- ✅ `search_code`/`search_knowledge` before `read_file` (targeted vs. full)
- ✅ Subagents for exploration (offload context burden)
- ✅ Pre-existing analysis tools (designed for efficiency)

**Result**: 5-10x faster analysis, stay within rate limits, better response quality.
