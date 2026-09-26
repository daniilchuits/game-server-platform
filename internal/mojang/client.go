// Package mojang resolves and downloads official Minecraft server releases.
package mojang

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"game-server-platform/internal/domain"
)

const manifestURL = "https://piston-meta.mojang.com/mc/game/version_manifest.json"

type Client struct {
	http        *http.Client
	manifestURL string
}

func New() *Client {
	return &Client{
		http:        &http.Client{Timeout: 30 * time.Minute},
		manifestURL: manifestURL,
	}
}

type manifestResponse struct {
	Versions []versionEntry `json:"versions"`
}

type versionEntry struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type versionResponse struct {
	Downloads struct {
		Server struct {
			URL  string `json:"url"`
			SHA1 string `json:"sha1"`
			Size int64  `json:"size"`
		} `json:"server"`
	} `json:"downloads"`
}

func (client *Client) Resolve(ctx context.Context, version string) (domain.ServerDownload, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var manifest manifestResponse
	if err := client.getJSON(ctx, client.manifestURL, &manifest); err != nil {
		return domain.ServerDownload{}, fmt.Errorf("fetch version manifest: %w", err)
	}
	var metadataURL string
	for _, entry := range manifest.Versions {
		if entry.ID == version {
			metadataURL = entry.URL
			break
		}
	}
	if metadataURL == "" {
		return domain.ServerDownload{}, fmt.Errorf("version %s was not found in Mojang's manifest", version)
	}
	var metadata versionResponse
	if err := client.getJSON(ctx, metadataURL, &metadata); err != nil {
		return domain.ServerDownload{}, fmt.Errorf("fetch version metadata: %w", err)
	}
	server := metadata.Downloads.Server
	if server.URL == "" {
		return domain.ServerDownload{}, fmt.Errorf("version %s has no server download URL", version)
	}
	digest, err := hex.DecodeString(server.SHA1)
	if err != nil || len(digest) != 20 || server.Size <= 0 {
		return domain.ServerDownload{}, fmt.Errorf("version %s has invalid server download integrity metadata", version)
	}
	return domain.ServerDownload{URL: server.URL, SHA1: server.SHA1, Size: server.Size}, nil
}

func (client *Client) getJSON(ctx context.Context, url string, target any) error {
	response, err := client.get(ctx, url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (client *Client) get(ctx context.Context, url string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("HTTP %s", response.Status)
	}
	return response, nil
}
