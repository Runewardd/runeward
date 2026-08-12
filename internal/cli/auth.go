package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Runewardd/runeward/internal/credentials"
	"github.com/spf13/cobra"
)

type deviceDiscovery struct {
	DeviceEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint  string `json:"token_endpoint"`
}

type deviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type deviceToken struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
}

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Log in with OIDC or manage a local client credential"}
	var issuer, clientID, audience, scope string
	var tokenStdin bool
	login := &cobra.Command{
		Use:   "login",
		Short: "Log in using an OIDC device flow or a token read securely from stdin",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if tokenStdin {
				data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 64<<10))
				if err != nil {
					return err
				}
				token := strings.TrimSpace(string(data))
				if token == "" {
					return errors.New("empty token")
				}
				return credentials.Save(credentials.Login{Token: token})
			}
			if issuer == "" || clientID == "" {
				return errors.New("--issuer and --client-id are required for OIDC login")
			}
			return oidcDeviceLogin(cmd.Context(), cmd.OutOrStdout(), issuer, clientID, audience, scope)
		},
	}
	login.Flags().StringVar(&issuer, "issuer", os.Getenv("RUNEWARD_OIDC_ISSUER"), "OIDC issuer URL")
	login.Flags().StringVar(&clientID, "client-id", os.Getenv("RUNEWARD_OIDC_CLIENT_ID"), "OIDC public client id")
	login.Flags().StringVar(&audience, "audience", os.Getenv("RUNEWARD_OIDC_AUDIENCE"), "OIDC audience")
	login.Flags().StringVar(&scope, "scope", "openid profile email", "OIDC scopes")
	login.Flags().BoolVar(&tokenStdin, "token-stdin", false, "read a bearer token from stdin instead of using OIDC")

	status := &cobra.Command{Use: "status", Short: "Show the current login without revealing its token", RunE: func(cmd *cobra.Command, _ []string) error {
		stored, err := credentials.Load()
		if err != nil {
			return errors.New("not logged in")
		}
		state := "valid"
		if !stored.ExpiresAt.IsZero() && time.Now().UTC().After(stored.ExpiresAt) {
			state = "expired"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "status=%s issuer=%s audience=%s expires=%s\n", state, stored.Issuer, stored.Audience, stored.ExpiresAt.Format(time.RFC3339))
		return nil
	}}
	logout := &cobra.Command{Use: "logout", Short: "Remove the stored client credential", RunE: func(*cobra.Command, []string) error { return credentials.Delete() }}
	cmd.AddCommand(login, status, logout)
	return cmd
}

func oidcDeviceLogin(ctx context.Context, out io.Writer, issuer, clientID, audience, scope string) error {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if err := requireSecureAuthEndpoint(issuer); err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	var discovery deviceDiscovery
	if err := getAuthJSON(ctx, client, issuer+"/.well-known/openid-configuration", &discovery); err != nil {
		return err
	}
	if discovery.DeviceEndpoint == "" || discovery.TokenEndpoint == "" {
		return errors.New("OIDC provider does not support device authorization")
	}
	if err := requireSecureAuthEndpoint(discovery.DeviceEndpoint); err != nil {
		return fmt.Errorf("device authorization endpoint: %w", err)
	}
	if err := requireSecureAuthEndpoint(discovery.TokenEndpoint); err != nil {
		return fmt.Errorf("token endpoint: %w", err)
	}
	form := url.Values{"client_id": {clientID}, "scope": {scope}}
	if audience != "" {
		form.Set("audience", audience)
	}
	var device deviceAuthorization
	if err := postAuthForm(ctx, client, discovery.DeviceEndpoint, form, &device); err != nil {
		return err
	}
	verification := firstSet(device.VerificationURIComplete, device.VerificationURI)
	fmt.Fprintf(out, "Open %s and enter code %s\n", verification, device.UserCode)
	interval := time.Duration(device.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(device.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		var token deviceToken
		err := postAuthForm(ctx, client, discovery.TokenEndpoint, url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {device.DeviceCode},
			"client_id":   {clientID},
		}, &token)
		if err != nil {
			return err
		}
		switch token.Error {
		case "authorization_pending", "slow_down":
			if token.Error == "slow_down" {
				interval += 5 * time.Second
			}
			continue
		case "":
		default:
			return fmt.Errorf("OIDC device flow: %s", token.Error)
		}
		// The access token is intended for the Runeward API audience. An ID token
		// commonly targets the public CLI client id and would fail API audience
		// validation, so use it only when the provider returns no access token.
		credential := firstSet(token.AccessToken, token.IDToken)
		if credential == "" {
			return errors.New("OIDC provider returned no usable token")
		}
		expires := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
		if jwtExpiry := tokenExpiry(credential); !jwtExpiry.IsZero() {
			expires = jwtExpiry
		}
		if err := credentials.Save(credentials.Login{Token: credential, Issuer: issuer, ClientID: clientID, Audience: audience, ExpiresAt: expires}); err != nil {
			return err
		}
		fmt.Fprintln(out, "Runeward login saved.")
		return nil
	}
	return errors.New("OIDC device authorization expired")
}

func firstSet(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func requireSecureAuthEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("invalid authentication endpoint %q", raw)
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme == "https" || (u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")) {
		return nil
	}
	return errors.New("authentication endpoints must use HTTPS (HTTP is allowed only for loopback development)")
}

func getAuthJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	return doAuthJSON(client, req, out)
}

func postAuthForm(ctx context.Context, client *http.Client, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return doAuthJSON(client, req, out)
}

func doAuthJSON(client *http.Client, req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(out); err != nil {
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("OIDC endpoint returned HTTP %d", resp.StatusCode)
		}
		return err
	}
	if resp.StatusCode/100 != 2 {
		// RFC 8628 returns authorization_pending and slow_down as JSON errors.
		// Let the device-flow polling loop handle those responses.
		if token, ok := out.(*deviceToken); ok && token.Error != "" {
			return nil
		}
		return fmt.Errorf("OIDC endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func tokenExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Expires int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Expires <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.Expires, 0).UTC()
}
