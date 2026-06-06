// Package cli — mcp setup subcommand.
// spec: docs/features/end-user-onboarding/spec.md § 4
//
// 偵測 / 建 / 補對應 AI CLI host 的 MCP server config，使其能載入 0ops-mcp。
// 目前支援：claude-code（JSON）、codex（TOML 子集，line-based 處理）。
// copilot-cli 因 MCP config 規格未穩，僅印手動指引。
package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/winshare/zeroops/internal/shared/authconfig"
)

func newMcpCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP host integration helpers",
	}
	cmd.AddCommand(newMcpSetupCommand())
	return cmd
}

func newMcpSetupCommand() *cobra.Command {
	var (
		opsHost    string
		mcpBinary  string
		configPath string
		printOnly  bool
		assumeYes  bool
	)
	cmd := &cobra.Command{
		Use:   "setup <host>",
		Short: "Write 0ops MCP server entry into a supported AI CLI host config",
		Long: `Supported hosts:
  claude-code (alias: claude)   → ~/.claude.json mcpServers."0ops"
  codex                          → ~/.codex/config.toml [mcp_servers.0ops]
  copilot-cli                    → manual instructions (auto-write not yet supported)

Behavior:
  - Detects 0ops-mcp binary (--mcp-binary > $PATH > sibling of 0ops binary).
  - Reads OPS_HOST from auth.json unless --ops-host is set.
  - Idempotent: re-run is no-op when entry already matches.
  - Backs up existing config to <path>.bak.<timestamp> before overwriting.
  - --print-only dumps the snippet to stdout without touching files.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := strings.ToLower(strings.TrimSpace(args[0]))
			plan, err := planMcpSetup(host, opsHost, mcpBinary, configPath)
			if err != nil {
				return err
			}
			if printOnly {
				return plan.printSnippet(cmd.OutOrStdout())
			}
			return plan.apply(cmd, assumeYes)
		},
	}
	cmd.Flags().StringVar(&opsHost, "ops-host", "", "backend host (default: from auth.json)")
	cmd.Flags().StringVar(&mcpBinary, "mcp-binary", "", "path to 0ops-mcp binary (default: $PATH / sibling)")
	cmd.Flags().StringVar(&configPath, "config", "", "override target config path")
	cmd.Flags().BoolVar(&printOnly, "print-only", false, "dump snippet without writing")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "skip prompt when overwriting")
	return cmd
}

type mcpHost string

const (
	hostClaudeCode mcpHost = "claude-code"
	hostCodex      mcpHost = "codex"
	hostCopilotCLI mcpHost = "copilot-cli"
)

type mcpSetupPlan struct {
	host       mcpHost
	configPath string
	mcpBinary  string
	opsHost    string
}

func planMcpSetup(rawHost, opsHostFlag, mcpBinaryFlag, configPathFlag string) (*mcpSetupPlan, error) {
	host, err := resolveMcpHostKey(rawHost)
	if err != nil {
		return nil, err
	}
	mcpBin, err := resolveMcpBinary(mcpBinaryFlag)
	if err != nil {
		return nil, err
	}
	opsHost, err := resolveOpsHost(opsHostFlag)
	if err != nil {
		return nil, err
	}
	cfgPath, err := resolveConfigPath(host, configPathFlag)
	if err != nil {
		return nil, err
	}
	return &mcpSetupPlan{host: host, configPath: cfgPath, mcpBinary: mcpBin, opsHost: opsHost}, nil
}

func resolveMcpHostKey(raw string) (mcpHost, error) {
	switch raw {
	case "claude-code", "claude":
		return hostClaudeCode, nil
	case "codex":
		return hostCodex, nil
	case "copilot-cli", "copilot":
		return hostCopilotCLI, nil
	default:
		return "", fmt.Errorf("unknown MCP host %q (allowed: claude-code, codex, copilot-cli)", raw)
	}
}

func resolveMcpBinary(flag string) (string, error) {
	if flag != "" {
		abs, err := filepath.Abs(flag)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	if p, err := exec.LookPath("0ops-mcp"); err == nil {
		return p, nil
	}
	// fallback: sibling of current binary
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "0ops-mcp")
		if _, err := os.Stat(sibling); err == nil {
			return sibling, nil
		}
	}
	return "", errors.New("0ops-mcp not found on PATH; pass --mcp-binary or run scripts/install.sh")
}

func resolveOpsHost(flag string) (string, error) {
	if flag != "" {
		return normalizeOpsHost(flag), nil
	}
	cfg, err := authconfig.Load()
	if err != nil {
		return "", fmt.Errorf("read auth.json: %w (run 0ops auth login or pass --ops-host)", err)
	}
	tok, ok := cfg.First()
	if !ok || strings.TrimSpace(tok.Host) == "" {
		return "", errors.New("auth.json has no token entry (run 0ops auth login or pass --ops-host)")
	}
	return normalizeOpsHost(tok.Host), nil
}

func normalizeOpsHost(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimRight(v, "/")
	if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
		v = "https://" + v
	}
	return v
}

func resolveConfigPath(host mcpHost, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch host {
	case hostClaudeCode:
		return filepath.Join(home, ".claude.json"), nil
	case hostCodex:
		return filepath.Join(home, ".codex", "config.toml"), nil
	case hostCopilotCLI:
		return "", nil
	default:
		return "", fmt.Errorf("internal: no default path for host %q", host)
	}
}

func (p *mcpSetupPlan) printSnippet(w io.Writer) error {
	switch p.host {
	case hostClaudeCode:
		entry := map[string]any{
			"mcpServers": map[string]any{
				"0ops": p.claudeEntry(),
			},
		}
		out, err := json.MarshalIndent(entry, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "# target: %s\n%s\n", p.configPath, out)
		return err
	case hostCodex:
		_, err := fmt.Fprintf(w, "# target: %s\n%s", p.configPath, p.codexSection())
		return err
	case hostCopilotCLI:
		return printCopilotInstructions(w, p.mcpBinary, p.opsHost)
	}
	return nil
}

func (p *mcpSetupPlan) apply(cmd *cobra.Command, assumeYes bool) error {
	switch p.host {
	case hostClaudeCode:
		return p.applyClaudeCode(cmd, assumeYes)
	case hostCodex:
		return p.applyCodex(cmd, assumeYes)
	case hostCopilotCLI:
		return printCopilotInstructions(cmd.OutOrStdout(), p.mcpBinary, p.opsHost)
	}
	return nil
}

// --- Claude Code (JSON) ---

func (p *mcpSetupPlan) claudeEntry() map[string]any {
	return map[string]any{
		"command": p.mcpBinary,
		"env": map[string]any{
			"OPS_HOST": p.opsHost,
		},
	}
}

func (p *mcpSetupPlan) applyClaudeCode(cmd *cobra.Command, assumeYes bool) error {
	raw, err := os.ReadFile(p.configPath) //nolint:gosec // path scoped under user home
	root := map[string]any{}
	switch {
	case err == nil:
		if len(raw) > 0 {
			if jerr := json.Unmarshal(raw, &root); jerr != nil {
				return fmt.Errorf("parse %s: %w (refusing to overwrite; fix or move the file)", p.configPath, jerr)
			}
		}
	case errors.Is(err, os.ErrNotExist):
		// fresh file
	default:
		return fmt.Errorf("read %s: %w", p.configPath, err)
	}

	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	desired := p.claudeEntry()
	if existing, ok := servers["0ops"].(map[string]any); ok && mapsEqual(existing, desired) {
		fmt.Fprintf(cmd.OutOrStdout(), "claude-code config already up-to-date: %s\n", p.configPath)
		return nil
	}
	if _, present := servers["0ops"]; present && !assumeYes {
		ok, perr := promptYes(cmd, fmt.Sprintf("Overwrite existing mcpServers.0ops in %s? [yes/NO] ", p.configPath))
		if perr != nil {
			return perr
		}
		if !ok {
			return errors.New("aborted")
		}
	}
	servers["0ops"] = desired
	root["mcpServers"] = servers

	if err := backupIfExists(p.configPath); err != nil {
		return err
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(p.configPath, append(out, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "claude-code config written: %s\nRestart Claude Code to load MCP server.\n", p.configPath)
	return nil
}

// --- Codex CLI (TOML subset, line-based) ---

func (p *mcpSetupPlan) codexSection() string {
	var b strings.Builder
	b.WriteString("[mcp_servers.0ops]\n")
	fmt.Fprintf(&b, "command = %q\n", p.mcpBinary)
	b.WriteString("args = []\n")
	fmt.Fprintf(&b, "env = { OPS_HOST = %q }\n", p.opsHost)
	return b.String()
}

func (p *mcpSetupPlan) applyCodex(cmd *cobra.Command, assumeYes bool) error {
	existing, err := os.ReadFile(p.configPath) //nolint:gosec // path scoped under user home
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", p.configPath, err)
	}
	current := string(existing)
	updated, present, sameAsExisting, rerr := replaceTomlTable(current, "mcp_servers.0ops", p.codexSection())
	if rerr != nil {
		return rerr
	}
	if sameAsExisting {
		fmt.Fprintf(cmd.OutOrStdout(), "codex config already up-to-date: %s\n", p.configPath)
		return nil
	}
	if present && !assumeYes {
		ok, perr := promptYes(cmd, fmt.Sprintf("Overwrite existing [mcp_servers.0ops] in %s? [yes/NO] ", p.configPath))
		if perr != nil {
			return perr
		}
		if !ok {
			return errors.New("aborted")
		}
	}
	if err := os.MkdirAll(filepath.Dir(p.configPath), 0o700); err != nil {
		return err
	}
	if err := backupIfExists(p.configPath); err != nil {
		return err
	}
	if err := writeFileAtomic(p.configPath, []byte(updated), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "codex config written: %s\nReload Codex CLI to pick up the new MCP server.\n", p.configPath)
	return nil
}

// replaceTomlTable replaces (or appends) the named table section in src.
// Returns updated text, whether the table existed, whether the existing content
// equals the desired section (idempotency), and an error.
//
// This is a line-based handler for the subset of TOML the spec emits — table
// headers of form "[mcp_servers.0ops]" and key/value lines until the next
// "[" header or EOF. Comments and other tables are preserved verbatim.
func replaceTomlTable(src, tableName, desiredSection string) (string, bool, bool, error) {
	desired := strings.TrimRight(desiredSection, "\n")
	header := "[" + tableName + "]"

	if src == "" {
		return desired + "\n", false, false, nil
	}

	lines := strings.Split(src, "\n")
	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == header {
			start = i
			break
		}
	}
	if start < 0 {
		var b strings.Builder
		b.WriteString(strings.TrimRight(src, "\n"))
		b.WriteString("\n\n")
		b.WriteString(desired)
		b.WriteString("\n")
		return b.String(), false, false, nil
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			end = i
			break
		}
	}
	existingSection := strings.TrimRight(strings.Join(lines[start:end], "\n"), "\n")
	if existingSection == desired {
		return src, true, true, nil
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString(strings.Join(lines[:start], "\n"))
		b.WriteString("\n")
	}
	b.WriteString(desired)
	b.WriteString("\n")
	if end < len(lines) {
		b.WriteString(strings.Join(lines[end:], "\n"))
	}
	return b.String(), true, false, nil
}

// --- Copilot CLI (instructions only) ---

func printCopilotInstructions(w io.Writer, mcpBin, opsHost string) error {
	_, err := fmt.Fprintf(w, `GitHub Copilot CLI MCP config 規格 v1 未穩定，本工具暫不自動寫入。

手動指引：
  - 待 Copilot CLI MCP support 釋出後，參照其 release notes 將下列值寫入對應 config：
    command: %s
    env:
      OPS_HOST: %s

替代方案：先用 claude-code 或 codex：
  0ops mcp setup claude-code
  0ops mcp setup codex

reference: docs/features/end-user-onboarding/mcp-hosts/copilot-cli.md
`, mcpBin, opsHost)
	return err
}

// --- helpers ---

func backupIfExists(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	bak := fmt.Sprintf("%s.bak.%s", path, time.Now().UTC().Format("20060102150405"))
	in, err := os.ReadFile(path) //nolint:gosec // path scoped to user-controlled config under home
	if err != nil {
		return err
	}
	return os.WriteFile(bak, in, 0o600)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".0ops-mcpsetup-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		switch ta := va.(type) {
		case map[string]any:
			tb, ok := vb.(map[string]any)
			if !ok || !mapsEqual(ta, tb) {
				return false
			}
		default:
			if fmt.Sprintf("%v", va) != fmt.Sprintf("%v", vb) {
				return false
			}
		}
	}
	return true
}

func promptYes(cmd *cobra.Command, msg string) (bool, error) {
	fmt.Fprint(cmd.OutOrStdout(), msg)
	r := bufio.NewReader(cmd.InOrStdin())
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return strings.TrimSpace(line) == "yes", nil
}
