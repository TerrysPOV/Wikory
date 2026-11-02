//go:build spike && unix

// Command spike-recorder supports the stage 0 spikes. Modes:
//
//	record <label>                  save hook stdin to $SPIKE_OUT/<label>-<event>-<nanos>.json
//	block-once <label> <reason>     record, then block a Stop once (only when stop_hook_active is false)
//	detach <label> <secs>           record, start a detached child that writes a marker after <secs>, then sleep 5 s
//	sleepwrite <label> <secs>       (internal) sleep then write <label>-survived.txt
//	lagcopy <label> <transcript>    (internal) wait 5 s, then copy the transcript again
//	ttyprobe                        report whether fds 0, 1, 2 are terminals and whether /dev/tty opens
//	writeprobe <dir>                try to create <dir>/probe.txt and report the result
//	mcp                             minimal stdio MCP server with one tool, ping
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func outDir() string {
	d := os.Getenv("SPIKE_OUT")
	if d == "" {
		d = os.TempDir()
	}
	_ = os.MkdirAll(d, 0o700)
	return d
}

func record(label string) map[string]any {
	raw, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	event, _ := p["hook_event_name"].(string)
	if event == "" {
		event = "unknown"
	}
	name := fmt.Sprintf("%s-%s-%d.json", label, event, time.Now().UnixNano())
	_ = os.WriteFile(filepath.Join(outDir(), name), raw, 0o600)
	if tp, ok := p["transcript_path"].(string); ok && tp != "" && os.Getenv("SPIKE_COPY_TRANSCRIPT") == "1" {
		if data, err := os.ReadFile(tp); err == nil {
			_ = os.WriteFile(filepath.Join(outDir(), name+".transcript-at-hook.jsonl"), data, 0o600)
		}
		spawnDetached("lagcopy", name, tp)
	}
	return p
}

func spawnDetached(args ...string) {
	self, err := os.Executable()
	if err != nil {
		return
	}
	c := exec.Command(self, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.Env = os.Environ()
	if err := c.Start(); err == nil {
		_ = c.Process.Release()
	}
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "record":
		record(os.Args[2])
	case "block-once":
		p := record(os.Args[2])
		if active, _ := p["stop_hook_active"].(bool); !active {
			out, _ := json.Marshal(map[string]string{"decision": "block", "reason": os.Args[3]})
			fmt.Println(string(out))
		}
	case "detach":
		record(os.Args[2])
		spawnDetached("sleepwrite", os.Args[2], os.Args[3])
		time.Sleep(5 * time.Second) // longer than the hook timeout: the harness kills this process
	case "sleepwrite":
		secs, _ := strconv.Atoi(os.Args[3])
		time.Sleep(time.Duration(secs) * time.Second)
		msg := fmt.Sprintf("survived pid=%d ppid=%d at=%s\n", os.Getpid(), os.Getppid(), time.Now().Format(time.RFC3339Nano))
		_ = os.WriteFile(filepath.Join(outDir(), os.Args[2]+"-survived.txt"), []byte(msg), 0o600)
	case "lagcopy":
		time.Sleep(5 * time.Second)
		if data, err := os.ReadFile(os.Args[3]); err == nil {
			_ = os.WriteFile(filepath.Join(outDir(), os.Args[2]+".transcript-after-5s.jsonl"), data, 0o600)
		}
	case "ttyprobe":
		res := map[string]any{}
		for fd := 0; fd <= 2; fd++ {
			_, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
			res["fd"+strconv.Itoa(fd)+"_is_tty"] = err == nil
		}
		if f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
			res["dev_tty_opens"] = true
			f.Close()
		} else {
			res["dev_tty_opens"] = false
		}
		out, _ := json.Marshal(res)
		_ = os.WriteFile(filepath.Join(outDir(), "ttyprobe.json"), out, 0o600)
		fmt.Println(string(out))
	case "writeprobe":
		err := os.WriteFile(filepath.Join(os.Args[2], "probe.txt"), []byte("probe\n"), 0o600)
		fmt.Printf("writeprobe dir=%s error=%v\n", os.Args[2], err)
	case "mcp":
		serveMCP()
	}
}

func serveMCP() {
	logf, _ := os.OpenFile(filepath.Join(outDir(), "mcp-calls.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	enc := json.NewEncoder(os.Stdout)
	for in.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &req) != nil {
			continue
		}
		fmt.Fprintf(logf, "%s %s %s\n", time.Now().Format(time.RFC3339Nano), req.Method, req.Params)
		if len(req.ID) == 0 {
			continue // notification
		}
		var result any
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			result = map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "spike", "version": "0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name": "ping", "description": "Stage 0 probe. Returns pong.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "pong"}}}
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
			continue
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
}
