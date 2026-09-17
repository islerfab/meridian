package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/urfave/cli/v3"
	"golang.org/x/oauth2"
)

// Google OAuth 2.0 endpoints (stable, documented) — hardcoded to keep the
// bootstrap free of the heavyweight google API packages.
var googleOAuthEndpoint = oauth2.Endpoint{
	AuthURL:  "https://accounts.google.com/o/oauth2/auth",
	TokenURL: "https://oauth2.googleapis.com/token",
}

const calendarScope = "https://www.googleapis.com/auth/calendar"

// newOAuthCmd performs the interactive OAuth bootstrap flow:
// out-of-band laptop flow with a localhost:5555 loopback callback. The
// refresh token is either printed to stdout for piping into a SOPS secret
// (default) or written in place into an env file (--write-env) so it never
// appears on a terminal or in a transcript. It is never logged elsewhere.
//
// Note: the Google Cloud OAuth consent screen must be published to
// production status — apps left in Testing mode get refresh tokens that
// expire after 7 days, which is useless for a long-running sync engine.
func newOAuthCmd() *cli.Command {
	return &cli.Command{
		Name:  "oauth",
		Usage: "Interactive OAuth bootstrap for provider accounts (Google)",
		Description: "Runs the local-loopback OAuth consent flow and obtains a refresh token.\n" +
			"By default the token is printed to stdout (pipe it into your secret store).\n" +
			"With --write-env FILE the token is instead written into FILE as\n" +
			"MERIDIAN_GOOGLE_REFRESH_TOKEN=... without ever being displayed.\n\n" +
			"The OAuth consent screen must be published to Production status in Google Cloud\n" +
			"Console — apps left in Testing mode issue refresh tokens that expire after 7 days,\n" +
			"unworkable for a long-running sync engine.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "client-id",
				Usage:   "OAuth client ID (Desktop-app type)",
				Sources: cli.EnvVars("MERIDIAN_GOOGLE_CLIENT_ID"),
			},
			&cli.StringFlag{
				Name:    "client-secret",
				Usage:   "OAuth client secret",
				Sources: cli.EnvVars("MERIDIAN_GOOGLE_CLIENT_SECRET"),
			},
			&cli.IntFlag{
				Name:  "port",
				Usage: "localhost callback port",
				Value: 5555,
			},
			&cli.StringFlag{
				Name:  "write-env",
				Usage: "write the refresh token into this env file instead of printing it",
			},
			&cli.StringFlag{
				Name:  "env-key",
				Usage: "env key to write with --write-env",
				Value: "MERIDIAN_GOOGLE_REFRESH_TOKEN",
			},
		},
		Action: runOAuth,
	}
}

func runOAuth(ctx context.Context, cmd *cli.Command) error {
	// Dev convenience: pick up client credentials from a local .env; real
	// env vars win and this is a no-op when the file does not exist.
	_ = godotenv.Load()

	clientID := stringFromFlagOrEnv(cmd, "client-id", "MERIDIAN_GOOGLE_CLIENT_ID")
	clientSecret := stringFromFlagOrEnv(cmd, "client-secret", "MERIDIAN_GOOGLE_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return errors.New("oauth: client ID and secret required, via --client-id/--client-secret, MERIDIAN_GOOGLE_CLIENT_ID/MERIDIAN_GOOGLE_CLIENT_SECRET, or a .env carrying those two")
	}

	port := cmd.Int("port")
	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     googleOAuthEndpoint,
		RedirectURL:  fmt.Sprintf("http://localhost:%d/callback", port),
		Scopes:       []string{calendarScope},
	}

	state, err := randomToken()
	if err != nil {
		return err
	}
	verifier := oauth2.GenerateVerifier()
	authURL := conf.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(verifier),
		// Force the consent screen so Google issues a refresh token even
		// when the user has authorized this client before.
		oauth2.SetAuthURLParam("prompt", "consent"),
	)

	code, err := waitForCallback(ctx, port, state, authURL, cmd.Writer)
	if err != nil {
		return err
	}

	tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return fmt.Errorf("oauth: token exchange failed: %w", err)
	}
	if tok.RefreshToken == "" {
		return errors.New("oauth: no refresh token in response (re-run; if it persists, revoke the app at myaccount.google.com/permissions and try again)")
	}

	if envFile := cmd.String("write-env"); envFile != "" {
		key := cmd.String("env-key")
		if err := upsertEnvKey(envFile, key, tok.RefreshToken); err != nil {
			return fmt.Errorf("oauth: writing %s: %w", envFile, err)
		}
		_, _ = fmt.Fprintf(cmd.Writer, "Refresh token written to %s as %s (not displayed).\n", envFile, key)
		return nil
	}
	// Piping mode: token on stdout, nothing else.
	_, _ = fmt.Fprintln(cmd.Writer, tok.RefreshToken)
	return nil
}

// waitForCallback runs the loopback listener, prints the consent URL, and
// returns the authorization code.
func waitForCallback(ctx context.Context, port int, state, authURL string, out io.Writer) (string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return "", fmt.Errorf("oauth: cannot listen on localhost:%d (is another flow running?): %w", port, err)
	}

	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var res result
		switch {
		case q.Get("state") != state:
			http.Error(w, "state mismatch", http.StatusBadRequest)
			res = result{err: errors.New("oauth: state mismatch on callback")}
		case q.Get("error") != "":
			http.Error(w, "authorization declined", http.StatusBadRequest)
			res = result{err: fmt.Errorf("oauth: authorization failed: %s", q.Get("error"))}
		case q.Get("code") == "":
			http.Error(w, "missing code", http.StatusBadRequest)
			res = result{err: errors.New("oauth: callback without code")}
		default:
			_, _ = fmt.Fprintln(w, "meridian: authorization received — you can close this tab.")
			res = result{code: q.Get("code")}
		}
		select {
		case results <- res:
		default: // duplicate callback; first result wins
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln) //nolint:errcheck // always ErrServerClosed after Shutdown

	_, _ = fmt.Fprintf(out, "Open this URL in your browser and authorize access:\n\n%s\n\nWaiting for callback on localhost:%d ...\n", authURL, port)

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	select {
	case r := <-results:
		return r.code, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(10 * time.Minute):
		return "", errors.New("oauth: timed out waiting for authorization")
	}
}

// upsertEnvKey updates key=value in an env file in place, preserving all
// other lines (including comments); appends if the key is absent. The file
// is created 0600 if missing.
func upsertEnvKey(path, key, value string) error {
	line := key + "=" + `"` + value + `"`
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	replaced := false
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, key+"=") || strings.HasPrefix(trimmed, "export "+key+"=") {
			lines[i] = line
			replaced = true
		}
	}
	if !replaced {
		lines = append(lines, line)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func stringFromFlagOrEnv(cmd *cli.Command, flag, envKey string) string {
	if v := cmd.String(flag); v != "" {
		return v
	}
	// After godotenv.Load, .env values are plain env vars; cli Sources were
	// evaluated before the Load, so consult the env again.
	return os.Getenv(envKey)
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
