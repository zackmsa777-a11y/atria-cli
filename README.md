<div align="center">

```text
          .::::::::.          
        .::::::::::::.        
       .:::::    ':::::.      
      .:::::::.    '::::.     
     .::::::'       '::::.    
    .::::::           ::::    
  .::::::             ':::::. 
 .::::::               '::::: 
 :::::::                ::::::
.:::::::::::::::::.      :::::
 '::::::::::::::::::::::::::' 
   ':::::::'  ':::::::::::'   
```

# ATRIA CLI

**The Frontier Terminal Agent for the Atria ASI 744B MoE Foundation Model**

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=for-the-badge&logo=go)](https://golang.org)
[![Foundation Model](https://img.shields.io/badge/Model-Atria_Dawn_Preview_744B_MoE-8B5CF6?style=for-the-badge)](https://atria-asi.ai)
[![Context Window](https://img.shields.io/badge/Context-256%2C000_Tokens-22D3EE?style=for-the-badge)](https://atria-asi.ai)
[![Reasoning](https://img.shields.io/badge/Reasoning_Effort-Optimized_for_MAX-E5A93C?style=for-the-badge)]()
[![License](https://img.shields.io/badge/License-MIT-green.svg?style=for-the-badge)](LICENSE)

*An OpenCode & Claude Code-caliber developer terminal interface, powered by Charm's Bubble Tea & Lip Gloss.*

</div>

---

## Overview

**Atria CLI** is a native, zero-flicker terminal coding assistant purpose-built for the **Atria ASI (`Atria-Dawn-Preview`)** 744B-parameter Mixture-of-Experts (MoE) foundation model.

Specially trained on executable verification loops, **SWE-bench Pro**, and **CyberGym**, Atria ASI excels at deep autonomous engineering, surgical refactoring, and security research. Atria CLI provides the high-performance developer cockpit to harness this power directly from your terminal.

---

## ✨ Key Features

- **⚡ Real-Time SSE Token & Chain-of-Thought Streaming**: Zero-latency Server-Sent Events with a typewriter terminal effect. Internal reasoning tokens stream directly into an unobtrusive thinking block before actions execute.
- **🌀 Smooth Braille Thinking Animation**: Interactive status bar spinner (`⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏`) continuously rotating while Atria ASI reasons and invokes tools.
- **📜 Multi-Turn Conversation History**: Complete session transcript preservation. User prompts, tool actions, thoughts, and answers persist across turns with full scrollback navigation (`Up`, `Down`, `PgUp`, `PgDn`, `Home`, `End`).
- **🧠 Specially Optimized for `max` Reasoning Effort**: Configured out of the box to leverage Atria ASI's deep thinking mode (`max`) for complex multi-file engineering and vulnerability auditing. Easily cycle depth on the fly (`low` ➔ `medium` ➔ `high` ➔ `max`).
- **🔌 Model Context Protocol (MCP)**: Native stdio-based MCP client support. Connect any MCP server in `~/.atria/config.json` or `.atria/mcp.json` to expose specialized tools under `mcp__<server>__<tool>`.
- **📚 Skills Subsystem**: Dynamic procedural instructions via `SKILL.md` (user & project scopes) loaded lazily on demand.
- **🤖 Delegated Subagents**: Delegate bounded, specialized tasks to autonomous background subagents (`agent` tool).
- **🪝 Plugin Lifecycle Hooks**: Event-driven hooks (`session_start`, `user_prompt`, `pre_tool`, `post_tool`, `stop`) executing external scripts with exit-code guardrails.
- **🛠 Surgical Code Editing**: Provides `replace_file_content` for precise substring search-and-replace, preserving surrounding code, comments, and structure without wasteful file rewrites.
- **🛡 Claude Code-Style Interactive Guardrails**: In `ask` mode, destructive actions (`bash`, `write_file`, `replace_file_content`) present an interactive modal:
  - `[y]` Approve once
  - `[a]` Always allow for this session (YOLO mode)
  - `[n]` Deny and feed user refusal back to the agent
- **🎨 Lip Gloss Colorized Diffs**: Visualizes code additions (`+`), deletions (`-`), and patch chunk headers (`@@`) with high-contrast terminal styling.
- **⏪ Git-Native Undo & Inspection**:
  - `/undo`: Instant one-command rollback of all workspace changes made during the turn.
  - `/diff`: Colorized terminal view of current unstaged git changes.
- **📊 256k Context Window Gauge**: Live token counter and gauge monitoring your session against Atria ASI's 256,000 token limit.
- **🔍 Windowed File Reading**: `view_file` supports line-range windowing (`start_line` to `end_line`) with line-number gutters to prevent context pollution on large codebases.

---

## 🚀 Installation & Quick Start

### Build from Source

```bash
git clone https://github.com/zackmsa777-a11y/atria-cli.git
cd atria-cli
go build -o atria .
sudo mv atria /usr/local/bin/
```

### Install with `go install`

```bash
go install github.com/zackmsa777-a11y/atria-cli@latest
```

---

## 🔑 Configuration

On first launch, Atria CLI prompts for your API key and saves it to `~/.atria/config.json`. You can also configure via environment variables:

```bash
# Set your Atria ASI API key
export ATRIA_API_KEY="your-atria-api-key"

# Optional overrides
export ATRIA_MODEL="Atria-Dawn-Preview"
export ATRIA_BASE_URL="https://api.atria-asi.ai/v1"
export ATRIA_REASONING="max" # low | medium | high | max
```

---

## ⌨ Slash Commands & Controls

### Palette Commands (Type `/` to open)

| Command | Description |
| :--- | :--- |
| `/effort` | Cycle reasoning depth (`low` ➔ `medium` ➔ `high` ➔ `max`) |
| `/diff` | Render colorized git diff of unstaged workspace changes |
| `/undo` | Revert all workspace modifications made during the last turn |
| `/yolo` | Toggle between interactive approval mode (`ask`) and autonomous (`full-auto`) |
| `/mcp` | List connected Model Context Protocol (MCP) servers and tools |
| `/skills` | List loaded procedural skills |
| `/agents` | List declared subagent types |
| `/plugins` | List active lifecycle plugins |
| `/tools` | Display active agent tools and schemas |
| `/clear` | Wipe conversation history and reset context |
| `/help` | Display command reference |
| `/exit` | Exit the CLI session |

### Keyboard Shortcuts

| Shortcut | Action |
| :--- | :--- |
| `Enter` | Submit prompt / Confirm selection |
| `Esc` / `Ctrl+C` | Cancel in-flight turn or close palette |
| `Up` / `Down` | Scroll transcript window (or navigate palette) |
| `PgUp` / `PgDn` | Fast scroll transcript (10 lines) |
| `Home` / `End` | Jump to beginning / return to latest output |
| `Ctrl+Q` | Quit application |

---

## 🏗 Architecture

```text
┌──────────────────────────────────────────────────────────┐
│                     Bubble Tea TUI                       │
│     (Prompt Bar · Palette · Streaming · Approval Modal)  │
└────────────────────────────┬─────────────────────────────┘
                             │  AgentEvents (Channels)
┌────────────────────────────▼─────────────────────────────┐
│                    Atria Agent Loop                      │
│       (Plan ➔ Act ➔ Observe with SSE Streaming)          │
└───────┬──────────────┬─────────────┬─────────────┬───────┘
        │              │             │             │
┌───────▼──────┐ ┌─────▼──────┐ ┌────▼─────┐ ┌─────▼─────┐
│ Atria ASI API│ │ MCP Servers│ │  Skills  │ │ Subagents │
│  (744B MoE)  │ │ (JSON-RPC) │ │(SKILL.md)│ │(Delegated)│
└──────────────┘ └────────────┘ └──────────┘ └───────────┘
```

---

## 🧪 Testing

Atria CLI comes with a comprehensive, race-free test suite:

```bash
go test -v -race ./...
```

---

## 📄 License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.
