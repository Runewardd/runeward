package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/adefemi171/runeward/internal/backend"
	"github.com/spf13/cobra"
)

// fleetStateFile remembers the active fleet id so subcommands don't need --fleet.
const fleetStateFile = ".runeward-fleet"

// newFleetCmd drives a fleet on a running `runeward serve` over REST: push
// prompts and run an agent (Cursor, Codex, or Claude) on each, all governed.
func newFleetCmd() *cobra.Command {
	var base, agent, model, fleetID string

	cmd := &cobra.Command{
		Use:   "fleet",
		Short: "Drive a governed fleet by prompt (create, push prompts, run an agent on each)",
		Long: "Talks to a running `runeward serve`. `up` creates a fleet and remembers its id;\n" +
			"`exec` runs a prompt on one sandbox (workspace accumulates); `add`+`run` fan\n" +
			"prompts out across all workers in parallel. Pick the agent with --agent and\n" +
			"the model with --model.",
	}
	cmd.PersistentFlags().StringVar(&base, "base", envOr("RUNEWARD_BASE", "http://127.0.0.1:8080"),
		"control-plane base URL (or $RUNEWARD_BASE)")
	cmd.PersistentFlags().StringVar(&agent, "agent", envOr("AGENT", "cursor"),
		"agent to run: cursor | codex | claude (or $AGENT)")
	cmd.PersistentFlags().StringVar(&model, "model", os.Getenv("MODEL"),
		"model slug passed to the agent (or $MODEL)")
	cmd.PersistentFlags().StringVar(&fleetID, "fleet", "",
		"fleet id (defaults to the one saved by `fleet up`)")

	client := func() *fleetClient { return &fleetClient{base: base, http: &http.Client{}} }

	up := &cobra.Command{
		Use:   "up [profile]",
		Short: "Create a fleet (default profile: build-fleet) and remember its id",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := "build-fleet"
			if len(args) == 1 {
				profile = args[0]
			}
			var fv fleetView
			if err := client().call(cmd.Context(), http.MethodPost, "/v1/fleets",
				map[string]string{"profile": profile}, &fv); err != nil {
				return err
			}
			if err := os.WriteFile(fleetStateFile, []byte(fv.ID), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "runeward: fleet %s up (profile=%s, %d sandboxes)\n", fv.ID, profile, len(fv.Sandboxes))
			for _, s := range fv.Sandboxes {
				fmt.Fprintf(os.Stderr, "  %s\n", s)
			}
			return nil
		},
	}

	add := &cobra.Command{
		Use:   "add <prompt>",
		Short: "Add a prompt to the shared board (fanned out across workers by `run`)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveFleetID(fleetID)
			if err != nil {
				return err
			}
			var t task
			if err := client().call(cmd.Context(), http.MethodPost, "/v1/fleets/"+id+"/tasks",
				map[string]string{"payload": args[0]}, &t); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "runeward: added task %s\n", t.ID)
			return nil
		},
	}

	run := &cobra.Command{
		Use:   "run",
		Short: "Drain the board: every worker builds pending prompts in parallel",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveFleetID(fleetID)
			if err != nil {
				return err
			}
			return client().drain(cmd.Context(), id, agent, model)
		},
	}

	build := &cobra.Command{
		Use:   "build <prompt>",
		Short: "up-if-needed + add + run, in one shot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(fleetStateFile); err != nil {
				if err := up.RunE(up, nil); err != nil {
					return err
				}
			}
			id, err := resolveFleetID(fleetID)
			if err != nil {
				return err
			}
			c := client()
			var t task
			if err := c.call(cmd.Context(), http.MethodPost, "/v1/fleets/"+id+"/tasks",
				map[string]string{"payload": args[0]}, &t); err != nil {
				return err
			}
			return c.drain(cmd.Context(), id, agent, model)
		},
	}

	exec := &cobra.Command{
		Use:   "exec <prompt>",
		Short: "Run a prompt on ONE sandbox (--sandbox, else the first) so changes accumulate",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveFleetID(fleetID)
			if err != nil {
				return err
			}
			c := client()
			sb, _ := cmd.Flags().GetString("sandbox")
			if sb == "" {
				var fv fleetView
				if err := c.call(cmd.Context(), http.MethodGet, "/v1/fleets/"+id, nil, &fv); err != nil {
					return err
				}
				if len(fv.Sandboxes) == 0 {
					return fmt.Errorf("fleet %s has no sandboxes", id)
				}
				sb = fv.Sandboxes[0]
			}
			cmdVec, err := agentCommand(agent, model, args[0])
			if err != nil {
				return err
			}
			out, err := c.shellExec(cmd.Context(), sb, cmdVec)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "runeward: [%s] %s\n", sb, args[0])
			fmt.Println(out)
			return nil
		},
	}
	exec.Flags().String("sandbox", "", "target a specific sandbox id (default: first in the fleet)")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show the board",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveFleetID(fleetID)
			if err != nil {
				return err
			}
			var tr tasksResp
			if err := client().call(cmd.Context(), http.MethodGet, "/v1/fleets/"+id+"/tasks", nil, &tr); err != nil {
				return err
			}
			for _, t := range tr.Tasks {
				fmt.Printf("%-9s %s\t%s\n", t.State, t.ID, t.Payload)
			}
			return nil
		},
	}

	export := &cobra.Command{
		Use:   "export [dir]",
		Short: "Copy every worker's /workspace to ./out (or <dir>)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveFleetID(fleetID)
			if err != nil {
				return err
			}
			dest := "./out"
			if len(args) == 1 {
				dest = args[0]
			}
			var fv fleetView
			if err := client().call(cmd.Context(), http.MethodGet, "/v1/fleets/"+id, nil, &fv); err != nil {
				return err
			}
			for _, sb := range fv.Sandboxes {
				out, err := filepath.Abs(expandHome(filepath.Join(dest, sb)))
				if err != nil {
					return err
				}
				if err := backend.Export(cmd.Context(), sb, out); err != nil {
					return fmt.Errorf("export %s: %w", sb, err)
				}
				fmt.Fprintf(os.Stderr, "runeward: exported %s -> %s\n", sb, out)
			}
			return nil
		},
	}

	down := &cobra.Command{
		Use:   "down",
		Short: "Kill the fleet and forget it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveFleetID(fleetID)
			if err != nil {
				return err
			}
			if err := client().call(cmd.Context(), http.MethodDelete, "/v1/fleets/"+id, nil, nil); err != nil {
				return err
			}
			_ = os.Remove(fleetStateFile)
			fmt.Fprintf(os.Stderr, "runeward: fleet %s down\n", id)
			return nil
		},
	}

	cmd.AddCommand(up, add, run, build, exec, status, export, down)
	return cmd
}

