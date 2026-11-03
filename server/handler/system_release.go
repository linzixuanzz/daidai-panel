package handler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"daidai-panel/service"
)

const latestPanelReleaseAPI = "https://api.github.com/repos/linzixuanzz/daidai-panel/releases/latest"

type panelReleaseInfo struct {
	TagName     string              `json:"tag_name"`
	Name        string              `json:"name"`
	Body        string              `json:"body"`
	HTMLURL     string              `json:"html_url"`
	PublishedAt string              `json:"published_at"`
	Assets      []panelReleaseAsset `json:"assets"`
}

type panelReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	// Digest 是 GitHub 为每个 Release 附件算好的校验值，形如 "sha256:<hex>"。
	// 下载更新包后用它核对内容，防止下载链路（含用户配置的加速代理）返回被篡改的程序。
	Digest string `json:"digest"`
}

func (r panelReleaseInfo) version() string {
	return strings.TrimPrefix(strings.TrimSpace(r.TagName), "v")
}

func (r panelReleaseInfo) findAsset(name string) (panelReleaseAsset, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return panelReleaseAsset{}, false
	}

	for _, asset := range r.Assets {
		if strings.EqualFold(strings.TrimSpace(asset.Name), name) {
			return asset, true
		}
	}
	return panelReleaseAsset{}, false
}

func fetchLatestPanelRelease() (*panelReleaseInfo, error) {
	client := service.NewHTTPClient(15 * time.Second)
	resp, err := client.Get(latestPanelReleaseAPI)
	if err != nil {
		return nil, fmt.Errorf("检查更新失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API 返回异常状态")
	}

	var release panelReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("解析 GitHub 响应失败")
	}

	return &release, nil
}
