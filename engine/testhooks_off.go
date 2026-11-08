//go:build !denebtest

package engine

import "net/http"

// TestBuild reports whether this binary has the test endpoints.
const TestBuild = false

func (e *Engine) startTestServicesLocked() {}

// TestHandler is nil outside the test build.
func (e *Engine) TestHandler() http.Handler { return nil }
