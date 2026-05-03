package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/freman/icalmerge/config"
	"github.com/freman/icalmerge/source"
)

func AuthAdd(cfg *config.Config, name string) error {
	if cfg.Google.ClientID == "" || cfg.Google.ClientSecret == "" {
		return fmt.Errorf("google.client_id and google.client_secret must be configured")
	}

	if err := os.MkdirAll(cfg.TokenDir(), 0o700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start callback listener: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://localhost:%d/callback", port)

	oauthCfg := source.OAuthConfig(cfg.Google.ClientID, cfg.Google.ClientSecret, redirectURL)
	state := fmt.Sprintf("%d", time.Now().UnixNano())

	authURL := oauthCfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	fmt.Printf("Opening browser for Google auth (%s)...\n", name)
	fmt.Printf("\nIf the browser does not open, visit:\n%s\n\n", authURL)

	openBrowser(authURL)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "invalid state", http.StatusBadRequest)
			errCh <- fmt.Errorf("state mismatch in OAuth callback")

			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- fmt.Errorf("no code in OAuth callback")

			return
		}

		fmt.Fprintln(w, "<html><body><h2>Authorization complete. You may close this tab.</h2></body></html>")
		codeCh <- code
	})

	srv := &http.Server{Handler: mux}

	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var code string
	select {
	case code = <-codeCh:
	case err = <-errCh:
		return fmt.Errorf("oauth callback: %w", err)
	case <-time.After(5 * time.Minute):
		return fmt.Errorf("timeout waiting for OAuth callback")
	}

	_ = srv.Shutdown(context.Background())

	tok, err := oauthCfg.Exchange(context.Background(), code)
	if err != nil {
		return fmt.Errorf("exchange code: %w", err)
	}

	tokenFile := filepath.Join(cfg.TokenDir(), name+".json")
	if err := source.SaveToken(tokenFile, tok); err != nil {
		return err
	}

	fmt.Printf("Token saved for account %q -> %s\n", name, tokenFile)

	return nil
}

func AuthList(cfg *config.Config) error {
	entries, err := os.ReadDir(cfg.TokenDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("No accounts configured.")

			return nil
		}

		return fmt.Errorf("read token dir: %w", err)
	}

	found := false
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		found = true
		name := strings.TrimSuffix(entry.Name(), ".json")
		path := filepath.Join(cfg.TokenDir(), entry.Name())

		expiry := tokenExpiry(path)
		fmt.Printf("  %-20s  %s\n", name, expiry)
	}

	if !found {
		fmt.Println("No accounts configured.")
	}

	return nil
}

func AuthRevoke(cfg *config.Config, name string) error {
	path := filepath.Join(cfg.TokenDir(), name+".json")

	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("account %q not found", name)
		}

		return fmt.Errorf("remove token: %w", err)
	}

	fmt.Printf("Token for %q removed.\n", name)

	return nil
}

func tokenExpiry(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "(unreadable)"
	}
	defer f.Close()

	var tok oauth2.Token
	if err := json.NewDecoder(f).Decode(&tok); err != nil {
		return "(invalid)"
	}

	if tok.Expiry.IsZero() {
		return "no expiry"
	}

	if tok.Expiry.Before(time.Now()) {
		return fmt.Sprintf("expired %s", tok.Expiry.Format("2006-01-02"))
	}

	return fmt.Sprintf("expires %s", tok.Expiry.Format("2006-01-02"))
}
