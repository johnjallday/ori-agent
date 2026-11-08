package agenthttp

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/store"
)

// Where an uploaded agent image lives.
//
// One of the user's agents in the Workspace Directory keeps its image in its
// own folder (<root>/Agents/<Name>/), so the image travels with the agent when
// the folder is copied or synced. Everything else — the built-in assistant, an
// agent still in the data dir, an AGENT_STORE_PATH install — uses the shared
// data-dir folder. The stored value is always a bare, server-generated
// filename; clients never name a path.

// avatarDirFor is the folder an agent's new image is written to.
func avatarDirFor(st store.Store, agentName string) string {
	if locator, ok := st.(rootAgentFolderLocator); ok {
		if dir, ok := locator.RootAgentFolder(agentName); ok {
			return dir
		}
	}
	return config.DefaultAgentAvatarsDir()
}

// avatarDirsForAgent lists where an agent's image may be: its own folder
// first, then the shared folder an older build wrote to.
func avatarDirsForAgent(st store.Store, agentName string) []string {
	own := avatarDirFor(st, agentName)
	shared := config.DefaultAgentAvatarsDir()
	if own == shared {
		return []string{shared}
	}
	return []string{own, shared}
}

// avatarOwner names the agent whose appearance uses filename, or "".
func avatarOwner(st store.Store, filename string) string {
	if st == nil {
		return ""
	}
	for _, name := range st.ListAgents() {
		// GetAgent returns a live record; an unrelated definition PATCH can
		// replace its Appearance pointer after the lookup lock is released.
		// Inspect the detached library definition cloned under that same lock.
		record := store.LibraryDefinition(st, name)
		ag := record.Definition
		if record.State == store.LibraryPresent && ag != nil && ag.Appearance != nil && ag.Appearance.UploadedImage() == filename {
			return name
		}
	}
	return ""
}

// avatarDirsFor lists where the image named filename may be, most specific
// first: the folder of the agent whose appearance names it, then the shared
// folder. A URL therefore never selects a folder by itself.
func avatarDirsFor(st store.Store, filename string) []string {
	if owner := avatarOwner(st, filename); owner != "" {
		return avatarDirsForAgent(st, owner)
	}
	return []string{config.DefaultAgentAvatarsDir()}
}

// uploadFilenameFor is a new, unused filename for an agent's next image: the
// agent's sanitized name plus a random part, e.g. "Scout-1a2b3c4d5e6f.png".
//
// Every upload gets its own name. Staging the bytes therefore never touches
// the image on display, two overlapping uploads can never write the same
// file, and replacing an image with another of the same format changes the
// stored filename — and with it the definition version — so an editor holding
// the old version is refused rather than silently overwriting. Names taken by
// any agent's appearance are skipped, so /avatars/<filename> leads to one
// agent. Images stored under older names stay readable.
func uploadFilenameFor(st store.Store, agentName, ext string) (string, error) {
	base := strings.TrimSuffix(appearanceUploadFilename(agentName, ext), ext)
	for range 4 {
		var random [6]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		filename := base + "-" + hex.EncodeToString(random[:]) + ext
		if avatarOwner(st, filename) == "" {
			return filename, nil
		}
	}
	return "", errors.New("could not choose an unused image name")
}

// ReadAvatarFile reads an uploaded image from the first folder that has it.
// Only image filenames are read, and each read goes through an os.Root
// confined to its folder.
func ReadAvatarFile(st store.Store, filename string) ([]byte, bool) {
	if !agent.IsAppearanceUploadFilename(filename) {
		return nil, false
	}
	for _, dir := range avatarDirsFor(st, filename) {
		root, err := os.OpenRoot(dir)
		if err != nil {
			continue
		}
		data, err := root.ReadFile(filename)
		_ = root.Close()
		if err == nil {
			return data, true
		}
	}
	return nil, false
}
