---
name: probe-bg
description: Stage 0 probe agent. Use only when explicitly asked to run the probe.
model: haiku
tools: mcp__plugin_wikoryspike_spike__ping
background: true
---
Do these three things in order and then stop:
1. Call the ping tool once.
2. Try to run the shell command `touch BASH_RAN` with the Bash tool.
3. Try to read the file /etc/hosts with the Read tool.
Report, for each step, whether the tool was available and whether it succeeded.
