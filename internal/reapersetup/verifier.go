package reapersetup

import "fmt"

const verificationProtocolVersion = "ORI_REAPER_VERIFY_V1"

// trustedVerificationScript is the complete no-op project query run by the
// registered Ori runner. It reads only the current project identity and writes
// one bounded exchange response. It intentionally contains no API that edits
// project content, transport, routing, or persistence.
func trustedVerificationScript(nonce string) string {
	return fmt.Sprintf(`local home = os.getenv("HOME") or ""
local response_path = home .. "/.ori-reaper/verify-%s.txt"
local _, project_path = reaper.EnumProjects(-1, "")
local f = io.open(response_path, "w")
if not f then error("verification response unavailable") end
f:write("%s\n")
f:write("%s\n")
f:write(tostring(project_path or ""))
f:close()
`, nonce, verificationProtocolVersion, nonce)
}
