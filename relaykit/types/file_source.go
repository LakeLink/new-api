package types

import (
	"fmt"
	"image"
	"net/url"
	"os"
	"strings"
	"sync"
)

// FileSource 统一的文件来源抽象接口
// 支持 URL 和 base64 两种来源，提供懒加载和缓存机制
type FileSource interface {
	IsURL() bool
	GetIdentifier() string
	GetRawData() string
	ClearRawData()

	SetCache(data *CachedFileData)
	GetCache() *CachedFileData
	HasCache() bool
	ClearCache()

	IsRegistered() bool
	SetRegistered(registered bool)
	ClaimCleanupRegistration() bool
	Mu() *sync.Mutex
}

// baseFileSource 共享的缓存/锁/清理注册状态
type baseFileSource struct {
	cachedData  *CachedFileData
	cacheLoaded bool
	registered  bool
	stateMu     sync.RWMutex
	mu          sync.Mutex
}

func (b *baseFileSource) SetCache(data *CachedFileData) {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	b.cachedData = data
	b.cacheLoaded = data != nil
}

func (b *baseFileSource) GetCache() *CachedFileData {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.cachedData
}

func (b *baseFileSource) HasCache() bool {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.cacheLoaded && b.cachedData != nil
}

func (b *baseFileSource) ClearCache() {
	b.stateMu.Lock()
	cachedData := b.cachedData
	b.cachedData = nil
	b.cacheLoaded = false
	b.stateMu.Unlock()

	if cachedData != nil {
		_ = cachedData.Close()
	}
}

func (b *baseFileSource) IsRegistered() bool {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.registered
}

func (b *baseFileSource) SetRegistered(registered bool) {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	b.registered = registered
}

// ClaimCleanupRegistration atomically marks this source as registered for
// request cleanup. It returns false when another caller already registered it.
func (b *baseFileSource) ClaimCleanupRegistration() bool {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	if b.registered {
		return false
	}
	b.registered = true
	return true
}

func (b *baseFileSource) Mu() *sync.Mutex {
	return &b.mu
}

// ---------------------------------------------------------------------------
// URLSource — URL 来源的 FileSource 实现
// ---------------------------------------------------------------------------

type URLSource struct {
	baseFileSource
	URL string
}

func (u *URLSource) IsURL() bool { return true }

func (u *URLSource) GetIdentifier() string {
	parsed, err := url.Parse(u.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "url:[invalid]"
	}
	// Identifiers are used in logs and errors. Paths, user information,
	// fragments, and signed query strings can all contain credentials.
	return "url:" + strings.ToLower(parsed.Scheme) + "://" + parsed.Host
}

func (u *URLSource) GetRawData() string { return u.URL }

func (u *URLSource) ClearRawData() {}

// ---------------------------------------------------------------------------
// Base64Source — Base64 内联数据来源的 FileSource 实现
// ---------------------------------------------------------------------------

type Base64Source struct {
	baseFileSource
	Base64Data string
	MimeType   string
}

func (b *Base64Source) IsURL() bool { return false }

func (b *Base64Source) GetIdentifier() string {
	// Never include inline media bytes in an error or debug log.
	return fmt.Sprintf("base64:[%d encoded bytes]", len(b.Base64Data))
}

func (b *Base64Source) GetRawData() string { return b.Base64Data }

func (b *Base64Source) ClearRawData() {
	if len(b.Base64Data) > 1024 {
		b.Base64Data = ""
	}
}

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

func NewURLFileSource(url string) *URLSource {
	return &URLSource{URL: url}
}

func NewBase64FileSource(base64Data string, mimeType string) *Base64Source {
	return &Base64Source{
		Base64Data: base64Data,
		MimeType:   mimeType,
	}
}

func NewFileSourceFromData(data string, mimeType string) FileSource {
	if strings.HasPrefix(data, "http://") || strings.HasPrefix(data, "https://") {
		return NewURLFileSource(data)
	}
	return NewBase64FileSource(data, mimeType)
}

// ---------------------------------------------------------------------------
// CachedFileData — 缓存的文件数据（支持内存和磁盘两种模式）
// ---------------------------------------------------------------------------

type CachedFileData struct {
	base64Data  string        // 内存中的 base64 数据（小文件）
	MimeType    string        // MIME 类型
	Size        int64         // 文件大小（字节）
	DiskSize    int64         // 磁盘缓存实际占用大小（字节，通常是 base64 长度）
	ImageConfig *image.Config // 图片配置（如果是图片）
	ImageFormat string        // 图片格式（如果是图片）

	diskPath        string     // 磁盘缓存文件路径（大文件）
	isDisk          bool       // 是否使用磁盘缓存
	mu              sync.Mutex // 保护缓存内容与关闭状态
	closed          bool       // 是否已关闭/清理
	statDecremented bool       // 是否已扣减统计

	OnClose func(size int64)
}

func NewMemoryCachedData(base64Data string, mimeType string, size int64) *CachedFileData {
	return &CachedFileData{
		base64Data: base64Data,
		MimeType:   mimeType,
		Size:       size,
		isDisk:     false,
	}
}

func NewDiskCachedData(diskPath string, mimeType string, size int64) *CachedFileData {
	return &CachedFileData{
		diskPath: diskPath,
		MimeType: mimeType,
		Size:     size,
		isDisk:   true,
	}
}

func (c *CachedFileData) GetBase64Data() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return "", fmt.Errorf("file cache already closed")
	}
	if !c.isDisk {
		return c.base64Data, nil
	}

	data, err := os.ReadFile(c.diskPath)
	if err != nil {
		return "", fmt.Errorf("failed to read from disk cache: %w", err)
	}
	return string(data), nil
}

func (c *CachedFileData) SetBase64Data(data string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.isDisk && !c.closed {
		c.base64Data = data
	}
}

func (c *CachedFileData) IsDisk() bool {
	return c.isDisk
}

func (c *CachedFileData) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *CachedFileData) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	if !c.isDisk {
		c.base64Data = ""
		return nil
	}

	if c.diskPath != "" {
		err := os.Remove(c.diskPath)
		if err == nil && !c.statDecremented && c.OnClose != nil {
			c.OnClose(c.DiskSize)
			c.statDecremented = true
		}
		return err
	}
	return nil
}
