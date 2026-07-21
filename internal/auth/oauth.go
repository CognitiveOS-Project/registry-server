package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	githubAuthURL  = "https://github.com/login/oauth/authorize"
	githubTokenURL = "https://github.com/login/oauth/access_token"
	githubUserURL  = "https://api.github.com/user"
)

type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

type GitHubOAuth struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
}

func NewGitHubOAuth(clientID, clientSecret, redirectURL string) *GitHubOAuth {
	return &GitHubOAuth{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (g *GitHubOAuth) AuthURL(state string) string {
	return fmt.Sprintf("%s?client_id=%s&redirect_uri=%s&scope=%s&state=%s",
		githubAuthURL,
		url.QueryEscape(g.ClientID),
		url.QueryEscape(g.RedirectURL),
		url.QueryEscape("read:user"),
		url.QueryEscape(state),
	)
}

func (g *GitHubOAuth) ExchangeCode(ctx context.Context, code string) (string, error) {
	data := url.Values{
		"client_id":     {g.ClientID},
		"client_secret": {g.ClientSecret},
		"code":          {code},
		"redirect_uri":  {g.RedirectURL},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", githubTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	if result.Error != "" {
		return "", fmt.Errorf("github oauth error: %s — %s", result.Error, result.ErrorDesc)
	}

	if result.AccessToken == "" {
		return "", fmt.Errorf("no access token in response")
	}

	return result.AccessToken, nil
}

func (g *GitHubOAuth) FetchUser(ctx context.Context, accessToken string) (*GitHubUser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", githubUserURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("github user API returned %d: %s", resp.StatusCode, string(body))
	}

	var user GitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decode user: %w", err)
	}

	return &user, nil
}
