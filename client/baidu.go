package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type FileItem struct {
	FsID           int64  `json:"fs_id"`
	Path           string `json:"path"`
	ServerFilename string `json:"server_filename"`
	Size           int64  `json:"size"`
	ServerMtime    int64  `json:"server_mtime"`
	LocalMtime     int64  `json:"local_mtime"`
	Md5            string `json:"md5"`
	IsDir          int    `json:"isdir"`
}

type listResponse struct {
	Errno int        `json:"errno"`
	List  []FileItem `json:"list"`
}

type listAllResponse struct {
	Errno int        `json:"errno"`
	List  []FileItem `json:"list"`
}

type renewResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Text         string `json:"text"`
}

type TokenUpdateCallback func(newAccessToken, newRefreshToken string)

type BaiduClient struct {
	mu           sync.RWMutex
	accessToken  string
	refreshToken string
	httpClient   *http.Client
	onTokenRenew TokenUpdateCallback
}

func NewBaiduClient(accessToken, refreshToken string, onTokenRenew TokenUpdateCallback) *BaiduClient {
	return &BaiduClient{
		accessToken:  accessToken,
		refreshToken: refreshToken,
		httpClient: &http.Client{
			Timeout: 10 * time.Minute,
		},
		onTokenRenew: onTokenRenew,
	}
}

func (c *BaiduClient) GetAccessToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.accessToken
}

func (c *BaiduClient) GetRefreshToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.refreshToken
}

func (c *BaiduClient) SetTokens(access, refresh string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accessToken = access
	c.refreshToken = refresh
}

// RefreshToken 借助 oplist renewapi 自动根据 refresh_token 刷新 access_token
func (c *BaiduClient) RefreshToken() error {
	c.mu.Lock()
	rf := c.refreshToken
	c.mu.Unlock()

	if rf == "" {
		return fmt.Errorf("未设置 BAIDU_REFRESH_TOKEN，无法自动刷新凭证")
	}

	renewURL := fmt.Sprintf(
		"https://api.oplist.org/baiduyun/renewapi?refresh_ui=%s&server_use=true&driver_txt=baiduyun_go",
		url.QueryEscape(rf),
	)

	req, err := http.NewRequest("GET", renewURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求刷新 token 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取刷新响应失败: %w", err)
	}

	var res renewResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return fmt.Errorf("解析刷新响应失败: %w, 响应: %s", err, string(body))
	}

	if res.AccessToken == "" {
		errMsg := res.Text
		if errMsg == "" {
			errMsg = string(body)
		}
		return fmt.Errorf("刷新 token 失败: %s，可能 refresh_token 已过期或无效", errMsg)
	}

	c.SetTokens(res.AccessToken, res.RefreshToken)

	if c.onTokenRenew != nil {
		c.onTokenRenew(res.AccessToken, res.RefreshToken)
	}

	return nil
}

// ListAllFast 利用官方 listall 递归接口，单次请求极速获取整棵树（数百倍性能提升）
func (c *BaiduClient) ListAllFast(remoteRoot string) (map[string]FileItem, error) {
	token := c.GetAccessToken()
	if token == "" {
		if err := c.RefreshToken(); err != nil {
			return nil, err
		}
		token = c.GetAccessToken()
	}

	reqURL := fmt.Sprintf(
		"https://pan.baidu.com/rest/2.0/xpan/file?method=listall&path=%s&access_token=%s&recursion=1&web=1",
		url.QueryEscape(remoteRoot),
		url.QueryEscape(token),
	)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pan.baidu.com")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if err := c.RefreshToken(); err == nil {
			return c.ListAllFast(remoteRoot)
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var lr listAllResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, err
	}

	if lr.Errno == -6 || lr.Errno == 111 {
		if err := c.RefreshToken(); err != nil {
			return nil, err
		}
		return c.ListAllFast(remoteRoot)
	}

	if lr.Errno != 0 && lr.Errno != 12 { // errno 12 是空目录
		return nil, fmt.Errorf("errno %d", lr.Errno)
	}

	result := make(map[string]FileItem, len(lr.List))
	for _, item := range lr.List {
		if item.IsDir == 1 {
			continue
		}
		relPath := item.Path
		if len(relPath) > len(remoteRoot) {
			relPath = relPath[len(remoteRoot):]
			if len(relPath) > 0 && relPath[0] == '/' {
				relPath = relPath[1:]
			}
		}
		result[relPath] = item
	}

	return result, nil
}

