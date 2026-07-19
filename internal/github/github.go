package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

type Client struct {
	Org     string
	Token   string
	HTTP    *http.Client
	BaseURL string
}

func NewClient() *Client {
	token := os.Getenv("REGISTRY_GH_TOKEN")
	org := os.Getenv("REGISTRY_GH_ORG")
	if org == "" {
		org = "CognitiveOS-CGP-Packages"
	}
	return &Client{
		Org:     org,
		Token:   token,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
		BaseURL: "https://api.github.com",
	}
}

func (c *Client) Enabled() bool {
	return c.Token != ""
}

func (c *Client) repoExists(repo string) (bool, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/repos/%s/%s", c.BaseURL, c.Org, repo), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return true, nil
	}
	if resp.StatusCode == 404 {
		return false, nil
	}
	return false, fmt.Errorf("GitHub API %d", resp.StatusCode)
}

func (c *Client) createRepo(name, description string) error {
	payload := map[string]interface{}{
		"name":        name,
		"description": description,
		"private":     false,
		"auto_init":   false,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/orgs/%s/repos", c.BaseURL, c.Org), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 201 {
		log.Printf("GitHub: created repo %s/%s", c.Org, name)
		return nil
	}
	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("create repo: %s", string(respBody))
}

type Release struct {
	ID   int64  `json:"id"`
	Tag  string `json:"tag_name"`
	Name string `json:"name"`
}

func (c *Client) createRelease(repo, tag, name, body string) (*Release, error) {
	payload := map[string]interface{}{
		"tag_name":   tag,
		"name":       name,
		"body":       body,
		"draft":      false,
		"prerelease": false,
	}
	reqBody, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/repos/%s/%s/releases", c.BaseURL, c.Org, repo), bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create release: %s", string(respBody))
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	log.Printf("GitHub: created release %s/%s tag=%s", c.Org, repo, tag)
	return &release, nil
}

func (c *Client) uploadAsset(repo string, releaseID int64, filename string, data []byte) (string, error) {
	uploadsBase := "https://uploads.github.com"
	if c.BaseURL != "https://api.github.com" {
		uploadsBase = c.BaseURL
	}
	uploadURL := fmt.Sprintf("%s/repos/%s/%s/releases/%d/assets?name=%s",
		uploadsBase, c.Org, repo, releaseID, filename)

	req, err := http.NewRequest("POST", uploadURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload asset: %s", string(respBody))
	}

	var asset struct {
		BrowserDownloadURL string `json:"browser_download_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&asset); err != nil {
		return "", err
	}
	log.Printf("GitHub: uploaded %s to %s/%s release %d", filename, c.Org, repo, releaseID)
	return asset.BrowserDownloadURL, nil
}

type PublishResult struct {
	DownloadURL string
	ReleaseTag  string
}

func (c *Client) PublishPackage(name, version, description string, cgpData []byte) (*PublishResult, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("GitHub integration not configured (REGISTRY_GH_TOKEN not set)")
	}

	exists, err := c.repoExists(name)
	if err != nil {
		return nil, fmt.Errorf("check repo: %w", err)
	}
	if !exists {
		if err := c.createRepo(name, description); err != nil {
			return nil, fmt.Errorf("create repo: %w", err)
		}
	}

	tag := "v" + version
	releaseName := fmt.Sprintf("%s v%s", name, version)
	release, err := c.createRelease(name, tag, releaseName, fmt.Sprintf("Release %s version %s", name, version))
	if err != nil {
		return nil, fmt.Errorf("create release: %w", err)
	}

	assetName := fmt.Sprintf("%s-%s.cgp", name, version)
	downloadURL, err := c.uploadAsset(name, release.ID, assetName, cgpData)
	if err != nil {
		return nil, fmt.Errorf("upload asset: %w", err)
	}

	return &PublishResult{
		DownloadURL: downloadURL,
		ReleaseTag:  tag,
	}, nil
}
