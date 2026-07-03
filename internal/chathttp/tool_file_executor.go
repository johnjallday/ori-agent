package chathttp

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/toolapi"
)

// ExecuteToolWithFiles executes a tool with optional file attachment support.
// It checks if the tool implements FileAttachmentHandler and handles file filtering
// and execution appropriately. This consolidates the duplicated file handling logic
// that was previously spread across multiple handler functions.
//
// Parameters:
//   - ctx: The context for the tool execution
//   - tool: The plugin tool to execute
//   - toolName: The name of the tool (for logging)
//   - args: The arguments to pass to the tool
//   - files: Optional file attachments to pass to the tool
//
// Returns:
//   - result: The string result from the tool execution
//   - err: Any error that occurred during execution
func ExecuteToolWithFiles(
	ctx context.Context,
	tool toolapi.Tool,
	toolName string,
	args string,
	files []toolapi.FileAttachment,
) (result string, err error) {
	return executeToolWithFiles(ctx, tool, toolName, args, files, logger.Info)
}

// ExecuteToolWithFilesDebug is a variant of ExecuteToolWithFiles that uses Debug-level
// logging instead of Info-level. Use this for internal/background tool executions
// where verbose logging is not needed.
func ExecuteToolWithFilesDebug(
	ctx context.Context,
	tool toolapi.Tool,
	toolName string,
	args string,
	files []toolapi.FileAttachment,
) (result string, err error) {
	return executeToolWithFiles(ctx, tool, toolName, args, files, logger.Debug)
}

// executeToolWithFiles is the shared implementation behind both exported
// variants; only the log level differs between them.
func executeToolWithFiles(
	ctx context.Context,
	tool toolapi.Tool,
	toolName string,
	args string,
	files []toolapi.FileAttachment,
	log func(msg string, fields ...logger.Fields),
) (result string, err error) {
	// Check if the plugin supports file attachments and files are provided
	if fileHandler, ok := tool.(toolapi.FileAttachmentHandler); ok && len(files) > 0 {
		// Filter files to only those accepted by the plugin
		acceptedTypes := fileHandler.AcceptsFiles()
		filteredFiles := toolapi.FilterFilesByAcceptedTypes(files, acceptedTypes)

		log("Tool supports file attachments", logger.Fields{
			"tool":           toolName,
			"accepted_types": len(acceptedTypes),
			"filtered_files": len(filteredFiles),
		})

		if len(filteredFiles) > 0 {
			log("Calling tool with files", logger.Fields{
				"tool":       toolName,
				"file_count": len(filteredFiles),
			})
			return fileHandler.CallWithFiles(ctx, args, filteredFiles)
		}

		// No matching files, fall back to regular call
		log("No matching files, using regular call", logger.Fields{"tool": toolName})
		return tool.Call(ctx, args)
	}

	// Regular call without files
	_, isFileHandler := tool.(toolapi.FileAttachmentHandler)
	log("Tool call (no file support or no files)", logger.Fields{
		"tool":            toolName,
		"is_file_handler": isFileHandler,
		"files_available": len(files),
	})
	return tool.Call(ctx, args)
}