// ListDir 列出指定目录下的文件与子文件夹
func (c *BaiduClient) ListDir(remoteDir string) ([]FileItem, error) {
	var allItems []FileItem
	page := 1
	num := 1000

	for {
		token := c.GetAccessToken()
		if token == "" {
			if err := c.RefreshToken(); err != nil {
				return nil, err
			}
			token = c.GetAccessToken()
		}

		reqURL := fmt.Sprintf(
			"https://pan.baidu.com/rest/2.0/xpan/file?method=list&dir=%s&access_token=%s&page=%d&num=%d&order=name&desc=0",
			url.QueryEscape(remoteDir),
			url.QueryEscape(token),
			page,
			num,
		)

		req, err := http.NewRequest("GET", reqURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "pan.baidu.com")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("网络请求失败: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}

		var lr listResponse
		if err := json.Unmarshal(body, &lr); err != nil {
			return nil, fmt.Errorf("解析响应失败: %w", err)
		}

		if lr.Errno == -6 || lr.Errno == 111 {
			if err := c.RefreshToken(); err != nil {
				return nil, fmt.Errorf("Token 失效且刷新失败: %w", err)
			}
			continue
		}

		if lr.Errno != 0 {
			return nil, fmt.Errorf("百度网盘 API 报错 errno: %d", lr.Errno)
		}

		allItems = append(allItems, lr.List...)
		if len(lr.List) < num {
			break
		}
		page++
	}

	return allItems, nil
}

// ListRecursive 深度扫描远端文件树（优先极速 listall，失败自动降级为 BFS 逐层扫描）
func (c *BaiduClient) ListRecursive(remoteRoot string, onProgress func(dirsScanned, filesFound int)) (map[string]FileItem, error) {
	log.Println("[扫描优化] 正在尝试调用百度网盘 listall 全量极速索引接口...")
	fastStart := time.Now()
	items, err := c.ListAllFast(remoteRoot)
	if err == nil {
		log.Printf("[扫描优化] 极速索引成功！耗时: %v，直接秒级获取 %d 个文件", time.Since(fastStart).Round(time.Millisecond), len(items))
		return items, nil
	}

	log.Printf("[扫描] 极速索引不可用 (%v)，平滑降级为常规逐级深度遍历...", err)
	result := make(map[string]FileItem)
	dirQueue := []string{remoteRoot}
	dirsScanned := 0

	for len(dirQueue) > 0 {
		currDir := dirQueue[0]
		dirQueue = dirQueue[1:]
		dirsScanned++

		items, err := c.ListDir(currDir)
		if err != nil {
			return nil, fmt.Errorf("遍历目录 %s 失败: %w", currDir, err)
		}

		for _, item := range items {
			if item.IsDir == 1 {
				dirQueue = append(dirQueue, item.Path)
			} else {
				relPath := item.Path
				if len(relPath) > len(remoteRoot) {
					relPath = relPath[len(remoteRoot):]
					if len(relPath) > 0 && relPath[0] == '/' {
						relPath = relPath[1:]
					}
				}
				result[relPath] = item
			}
		}

		if onProgress != nil {
			onProgress(dirsScanned, len(result))
		}
	}

	return result, nil
}

// DownloadFile 通过 PCS 接口流式下载，支持断点续传与失败自动重试
func (c *BaiduClient) DownloadFile(remotePath, localTarget string, expectedSize int64) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		lastErr = c.downloadFileOnce(remotePath, localTarget, expectedSize)
		if lastErr == nil {
			return nil
		}
		log.Printf("[重试] 下载 %s 第 %d 次失败: %v, 稍候重试...", filepath.Base(localTarget), attempt, lastErr)
		time.Sleep(time.Duration(attempt) * time.Second)
	}
	return lastErr
}

