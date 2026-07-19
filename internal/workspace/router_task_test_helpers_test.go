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

// withStorePath populates {workspaceID}/{nodeId} for store-node handlers,
// deriving them from the request URL, which may be
// /api/workspaces/{ws}/store-nodes/{nodeId} or the /canvas/store-nodes/{nodeId}
// alias.
func withStorePath(req *http.Request) *http.Request {
	segs := strings.Split(strings.Trim(strings.TrimPrefix(req.URL.Path, "/api/workspaces/"), "/"), "/")
	if len(segs) > 0 && segs[0] != "" {
		req.SetPathValue("workspaceID", segs[0])
	}
	for i, s := range segs {
		if s == "store-nodes" && i+1 < len(segs) {
			req.SetPathValue("nodeId", segs[i+1])
		}
	}
	return req
}

// withFilesPath populates {workspaceID}, {folderId} and {relativePath} for the
// files/folders handlers, deriving them from the request URL.
func withFilesPath(req *http.Request) *http.Request {
	segs := strings.Split(strings.Trim(strings.TrimPrefix(req.URL.Path, "/api/workspaces/"), "/"), "/")
	if len(segs) > 0 && segs[0] != "" {
		req.SetPathValue("workspaceID", segs[0])
	}
	for i, s := range segs {
		switch s {
		case "folders":
			if i+1 < len(segs) {
				req.SetPathValue("folderId", segs[i+1])
			}
		case "files":
			if i+1 < len(segs) {
				req.SetPathValue("relativePath", strings.Join(segs[i+1:], "/"))
			}
		}
	}
	return req
}
