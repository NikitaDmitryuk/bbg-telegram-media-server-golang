package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const apiPrefix = "/api/v2"

type HTTPStatusError struct {
	Operation string
	Status    int
}

// Client talks to qBittorrent Web API (v2).
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("qBittorrent: %s failed status=%d", e.Operation, e.Status)
}

func isAuthenticationStatus(err error) bool {
	var statusErr *HTTPStatusError
	return errors.As(err, &statusErr) &&
		(statusErr.Status == http.StatusUnauthorized || statusErr.Status == http.StatusForbidden)
}

func (c *Client) retryAfterAuthentication(ctx context.Context, call func() error) error {
	err := call()
	if !isAuthenticationStatus(err) {
		return err
	}
	if loginErr := c.Login(ctx); loginErr != nil {
		return fmt.Errorf("qBittorrent reauthentication: %w", loginErr)
	}
	return call()
}

// NewClient builds a client. baseURL is the Web UI root, e.g. "http://localhost:8080".
func NewClient(baseURL, username, password string) (*Client, error) {
	baseURL = strings.TrimSuffix(baseURL, "/")
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("qBittorrent: invalid base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("qBittorrent: invalid base URL scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("qBittorrent: base URL must include host")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURL:  baseURL,
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Jar:     jar,
		},
	}, nil
}

// Login authenticates and stores the session cookie.
func (c *Client) Login(ctx context.Context) error {
	u := c.baseURL + apiPrefix + "/auth/login"
	form := url.Values{}
	form.Set("username", c.username)
	form.Set("password", c.password)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", c.baseURL+"/")
	// #nosec G704 -- baseURL is from config (QBittorrentURL), not user input
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("qBittorrent: login forbidden (IP banned or too many attempts)")
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return &HTTPStatusError{Operation: "login", Status: resp.StatusCode}
	}
	// qBittorrent returns 200 OK with body "Fails." when credentials are wrong (no SID cookie is set).
	if strings.TrimSpace(string(body)) == "Fails." {
		return fmt.Errorf("qBittorrent: login failed (invalid credentials)")
	}
	return nil
}

// AppVersion verifies that the authenticated Web API session can call qBittorrent.
func (c *Client) AppVersion(ctx context.Context) (string, error) {
	u := c.baseURL + apiPrefix + "/app/version"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("Referer", c.baseURL+"/")
	// #nosec G704 -- baseURL is from config
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", &HTTPStatusError{Operation: "app/version", Status: resp.StatusCode}
	}
	return strings.TrimSpace(string(body)), nil
}

// CheckLogin logs in and confirms the session by reading /api/v2/app/version.
func (c *Client) CheckLogin(ctx context.Context) (string, error) {
	if err := c.Login(ctx); err != nil {
		return "", err
	}
	version, err := c.AppVersion(ctx)
	if err != nil {
		return "", err
	}
	return version, nil
}

// AddTorrentOptions controls optional parameters for torrent add API.
type AddTorrentOptions struct {
	SequentialDownload bool
	FirstLastPiecePrio bool
}

// AddTorrentFromURLs adds a torrent from magnet or .torrent URL. savepath is the download directory.
func (c *Client) AddTorrentFromURLs(ctx context.Context, urls, savepath string, opts *AddTorrentOptions) error {
	return c.retryAfterAuthentication(ctx, func() error {
		return c.addTorrentFromURLs(ctx, urls, savepath, opts)
	})
}

func (c *Client) addTorrentFromURLs(ctx context.Context, urls, savepath string, opts *AddTorrentOptions) error {
	u := c.baseURL + apiPrefix + "/torrents/add"
	form := url.Values{}
	form.Set("urls", urls)
	form.Set("savepath", savepath)
	if opts != nil {
		if opts.SequentialDownload {
			form.Set("sequentialDownload", "true")
		}
		if opts.FirstLastPiecePrio {
			form.Set("firstLastPiecePrio", "true")
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", c.baseURL+"/")
	// #nosec G704 -- baseURL from config
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &HTTPStatusError{Operation: "add urls", Status: resp.StatusCode}
	}
	return nil
}

// AddTorrentFromFile uploads a .torrent file. savepath is the download directory.
func (c *Client) AddTorrentFromFile(
	ctx context.Context,
	filename string,
	torrentBody []byte,
	savepath string,
	opts *AddTorrentOptions,
) error {
	return c.retryAfterAuthentication(ctx, func() error {
		return c.addTorrentFromFile(ctx, filename, torrentBody, savepath, opts)
	})
}

func (c *Client) addTorrentFromFile(
	ctx context.Context,
	filename string,
	torrentBody []byte,
	savepath string,
	opts *AddTorrentOptions,
) error {
	u := c.baseURL + apiPrefix + "/torrents/add"
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("torrents", filename)
	if err != nil {
		return err
	}
	if _, writeErr := part.Write(torrentBody); writeErr != nil {
		return writeErr
	}
	_ = w.WriteField("savepath", savepath)
	if opts != nil {
		if opts.SequentialDownload {
			_ = w.WriteField("sequentialDownload", "true")
		}
		if opts.FirstLastPiecePrio {
			_ = w.WriteField("firstLastPiecePrio", "true")
		}
	}
	if closeErr := w.Close(); closeErr != nil {
		return closeErr
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Referer", c.baseURL+"/")
	// #nosec G704 -- baseURL from config
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &HTTPStatusError{Operation: "add file", Status: resp.StatusCode}
	}
	return nil
}

// TorrentInfo is one entry from /torrents/info.
type TorrentInfo struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	Progress    float64 `json:"progress"`
	State       string  `json:"state"`
	Size        int64   `json:"size"`
	TotalSize   int64   `json:"total_size"`
	Downloaded  int64   `json:"downloaded"`
	AmountLeft  int64   `json:"amount_left"` // bytes left to download
	Completed   int64   `json:"completed"`   // bytes completed (transfer)
	AddedOn     int64   `json:"added_on"`
	SavePath    string  `json:"save_path"`
	ContentPath string  `json:"content_path"`
}