// --- REST client ---

type fleetClient struct {
	base string
	http *http.Client
}

type fleetView struct {
	ID        string   `json:"id"`
	Sandboxes []string `json:"sandboxes"`
}

type task struct {
	ID      string `json:"id"`
	Payload string `json:"payload"`
	State   string `json:"state"`
}

type claimResp struct {
	Claimed bool `json:"claimed"`
	Task    task `json:"task"`
}

type tasksResp struct {
	Tasks []task `json:"tasks"`
}

type toolResult struct {
	Verdict  string `json:"verdict"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Reason   string `json:"reason"`
}

// call issues a JSON request and decodes a 2xx body into out (may be nil).
func (c *fleetClient) call(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach control plane at %s (is `runeward serve` running?): %w", c.base, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		if msg := serverError(data); msg != "" {
			return fmt.Errorf("%s %s: %s (%s)", method, path, resp.Status, msg)
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// shellExec runs a command vector in one sandbox and returns its stdout.
func (c *fleetClient) shellExec(ctx context.Context, sandbox string, command []string) (string, error) {
	var res toolResult
	err := c.call(ctx, http.MethodPost, "/v1/sandboxes/"+sandbox+"/shell/exec",
		map[string]any{"command": command}, &res)
	if err != nil {
		return "", err
	}
	if res.Verdict == "deny" {
		return "", fmt.Errorf("policy denied: %s", res.Reason)
	}
	return res.Stdout, nil
}

// drain runs one worker per sandbox; each claims and builds pending tasks until
// the board is empty, in parallel.
func (c *fleetClient) drain(ctx context.Context, fleetID, agent, model string) error {
	var fv fleetView
	if err := c.call(ctx, http.MethodGet, "/v1/fleets/"+fleetID, nil, &fv); err != nil {
		return err
	}
	if len(fv.Sandboxes) == 0 {
		return fmt.Errorf("fleet %s has no sandboxes", fleetID)
	}
	var mu sync.Mutex
	say := func(format string, a ...any) {
		mu.Lock()
		fmt.Fprintf(os.Stderr, format+"\n", a...)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	for i, sb := range fv.Sandboxes {
		wg.Add(1)
		go func(sb, owner string) {
			defer wg.Done()
			for {
				var claim claimResp
				if err := c.call(ctx, http.MethodPost, "/v1/fleets/"+fleetID+"/claim",
					map[string]string{"owner": owner}, &claim); err != nil {
					say("!! [%s] claim failed: %v", owner, err)
					return
				}
				if !claim.Claimed {
					return
				}
				say(">> [%s/%s] building: %s", owner, sb, claim.Task.Payload)
				cmdVec, err := agentCommand(agent, model, claim.Task.Payload)
				if err != nil {
					say("!! [%s] %v", owner, err)
					return
				}
				out, err := c.shellExec(ctx, sb, cmdVec)
				if err != nil {
					_ = c.call(ctx, http.MethodPost, "/v1/fleets/"+fleetID+"/tasks/"+claim.Task.ID+"/fail",
						map[string]any{"error": err.Error(), "requeue": true}, nil)
					say("!! [%s] failed (requeued) %s: %v", owner, claim.Task.ID, err)
					continue
				}
				if strings.TrimSpace(out) != "" {
					say("%s", strings.TrimSpace(out))
				}
				_ = c.call(ctx, http.MethodPost, "/v1/fleets/"+fleetID+"/tasks/"+claim.Task.ID+"/complete",
					map[string]string{"result": "done by " + owner}, nil)
				say("<< [%s] done: %s", owner, claim.Task.ID)
			}
		}(sb, fmt.Sprintf("worker-%d", i+1))
	}
	wg.Wait()
	return nil
}

// --- helpers ---

// agentCommand builds the exec command vector for the selected agent.
func agentCommand(agent, model, prompt string) ([]string, error) {
	switch agent {
	case "cursor":
		cmd := []string{"agent", "-p", prompt, "--force", "--output-format", "text"}
		if model != "" {
			cmd = append(cmd, "--model", model)
		}
		return cmd, nil
	case "codex":
		cmd := []string{"codex", "exec", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox"}
		if model != "" {
			cmd = append(cmd, "-m", model)
		}
		return append(cmd, prompt), nil
	case "claude":
		cmd := []string{"claude", "-p", prompt, "--dangerously-skip-permissions"}
		if model != "" {
			cmd = append(cmd, "--model", model)
		}
		return cmd, nil
	default:
		return nil, fmt.Errorf("unknown agent %q (use cursor, codex, or claude)", agent)
	}
}

// resolveFleetID returns the explicit id, else the one saved by `fleet up`.
func resolveFleetID(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	b, err := os.ReadFile(fleetStateFile)
	if err != nil {
		return "", fmt.Errorf("no active fleet; run `runeward fleet up` first or pass --fleet <id>")
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return "", fmt.Errorf("no active fleet; run `runeward fleet up` first or pass --fleet <id>")
	}
	return id, nil
}

func serverError(data []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil {
		return e.Error
	}
	return ""
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
