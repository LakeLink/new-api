package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPprofListenAddressDefaultsToLoopback(t *testing.T) {
	t.Setenv("PPROF_ADDR", "")

	assert.Equal(t, "127.0.0.1:8005", pprofListenAddress())
}

func TestPprofListenAddressAllowsExplicitOperatorOverride(t *testing.T) {
	t.Setenv("PPROF_ADDR", " 10.0.0.2:9000 ")

	assert.Equal(t, "10.0.0.2:9000", pprofListenAddress())
}

func TestPprofUsesDedicatedHandler(t *testing.T) {
	handler := newPprofHandler()

	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	assert.Equal(t, http.StatusOK, index.Code)

	root := httptest.NewRecorder()
	handler.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusNotFound, root.Code)
}
