package common

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

type PageInfo struct {
	Page     int `json:"page"`      // page num 页码
	PageSize int `json:"page_size"` // page size 页大小

	Total int64 `json:"total"` // 总条数，后设置
	Items any   `json:"items"` // 数据，后设置
}

func (p *PageInfo) GetStartIdx() int {
	if p.Page <= 1 || p.PageSize <= 0 {
		return 0
	}
	maxInt := int(^uint(0) >> 1)
	if p.Page-1 > maxInt/p.PageSize {
		return maxInt
	}
	return (p.Page - 1) * p.PageSize
}

func (p *PageInfo) GetEndIdx() int {
	if p.Page <= 0 || p.PageSize <= 0 {
		return 0
	}
	maxInt := int(^uint(0) >> 1)
	if p.Page > maxInt/p.PageSize {
		return maxInt
	}
	return p.Page * p.PageSize
}

// GetBounds returns safe half-open slice bounds for a result set. Oversized
// page numbers yield an empty page instead of overflowing into a negative
// index and panicking.
func (p *PageInfo) GetBounds(total int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	start := p.GetStartIdx()
	if start >= total {
		return total, total
	}
	if p.PageSize <= 0 || p.PageSize > total-start {
		return start, total
	}
	return start, start + p.PageSize
}

func (p *PageInfo) GetPageSize() int {
	return p.PageSize
}

func (p *PageInfo) GetPage() int {
	return p.Page
}

func (p *PageInfo) SetTotal(total int64) {
	p.Total = total
}

func (p *PageInfo) SetItems(items any) {
	p.Items = items
}

func GetPageQuery(c *gin.Context) *PageInfo {
	pageInfo := &PageInfo{}
	// 手动获取并处理每个参数
	if page, err := strconv.Atoi(c.Query("p")); err == nil {
		pageInfo.Page = page
	}
	if pageSize, err := strconv.Atoi(c.Query("page_size")); err == nil {
		pageInfo.PageSize = pageSize
	}
	if pageInfo.Page < 1 {
		pageInfo.Page = 1
	}

	if pageInfo.PageSize <= 0 {
		// 兼容
		pageSize, _ := strconv.Atoi(c.Query("ps"))
		if pageSize > 0 {
			pageInfo.PageSize = pageSize
		}
		if pageInfo.PageSize <= 0 {
			pageSize, _ = strconv.Atoi(c.Query("size")) // token page
			if pageSize > 0 {
				pageInfo.PageSize = pageSize
			}
		}
		if pageInfo.PageSize <= 0 {
			pageInfo.PageSize = ItemsPerPage
		}
		if pageInfo.PageSize <= 0 {
			pageInfo.PageSize = 10
		}
	}

	if pageInfo.PageSize > 100 {
		pageInfo.PageSize = 100
	}

	return pageInfo
}
