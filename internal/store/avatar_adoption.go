package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/agent"
)

// AdoptLegacyAvatars moves each root agent's uploaded image out of sharedDir,
// the data-dir folder an older build kept every image in, and into the agent's
// own folder, so the image travels with the agent. It returns the agents whose
// image moved.
//
// It is independent of the agent migration's marker and safe to run at every
// start: an image already in the agent's folder is left alone, and nothing is
// ever overwritten. An image that another agent's appearance also names — the
// built-in assistant, or an agent still in the data dir — is copied and the
// shared file kept, so that agent keeps its picture.
func (c *CompositeStore) AdoptLegacyAvatars(sharedDir string) ([]string, error) {
	c.mu.RLock()
	root := c.root
	c.mu.RUnlock()
	if root == nil {
		return nil, nil
	}
	shared, err := os.OpenRoot(sharedDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = shared.Close() }()

	wanted := uploadUsers(root)
	uses := map[string]int{}
	for _, filename := range uploadUsers(c.system) {
		uses[filename]++
	}
	for _, filename := range wanted {
		uses[filename]++
	}

	var adopted []string
	var errs []error
	for name, filename := range wanted {
		moved, err := adoptAvatar(shared, filepath.Join(root.agentsDir(), name), filename)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		if !moved {
			continue
		}
		adopted = append(adopted, name)
		if uses[filename] == 1 {
			if err := shared.Remove(filename); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("%s: remove the shared copy: %w", name, err))
			}
		}
	}
	return adopted, errors.Join(errs...)
}

// uploadUsers maps each agent in st whose appearance names an uploaded image
// to that image's filename.
func uploadUsers(st Store) map[string]string {
	users := map[string]string{}
	for _, name := range st.ListAgents() {
		ag, ok := st.GetAgent(name)
		if !ok || ag == nil || ag.Appearance == nil {
			continue
		}
		if filename := ag.Appearance.UploadedImage(); agent.IsAppearanceUploadFilename(filename) {
			users[name] = filename
		}
	}
	return users
}

// adoptAvatar copies filename from shared into folder when folder lacks it,
// through an os.Root on each side so neither name can leave its folder. The
// copy lands under a temporary name and is renamed into place, so an
// interrupted run leaves no half-written image.
func adoptAvatar(shared *os.Root, folder, filename string) (bool, error) {
	dst, err := os.OpenRoot(folder)
	if err != nil {
		return false, err
	}
	defer func() { _ = dst.Close() }()
	if _, err := dst.Stat(filename); err == nil {
		return false, nil
	}
	data, err := shared.ReadFile(filename)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	tmp := filename + ".adopt.tmp"
	if err := dst.WriteFile(tmp, data, 0o600); err != nil {
		_ = dst.Remove(tmp)
		return false, err
	}
	if err := dst.Rename(tmp, filename); err != nil {
		_ = dst.Remove(tmp)
		return false, err
	}
	return true, nil
}
