package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/shinma06/entire-agent-devin/internal/devin"
	"github.com/shinma06/entire-agent-devin/internal/protocol"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: entire-agent-devin <subcommand> [args]")
	}
	if err := run(os.Args[1], os.Args[2:], os.Stdin, os.Stdout); err != nil {
		fatalf("%v", err)
	}
}

func run(cmd string, args []string, in io.Reader, out io.Writer) error {
	root := protocol.RepoRoot()
	flags := func(names ...string) (map[string]string, error) {
		parsed, err := protocol.Flags(args, names...)
		if err != nil {
			return nil, err
		}
		m := map[string]string{}
		for k, v := range parsed {
			m[k] = *v
		}
		return m, nil
	}
	switch cmd {
	case "info":
		return protocol.WriteJSON(out, devin.Info())
	case "detect":
		return protocol.WriteJSON(out, map[string]bool{"present": devin.Detect(root)})
	case "get-session-id":
		var hi protocol.HookInput
		if err := protocol.ReadJSON(in, &hi); err != nil {
			return err
		}
		return protocol.WriteJSON(out, map[string]string{"session_id": hi.SessionID})
	case "get-session-dir":
		f, err := flags("repo-path")
		if err != nil {
			return err
		}
		repo := f["repo-path"]
		if repo == "" {
			repo = root
		}
		return protocol.WriteJSON(out, map[string]string{"session_dir": devin.SessionDir(repo)})
	case "resolve-session-file":
		f, err := flags("session-dir", "session-id")
		if err != nil {
			return err
		}
		return protocol.WriteJSON(out, map[string]string{"session_file": devin.SessionFile(f["session-dir"], f["session-id"])})
	case "read-session":
		var hi protocol.HookInput
		if err := protocol.ReadJSON(in, &hi); err != nil {
			return err
		}
		s, err := devin.ReadSession(hi, root)
		if err != nil {
			return err
		}
		return protocol.WriteJSON(out, s)
	case "write-session":
		var s protocol.AgentSession
		if err := protocol.ReadJSON(in, &s); err != nil {
			return err
		}
		return devin.WriteSession(s)
	case "read-transcript":
		f, err := flags("session-ref")
		if err != nil {
			return err
		}
		data, err := os.ReadFile(f["session-ref"])
		if err != nil {
			return err
		}
		_, err = out.Write(data)
		return err
	case "chunk-transcript":
		f, err := flags("max-size")
		if err != nil {
			return err
		}
		max, err := strconv.Atoi(f["max-size"])
		if err != nil || max <= 0 {
			return fmt.Errorf("chunk-transcript: --max-size must be a positive integer, got %q", f["max-size"])
		}
		data, err := io.ReadAll(in)
		if err != nil {
			return err
		}
		return protocol.WriteJSON(out, protocol.Chunks{Chunks: devin.ChunkTranscript(data, max)})
	case "reassemble-transcript":
		var c protocol.Chunks
		if err := protocol.ReadJSON(in, &c); err != nil {
			return err
		}
		for _, chunk := range c.Chunks {
			if _, err := out.Write(chunk); err != nil {
				return err
			}
		}
		return nil
	case "format-resume-command":
		f, err := flags("session-id")
		if err != nil {
			return err
		}
		return protocol.WriteJSON(out, map[string]string{"command": devin.ResumeCommand(f["session-id"])})
	case "parse-hook":
		f, err := flags("hook")
		if err != nil {
			return err
		}
		ev, err := devin.ParseHook(f["hook"], in, root)
		if err != nil {
			return err
		}
		if ev == nil {
			_, err = io.WriteString(out, "null\n")
			return err
		}
		return protocol.WriteJSON(out, ev)
	case "install-hooks":
		force := false
		for _, a := range args {
			if a == "--force" {
				force = true
			}
		}
		n, err := devin.InstallHooks(root, force)
		if err != nil {
			return err
		}
		return protocol.WriteJSON(out, map[string]int{"hooks_installed": n})
	case "uninstall-hooks":
		return devin.UninstallHooks(root)
	case "are-hooks-installed":
		ok, err := devin.AreHooksInstalled(root)
		if err != nil {
			return err
		}
		return protocol.WriteJSON(out, map[string]bool{"installed": ok})
	case "get-transcript-position":
		f, err := flags("path")
		if err != nil {
			return err
		}
		pos, err := devin.TranscriptPosition(f["path"])
		if err != nil {
			return err
		}
		return protocol.WriteJSON(out, map[string]int{"position": pos})
	case "extract-modified-files":
		f, err := flags("path", "offset")
		if err != nil {
			return err
		}
		off, _ := strconv.Atoi(f["offset"])
		recs, pos, err := devin.RecordsFrom(f["path"], off)
		if err != nil {
			return err
		}
		return protocol.WriteJSON(out, map[string]any{"files": devin.ModifiedFiles(recs, root), "current_position": pos})
	case "extract-prompts":
		f, err := flags("session-ref", "offset")
		if err != nil {
			return err
		}
		off, _ := strconv.Atoi(f["offset"])
		recs, _, err := devin.RecordsFrom(f["session-ref"], off)
		if err != nil {
			return err
		}
		return protocol.WriteJSON(out, map[string][]string{"prompts": devin.Prompts(recs)})
	case "extract-summary":
		f, err := flags("session-ref")
		if err != nil {
			return err
		}
		recs, _, err := devin.RecordsFrom(f["session-ref"], 0)
		if err != nil {
			return err
		}
		s := devin.Summary(recs)
		return protocol.WriteJSON(out, map[string]any{"summary": s, "has_summary": s != ""})
	case "compact-transcript":
		f, err := flags("session-ref")
		if err != nil {
			return err
		}
		data, err := os.ReadFile(f["session-ref"])
		if err != nil {
			return err
		}
		compact, err := devin.CompactTranscript(data)
		if err != nil {
			return err
		}
		return protocol.WriteJSON(out, protocol.CompactTranscriptResponse{Transcript: base64.StdEncoding.EncodeToString(compact)})
	case "dispatch":
		f, err := flags("hook")
		if err != nil {
			return err
		}
		devin.Dispatch(f["hook"], in, os.Stderr)
		return nil
	case "version", "--version":
		_, err := fmt.Fprintln(out, "entire-agent-devin dev")
		return err
	default:
		return fmt.Errorf("unknown subcommand: %s", cmd)
	}
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
