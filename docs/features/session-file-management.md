# Session File Management

Session file management allows users to share files with AI agents during conversations. Files can be uploaded via drag-and-drop or linked from the local file system, and agents can read, write, and modify these files as part of their tasks.

## Features

- **Drag-and-drop file upload**: Upload files directly from your file manager to the session
- **File linking**: Link to existing files without copying them
- **Real-time updates**: SSE-based notifications when files change
- **Cross-platform support**: Works on macOS, Windows, and Linux
- **Agent file access**: Agents can read/write files through the session-files plugin
- **Permission system**: Configurable read/write/full access per agent or session

## API Endpoints

### File Operations

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/sessions/{id}/files/upload` | Upload a file |
| POST | `/api/sessions/{id}/files/link` | Link to an external file |
| GET | `/api/sessions/{id}/files` | List all session files |
| GET | `/api/sessions/{id}/files/{fileId}` | Get file metadata |
| GET | `/api/sessions/{id}/files/{fileId}/download` | Download file content |
| DELETE | `/api/sessions/{id}/files/{fileId}` | Remove file from session |
| POST | `/api/sessions/{id}/files/{fileId}/relink` | Relink a broken symlink |
| POST | `/api/sessions/{id}/files/validate` | Validate all links |

### Folder Operations

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/sessions/{id}/folder/open` | Open session folder in file manager |

### File Watching

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/sessions/{id}/files/events` | SSE stream for file changes |
| POST | `/api/sessions/{id}/files/watch` | Start watching session folder |
| DELETE | `/api/sessions/{id}/files/watch` | Stop watching session folder |

## Usage Examples

### Upload a file

```bash
curl -X POST http://localhost:8765/api/sessions/my-session/files/upload \
  -F "file=@document.pdf"
```

### Link to an external file

```bash
curl -X POST http://localhost:8765/api/sessions/my-session/files/link \
  -H "Content-Type: application/json" \
  -d '{"path": "/path/to/file.txt", "name": "my-file.txt"}'
```

### List session files

```bash
curl http://localhost:8765/api/sessions/my-session/files
```

### Download a file

```bash
curl http://localhost:8765/api/sessions/my-session/files/{fileId}/download \
  --output downloaded-file.pdf
```

### Subscribe to file changes (SSE)

```javascript
const eventSource = new EventSource('/api/sessions/my-session/files/events');
eventSource.onmessage = (event) => {
  console.log('File changed:', JSON.parse(event.data));
};
```

## File Storage

Files are stored in the session files directory:

```
<data-dir>/sessions/<session-id>/
├── manifest.json     # File metadata
└── files/
    ├── document.pdf  # Uploaded files
    └── linked.txt -> /original/path  # Symlinks
```

## Limits

- **Maximum files per session**: 50
- **Maximum file size**: 100 MB
- **Supported operations**: Upload, Link, Download, Delete, Relink

## Permission Levels

| Level | Description |
|-------|-------------|
| `none` | No file access allowed |
| `read_only` | Can only read files |
| `read_write` | Can read and write files |
| `full` | Full access including delete |

Permissions can be configured per-agent in the agent config or per-session.

## Agent File Tools

When the `session-files` plugin is installed, agents can access files using these tools:

### `session_files_list`
List all files in the current session.

### `session_file_read`
Read file contents. Supports text, binary (base64), and image files.

### `session_file_write`
Create a new file in the session folder.

### `session_file_modify`
Modify an existing file.

### `session_file_delete`
Delete a file from the session.

## Cross-Platform Support

### macOS
- Full symlink support
- Opens folders with `open` command
- File watching via fsnotify

### Windows
- Symlinks fallback to file copy if permissions fail
- Opens folders with `explorer` command
- File watching via fsnotify

### Linux
- Full symlink support
- Opens folders with `xdg-open` command
- File watching via fsnotify (may require inotify limit adjustment)

## Broken Link Detection

When a linked file's original source is moved or deleted:

1. The link status changes to `broken`
2. UI displays a warning icon
3. User can relink to a new location or remove the file
4. Validate endpoint checks all links and returns broken ones

## Frontend Integration

The file manager component provides:

- Drag-and-drop dropzone
- Copy vs Link choice dialog
- File list with metadata
- Preview modal (text, images, code)
- Broken link indicators
- Progress bars for uploads
- Real-time updates via SSE
