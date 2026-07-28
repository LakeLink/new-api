package service

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseHEIFDimensionsRejectsOverflowingExtendedBox(t *testing.T) {
	data := make([]byte, 24)
	binary.BigEndian.PutUint32(data[0:4], 1)
	copy(data[4:8], "meta")
	binary.BigEndian.PutUint64(data[8:16], math.MaxUint64)

	assert.NotPanics(t, func() {
		_, _, ok := parseHEIFDimensions(data)
		assert.False(t, ok)
	})
}

func TestFindISPERejectsOverflowingNestedBox(t *testing.T) {
	data := make([]byte, 24)
	binary.BigEndian.PutUint32(data[0:4], 1)
	copy(data[4:8], "iprp")
	binary.BigEndian.PutUint64(data[8:16], math.MaxUint64)

	assert.NotPanics(t, func() {
		_, _, ok := findISPE(data)
		assert.False(t, ok)
	})
}

func TestFindISPEBoundsContainerNesting(t *testing.T) {
	data := make([]byte, 12)
	binary.BigEndian.PutUint32(data[0:4], 12)
	copy(data[4:8], "ispe")

	for range 64 {
		box := make([]byte, len(data)+8)
		binary.BigEndian.PutUint32(box[0:4], uint32(len(box)))
		copy(box[4:8], "iprp")
		copy(box[8:], data)
		data = box
	}

	assert.NotPanics(t, func() {
		_, _, ok := findISPE(data)
		assert.False(t, ok)
	})
}