// TorrentsInfo returns torrent list. sortOrder: "asc" or "desc". sortBy: e.g. "added_on".
func (c *Client) TorrentsInfo(ctx context.Context, hashes, sort string, reverse bool) ([]TorrentInfo, error) {
	list, err := c.torrentsInfo(ctx, hashes, sort, reverse)
	if !isAuthenticationStatus(err) {
		return list, err
	}
	if loginErr := c.Login(ctx); loginErr != nil {
		return nil, fmt.Errorf("qBittorrent reauthentication: %w", loginErr)
	}
	return c.torrentsInfo(ctx, hashes, sort, reverse)
}

func (c *Client) torrentsInfo(ctx context.Context, hashes, sort string, reverse bool) ([]TorrentInfo, error) {
	u := c.baseURL + apiPrefix + "/torrents/info"
	if hashes != "" || sort != "" {
		params := url.Values{}
		if hashes != "" {
			params.Set("hashes", hashes)
		}
		if sort != "" {
			params.Set("sort", sort)
			if reverse {
				params.Set("reverse", "true")
			}
		}
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", c.baseURL+"/")
	// #nosec G704 -- baseURL from config
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{Operation: "torrents/info", Status: resp.StatusCode}
	}
	var list []TorrentInfo
	if decErr := json.NewDecoder(resp.Body).Decode(&list); decErr != nil {
		return nil, decErr
	}
	return list, nil
}

// TorrentFileInfo is one entry from /torrents/files.
type TorrentFileInfo struct {
	Index    int     `json:"index"`
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	Progress float64 `json:"progress"`
	Priority int     `json:"priority"`
}

// TorrentFiles returns the file list for a torrent.
func (c *Client) TorrentFiles(ctx context.Context, hash string) ([]TorrentFileInfo, error) {
	var files []TorrentFileInfo
	err := c.retryAfterAuthentication(ctx, func() error {
		var err error
		files, err = c.torrentFiles(ctx, hash)
		return err
	})
	return files, err
}

func (c *Client) torrentFiles(ctx context.Context, hash string) ([]TorrentFileInfo, error) {
	u := c.baseURL + apiPrefix + "/torrents/files"
	params := url.Values{}
	params.Set("hash", hash)
	u += "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", c.baseURL+"/")
	// #nosec G704 -- baseURL from config
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{Operation: "torrents/files", Status: resp.StatusCode}
	}
	var files []TorrentFileInfo
	if decErr := json.NewDecoder(resp.Body).Decode(&files); decErr != nil {
		return nil, decErr
	}
	for i := range files {
		files[i].Index = i
	}
	return files, nil
}

// SetFilePriority sets download priority for specific files. ids is pipe-separated 0-based indices (e.g. "0|1|3").
// Priority values: 0=skip, 1=normal, 6=high, 7=maximal.
func (c *Client) SetFilePriority(ctx context.Context, hash, ids string, priority int) error {
	return c.retryAfterAuthentication(ctx, func() error {
		return c.setFilePriority(ctx, hash, ids, priority)
	})
}

func (c *Client) setFilePriority(ctx context.Context, hash, ids string, priority int) error {
	u := c.baseURL + apiPrefix + "/torrents/filePrio"
	form := url.Values{}
	form.Set("hash", hash)
	form.Set("id", ids)
	form.Set("priority", fmt.Sprintf("%d", priority))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", c.baseURL+"/")
	// #nosec G704 -- baseURL from config
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &HTTPStatusError{Operation: "filePrio", Status: resp.StatusCode}
	}
	return nil
}

// DeleteTorrent removes the torrent. deleteFiles: if true, deletes downloaded data.
func (c *Client) DeleteTorrent(ctx context.Context, hash string, deleteFiles bool) error {
	return c.retryAfterAuthentication(ctx, func() error {
		return c.deleteTorrent(ctx, hash, deleteFiles)
	})
}

func (c *Client) deleteTorrent(ctx context.Context, hash string, deleteFiles bool) error {
	u := c.baseURL + apiPrefix + "/torrents/delete"
	form := url.Values{}
	form.Set("hashes", hash)
	if deleteFiles {
		form.Set("deleteFiles", "true")
	} else {
		form.Set("deleteFiles", "false")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", c.baseURL+"/")
	// #nosec G704 -- baseURL from config
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &HTTPStatusError{Operation: "delete", Status: resp.StatusCode}
	}
	return nil
}
