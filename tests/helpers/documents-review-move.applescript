-- Disposable native demo only. Paths are separate argv entries, never code.
on run argv
  if count of argv is not 2 then error "Expected one source and its destination directory"
  set itemToMove to POSIX file (item 1 of argv) as alias
  set destinationDir to POSIX file (item 2 of argv) as alias
  with timeout of 8 seconds
    tell application id "com.apple.finder" to move itemToMove to destinationDir without replacing
  end timeout
  return "Moved disposable document"
end run
