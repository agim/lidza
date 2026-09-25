// Generated once by lidza gen resource; helpers shared by the resources.

package handlers

import (
	"strconv"

	"github.com/agim/lidza/pkg/router"
)

// PageParams reads limit and offset from the query with a default and a
// cap on the limit.
func PageParams[In any](req *router.Request[In], def, max int) (limit, offset int32) {
	limit = int32(def)
	if v, err := strconv.Atoi(req.Query("limit")); err == nil && v > 0 {
		limit = int32(min(v, max))
	}
	if v, err := strconv.Atoi(req.Query("offset")); err == nil && v >= 0 {
		offset = int32(v)
	}
	return limit, offset
}

// PtrInt converts a nullable int32 column to the schema's *int.
func PtrInt(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

// PtrInt32 converts the schema's *int to a nullable int32 parameter.
func PtrInt32(p *int) *int32 {
	if p == nil {
		return nil
	}
	v := int32(*p)
	return &v
}

// IntsFrom converts an integer[] column.
func IntsFrom(in []int32) []int {
	out := make([]int, len(in))
	for i, v := range in {
		out[i] = int(v)
	}
	return out
}

// Int32sFrom converts to an integer[] parameter.
func Int32sFrom(in []int) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}
