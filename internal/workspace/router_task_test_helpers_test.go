package workspace

import (
	"net/http"
	"strings"
)

// withTaskPath populates the {workspaceID} / {taskId} path values that ServeMux
// would set from the matched pattern, so task handlers (which read them via
// r.PathValue after the G2b router migration) can be unit-tested by direct
// invocation. It derives the ids from the request's URL path
// (/api/workspaces/{workspaceID}/tasks/{taskId}/...).
func withTaskPath(req *http.Request) *http.Request {
	segs := strings.Split(strings.Trim(strings.TrimPrefix(req.URL.Path, "/api/workspaces/"), "/"), "/")
	if len(segs) > 0 && segs[0] != "" {
		req.SetPathValue("workspaceID", segs[0])
	}
	if len(segs) > 2 && segs[1] == "tasks" {
		req.SetPathValue("taskId", segs[2])
	}
	return req
}
