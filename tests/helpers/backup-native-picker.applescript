-- Opt-in, test-operator UI automation of one owned osascript folder chooser.
-- No app inventory, file reads, shell execution, settings or password typing.
on run argv
  if (count of argv) is not 3 then error "invalid picker control"
  set targetPID to (item 1 of argv) as integer
  set actionName to item 2 of argv
  set selectedPath to item 3 of argv
  if targetPID < 2 then error "invalid chooser process"
  if actionName is not "cancel" and actionName is not "choose" then error "invalid chooser action"
  with timeout of 12 seconds
    tell application "System Events"
      set chooser to first application process whose unix id is targetPID
      tell chooser
        repeat 40 times
          if exists button "Cancel" of splitter group 1 of window 1 then exit repeat
          delay 0.1
        end repeat
        if not (exists button "Cancel" of splitter group 1 of window 1) then error "owned chooser is not ready"
        set frontmost to true
        if actionName is "cancel" then
          click button "Cancel" of splitter group 1 of window 1
          return "cancelled"
        end if
        keystroke "g" using {command down, shift down}
        repeat 30 times
          if exists sheet 1 of window 1 then exit repeat
          delay 0.1
        end repeat
        if not (exists sheet 1 of window 1) then error "owned path sheet is not ready"
        -- Replace any remembered Go-to-Folder text, rather than appending to it.
        keystroke "a" using {command down}
        keystroke selectedPath
        repeat 3 times
          key code 36
          delay 0.4
          if not (exists sheet 1 of window 1) then exit repeat
        end repeat
        if exists sheet 1 of window 1 then error "owned path sheet did not navigate"
        -- Return navigates the Go-to-Folder sheet; the explicit native Choose
        -- button commits the selection. A second Return can only navigate on
        -- newer macOS and leave the chooser open. Sheet dismissal precedes
        -- the panel's asynchronous directory navigation; do not choose the old
        -- highlighted folder while that navigation is still settling.
        delay 0.6
        click button "Choose" of splitter group 1 of window 1
      end tell
    end tell
  end timeout
  return "chosen"
end run
