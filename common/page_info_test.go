package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func pageQueryContext(target string) *gin.Context {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodGet, target, nil)
	return context
}

func TestGetPageQueryRejectsNegativePagination(t *testing.T) {
	page := GetPageQuery(pageQueryContext("/items?p=-5&page_size=-100&ps=-20&size=-2"))

	assert.Equal(t, 1, page.Page)
	assert.Equal(t, ItemsPerPage, page.PageSize)
	assert.Zero(t, page.GetStartIdx())
}

func TestGetPageQueryCapsPageSize(t *testing.T) {
	page := GetPageQuery(pageQueryContext("/items?p=2&page_size=1000000"))

	assert.Equal(t, 2, page.Page)
	assert.Equal(t, 100, page.PageSize)
	assert.Equal(t, 100, page.GetStartIdx())
	assert.Equal(t, 200, page.GetEndIdx())
}

func TestPageInfoIndexesSaturateInsteadOfOverflowing(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	page := PageInfo{Page: maxInt, PageSize: 100}

	assert.Equal(t, maxInt, page.GetStartIdx())
	assert.Equal(t, maxInt, page.GetEndIdx())
	start, end := page.GetBounds(5)
	assert.Equal(t, 5, start)
	assert.Equal(t, 5, end)
}
