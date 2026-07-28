package service

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"golang.org/x/image/webp"
)

// return image.Config, format, clean base64 string, error
func DecodeBase64ImageData(base64String string) (image.Config, string, string, error) {
	// 去除base64数据的URL前缀（如果有）
	if idx := strings.Index(base64String, ","); idx != -1 {
		base64String = base64String[idx+1:]
	}

	if len(base64String) == 0 {
		return image.Config{}, "", "", errors.New("base64 string is empty")
	}

	maxImageBytes := common.BytesFromMegabytes(constant.MaxFileDownloadMB)
	if maxImageBytes <= 0 {
		maxImageBytes = 64 << 20
	}

	// Stream through a bounded decoder so an oversized inline image cannot
	// allocate its full decoded representation before the file limit is
	// enforced.
	decodedData, err := io.ReadAll(io.LimitReader(
		base64.NewDecoder(base64.StdEncoding, strings.NewReader(base64String)),
		common.ReadLimitWithOverrunByte(maxImageBytes),
	))
	if err != nil {
		return image.Config{}, "", "", fmt.Errorf("failed to decode base64 string: %s", err.Error())
	}
	if int64(len(decodedData)) > maxImageBytes {
		return image.Config{}, "", "", fmt.Errorf("image size exceeds maximum allowed size of %d bytes", maxImageBytes)
	}

	// 创建一个bytes.Buffer用于存储解码后的数据
	reader := bytes.NewReader(decodedData)
	config, format, err := getImageConfig(reader)
	return config, format, base64String, err
}

func DecodeBase64FileData(base64String string) (string, string, error) {
	var mimeType string
	var idx int
	idx = strings.Index(base64String, ",")
	if idx == -1 {
		_, file_type, base64, err := DecodeBase64ImageData(base64String)
		return "image/" + file_type, base64, err
	}
	mimeType = base64String[:idx]
	base64String = base64String[idx+1:]
	idx = strings.Index(mimeType, ";")
	if idx == -1 {
		_, file_type, base64, err := DecodeBase64ImageData(base64String)
		return "image/" + file_type, base64, err
	}
	mimeType = mimeType[:idx]
	idx = strings.Index(mimeType, ":")
	if idx == -1 {
		_, file_type, base64, err := DecodeBase64ImageData(base64String)
		return "image/" + file_type, base64, err
	}
	mimeType = mimeType[idx+1:]
	return mimeType, base64String, nil
}

// GetImageFromUrl 获取图片的类型和base64编码的数据
func GetImageFromUrl(url string) (mimeType string, data string, err error) {
	resp, err := DoDownloadRequest(url)
	if err != nil {
		return "", "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	// Check HTTP status code
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("failed to download image: HTTP %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/octet-stream" && !strings.HasPrefix(contentType, "image/") {
		return "", "", fmt.Errorf("invalid content type: %s, required image/*", contentType)
	}
	maxImageSize := common.BytesFromMegabytes(constant.MaxFileDownloadMB)
	if maxImageSize <= 0 {
		maxImageSize = 64 << 20
	}

	// Check Content-Length if available
	if resp.ContentLength > maxImageSize {
		return "", "", fmt.Errorf("image size %d exceeds maximum allowed size of %d bytes", resp.ContentLength, maxImageSize)
	}

	// Use LimitReader to prevent reading oversized images
	limitReader := io.LimitReader(resp.Body, common.ReadLimitWithOverrunByte(maxImageSize))
	buffer := &bytes.Buffer{}

	written, err := io.Copy(buffer, limitReader)
	if err != nil {
		return "", "", fmt.Errorf("failed to read image data: %w", err)
	}
	if written > maxImageSize {
		return "", "", fmt.Errorf("image size exceeds maximum allowed size of %d bytes", maxImageSize)
	}

	data = base64.StdEncoding.EncodeToString(buffer.Bytes())
	mimeType = contentType

	// Handle application/octet-stream type
	if mimeType == "application/octet-stream" {
		_, format, _, err := DecodeBase64ImageData(data)
		if err != nil {
			return "", "", err
		}
		mimeType = "image/" + format
	}

	return mimeType, data, nil
}

func DecodeUrlImageData(imageUrl string) (image.Config, string, error) {
	response, err := DoDownloadRequest(imageUrl)
	if err != nil {
		common.SysLog(fmt.Sprintf("fail to get image from url: %s", err.Error()))
		return image.Config{}, "", err
	}
	defer response.Body.Close()

	if response.StatusCode != 200 {
		err = errors.New(fmt.Sprintf("fail to get image from url: %s", response.Status))
		return image.Config{}, "", err
	}

	mimeType := response.Header.Get("Content-Type")

	if mimeType != "application/octet-stream" && !strings.HasPrefix(mimeType, "image/") {
		return image.Config{}, "", fmt.Errorf("invalid content type: %s, required image/*", mimeType)
	}

	var readData []byte
	for _, limit := range []int64{1024 * 8, 1024 * 24, 1024 * 64} {
		common.SysLog(fmt.Sprintf("try to decode image config with limit: %d", limit))

		// 从response.Body读取更多的数据直到达到当前的限制
		additionalData := make([]byte, limit-int64(len(readData)))
		n, readErr := io.ReadFull(response.Body, additionalData)
		readData = append(readData, additionalData[:n]...)

		var config image.Config
		var format string
		config, format, err = getImageConfig(bytes.NewReader(readData))
		if err == nil {
			return config, format, nil
		}
		if readErr != nil {
			break
		}
	}

	return image.Config{}, "", err // 返回最后一个错误
}

func getImageConfig(reader io.Reader) (image.Config, string, error) {
	// Read all data so we can retry with different decoders
	data, readErr := io.ReadAll(reader)
	if readErr != nil {
		return image.Config{}, "", fmt.Errorf("failed to read image data: %w", readErr)
	}

	// 读取图片的头部信息来获取图片尺寸
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil {
		return config, format, nil
	}
	common.SysLog(fmt.Sprintf("fail to decode image config(gif, jpg, png): %s", err.Error()))

	config, err = webp.DecodeConfig(bytes.NewReader(data))
	if err == nil {
		return config, "webp", nil
	}
	common.SysLog(fmt.Sprintf("fail to decode image config(webp): %s", err.Error()))

	// Try HEIF/HEIC: parse ISOBMFF ispe box for dimensions
	if heifMime := detectHEIF(data); heifMime != "" {
		formatName := "heif"
		if heifMime == "image/heic" {
			formatName = "heic"
		}
		if w, h, ok := parseHEIFDimensions(data); ok {
			return image.Config{Width: w, Height: h}, formatName, nil
		}
		return image.Config{}, "", fmt.Errorf("failed to decode HEIF/HEIC image dimensions")
	}

	return image.Config{}, "", err
}
