package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Runewardd/runeward/internal/authz"
	"github.com/Runewardd/runeward/internal/controlplane"
	"github.com/Runewardd/runeward/internal/mcp"
	"github.com/Runewardd/runeward/internal/obs"
	"github.com/Runewardd/runeward/internal/server"
	"github.com/Runewardd/runeward/internal/telemetry"
	"github.com/Runewardd/runeward/web"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newServeCmd(configDir *string) *cobra.Command {
	var port int
	var noUI bool
	var bind, token, tlsCert, tlsKey string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the control plane: governed REST API + web dashboard",
		Long: "Start the runeward control plane. Every sandbox tool call is routed\n" +
			"through the policy engine, cost/loop guardrails, and the tamper-evident\n" +
			"audit ledger. Serves the REST API, an approval inbox, an interactive\n" +
			"terminal WebSocket, and (unless --no-ui) the web dashboard.\n\n" +
			"Binds to 127.0.0.1 by default. Exposing it on another interface\n" +
			"(--bind) requires an API token (--token or RUNEWARD_API_TOKEN).",
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				token = os.Getenv("RUNEWARD_API_TOKEN")
			}
			authzStore, err := authz.FromEnv()
			if err != nil {
				return fmt.Errorf("load %s: %w", authz.EnvFile, err)
			}
			// A configured RBAC store (per-principal tokens) also satisfies the
			// authentication requirement for a non-loopback bind.
			if !isLoopbackHost(bind) && token == "" && authzStore == nil {
				return fmt.Errorf("refusing to bind %s without authentication: set --token / RUNEWARD_API_TOKEN, configure %s, or bind 127.0.0.1", bind, authz.EnvFile)
			}
			if (tlsCert == "") != (tlsKey == "") {
				return fmt.Errorf("--tls-cert and --tls-key must be set together")
			}

			mgr, err := controlplane.New(resolveConfigDir(*configDir))
			if err != nil {
				return err
			}
			defer mgr.Close()

			logger := obs.NewLogger()
			obs.SetBuildInfo(version)

			var dashboard http.Handler
			if !noUI {
				dashboard = web.Handler()
			}
			srv := server.New(mgr, dashboard, logger)
			srv.AuthToken = token
			srv.Authz = authzStore
			// MCP streamable-HTTP lives at /mcp alongside REST.
			mcpSrv := mcp.NewServer(mgr)
			srv.MCP = mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return mcpSrv }, nil)

			addr := net.JoinHostPort(bind, strconv.Itoa(port))
			httpSrv := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

			scheme := "http"
			if tlsCert != "" {
				scheme = "https"
			}
			errCh := make(chan error, 1)
			go func() {
				if tlsCert != "" {
					errCh <- httpSrv.ListenAndServeTLS(tlsCert, tlsKey)
					return
				}
				errCh <- httpSrv.ListenAndServe()
			}()

			logger.Info("control plane listening",
				"addr", scheme+"://"+addr, "ui", !noUI, "metrics", "/metrics",
				"auth", token != "", "tls", tlsCert != "")
			if token == "" {
				logger.Warn("control plane is UNAUTHENTICATED (bound to loopback); set --token before exposing it")
			}
			logger.Info(telemetry.Notice())
			telemetry.Report(version, "serve_start", map[string]string{"ui": fmt.Sprintf("%t", !noUI), "auth": fmt.Sprintf("%t", token != "")})

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			select {
			case err := <-errCh:
				if err != nil && err != http.ErrServerClosed {
					return err
				}
				return nil
			case <-ctx.Done():
				logger.Info("shutting down control plane")
				shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				return httpSrv.Shutdown(shutCtx)
			}
		},
	}
	cmd.Flags().IntVar(&port, "port", 8080, "listen port")
	cmd.Flags().StringVar(&bind, "bind", "127.0.0.1", "interface to bind (use 0.0.0.0 to expose on the network; requires --token)")
	cmd.Flags().StringVar(&token, "token", "", "API token required on every request (falls back to RUNEWARD_API_TOKEN)")
	cmd.Flags().StringVar(&tlsCert, "tls-cert", "", "path to a TLS certificate (enables HTTPS; requires --tls-key)")
	cmd.Flags().StringVar(&tlsKey, "tls-key", "", "path to the TLS private key (requires --tls-cert)")
	cmd.Flags().BoolVar(&noUI, "no-ui", false, "serve the REST API only, without the web dashboard")
	return cmd
}

// isLoopbackHost reports whether host is a loopback interface (or empty, which
// http treats as all interfaces — so it is not considered loopback).
func isLoopbackHost(host string) bool {
	switch host {
	case "localhost":
		return true
	case "":
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func resolveConfigDir(configDir string) string {
	if configDir == "" {
		return os.Getenv("RUNEWARD_CONFIG_DIR")
	}
	return configDir
}

func newMCPCmd(configDir *string) *cobra.Command {
	var asHTTP bool
	var port int
	var bind, token string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server wrapping runeward's governed tools (stdio or --http)",
		Long: "Expose runeward's governed sandbox tools over the Model Context Protocol.\n" +
			"By default it speaks stdio (for Claude Desktop / Cursor); with --http it\n" +
			"serves the streamable-HTTP transport at /mcp on 127.0.0.1 (a non-loopback\n" +
			"--bind requires --token or RUNEWARD_API_TOKEN).",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := controlplane.New(resolveConfigDir(*configDir))
			if err != nil {
				return err
			}
			defer mgr.Close()

			mcpSrv := mcp.NewServer(mgr)

			if asHTTP {
				if token == "" {
					token = os.Getenv("RUNEWARD_API_TOKEN")
				}
				if !isLoopbackHost(bind) && token == "" {
					return fmt.Errorf("refusing to bind %s without authentication: set --token or RUNEWARD_API_TOKEN, or bind 127.0.0.1", bind)
				}
				handler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return mcpSrv }, nil)
				mux := http.NewServeMux()
				mux.Handle("/mcp", handler)
				mux.Handle("/mcp/", handler)
				addr := net.JoinHostPort(bind, strconv.Itoa(port))
				httpSrv := &http.Server{Addr: addr, Handler: server.TokenAuth(token, mux), ReadHeaderTimeout: 10 * time.Second}
				errCh := make(chan error, 1)
				go func() { errCh <- httpSrv.ListenAndServe() }()
				fmt.Fprintf(os.Stderr, "runeward: MCP (streamable HTTP) at http://%s/mcp (auth=%t)\n", addr, token != "")

				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				select {
				case err := <-errCh:
					if err != nil && err != http.ErrServerClosed {
						return err
					}
					return nil
				case <-ctx.Done():
					shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					return httpSrv.Shutdown(shutCtx)
				}
			}

			return mcpSrv.Run(cmd.Context(), &mcpsdk.StdioTransport{})
		},
	}
	cmd.Flags().BoolVar(&asHTTP, "http", false, "serve over streamable HTTP instead of stdio")
	cmd.Flags().IntVar(&port, "port", 8081, "listen port for --http")
	cmd.Flags().StringVar(&bind, "bind", "127.0.0.1", "interface to bind for --http (0.0.0.0 requires --token)")
	cmd.Flags().StringVar(&token, "token", "", "API token required on every --http request (falls back to RUNEWARD_API_TOKEN)")
	return cmd
}