func (c *BaiduClient) downloadFileOnce(remotePath, localTarget string, expectedSize int64) error {
	token := c.GetAccessToken()
	if token == "" {
		if err := c.RefreshToken(); err != nil {
			return err
		}
		token = c.GetAccessToken()
	}

	reqURL := fmt.Sprintf(
		"https://d.pcs.baidu.com/rest/2.0/pcs/file?method=download&path=%s&access_token=%s&app_id=250528",
		url.QueryEscape(remotePath),
		url.QueryEscape(token),
	)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "pan.baidu.com")

	if err := os.MkdirAll(filepath.Dir(localTarget), 0755); err != nil {
		return err
	}

	tmpTarget := localTarget + ".bdsync.tmp"
	var existingSize int64 = 0
	if fi, err := os.Stat(tmpTarget); err == nil && expectedSize > 0 && fi.Size() < expectedSize {
		existingSize = fi.Size()
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("下载网络请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if err := c.RefreshToken(); err == nil {
			return c.downloadFileOnce(remotePath, localTarget, expectedSize)
		}
	}

	var out *os.File
	if resp.StatusCode == http.StatusPartialContent && existingSize > 0 {
		// 断点追加写入
		out, err = os.OpenFile(tmpTarget, os.O_WRONLY|os.O_APPEND, 0644)
	} else if resp.StatusCode == http.StatusOK {
		// 全量重新写入
		out, err = os.Create(tmpTarget)
	} else {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("下载失败 HTTP %d: %s", resp.StatusCode, string(body))
	}

	if err != nil {
		return err
	}

	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		return fmt.Errorf("写入文件中断: %w", err)
	}

	// 原子替换
	if err := os.Rename(tmpTarget, localTarget); err != nil {
		_ = os.Remove(localTarget)
		return os.Rename(tmpTarget, localTarget)
	}

	return nil
}

// UploadFile 通过 PCS 专属接口上传文件，支持重试
func (c *BaiduClient) UploadFile(localPath, remotePath string) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		lastErr = c.uploadFileOnce(localPath, remotePath)
		if lastErr == nil {
			return nil
		}
		log.Printf("[重试] 上传 %s 第 %d 次失败: %v, 稍候重试...", filepath.Base(localPath), attempt, lastErr)
		time.Sleep(time.Duration(attempt) * time.Second)
	}
	return lastErr
}

func (c *BaiduClient) uploadFileOnce(localPath, remotePath string) error {
	token := c.GetAccessToken()
	if token == "" {
		if err := c.RefreshToken(); err != nil {
			return err
		}
		token = c.GetAccessToken()
	}

	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	bodyBuf := &bytes.Buffer{}
	writer := multipart.NewWriter(bodyBuf)
	part, err := writer.CreateFormFile("file", filepath.Base(localPath))
	if err != nil {
		return err
	}

	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	writer.Close()

	reqURL := fmt.Sprintf(
		"https://d.pcs.baidu.com/rest/2.0/pcs/file?method=upload&path=%s&access_token=%s&ondup=overwrite&app_id=250528",
		url.QueryEscape(remotePath),
		url.QueryEscape(token),
	)

	req, err := http.NewRequest("POST", reqURL, bodyBuf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "pan.baidu.com")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("上传网络请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if err := c.RefreshToken(); err == nil {
			return c.uploadFileOnce(localPath, remotePath)
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("上传失败 HTTP %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// DeleteFile 在百度网盘中删除文件或文件夹
func (c *BaiduClient) DeleteFile(remotePath string) error {
	token := c.GetAccessToken()
	if token == "" {
		if err := c.RefreshToken(); err != nil {
			return err
		}
		token = c.GetAccessToken()
	}

	fileListJSON, _ := json.Marshal([]string{remotePath})
	reqURL := fmt.Sprintf(
		"https://pan.baidu.com/rest/2.0/xpan/file?method=filemanager&opera=delete&access_token=%s",
		url.QueryEscape(token),
	)

	data := url.Values{}
	data.Set("async", "0")
	data.Set("filelist", string(fileListJSON))

	req, err := http.NewRequest("POST", reqURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "pan.baidu.com")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("删除请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var res struct {
		Errno int `json:"errno"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return err
	}

	if res.Errno != 0 {
		return fmt.Errorf("删除失败 errno: %d, response: %s", res.Errno, string(body))
	}

	return nil
}
