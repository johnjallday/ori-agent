package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/johnjallday/dolphin-agent/internal/types"
)

type fileStore struct {
	mu      sync.Mutex
	path    string
	agents  map[string]*types.Agent
	current string
}

func NewFileStore(path string, defaultSettings types.Settings) (Store, error) {
	fs := &fileStore{
		path:   path,
		agents: make(map[string]*types.Agent),
	}
	// try load
	_ = fs.load()

	// ensure at least one agent exists
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.current == "" {
		fs.current = "default"
	}
	if _, ok := fs.agents[fs.current]; !ok {
		fs.agents[fs.current] = &types.Agent{
			Settings: defaultSettings,
			Plugins:  make(map[string]types.LoadedPlugin),
		}
	}
	_ = fs.saveUnlocked()
	return fs, nil
}

func (s *fileStore) ListAgents() (names []string, current string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names = make([]string, 0, len(s.agents))
	for n := range s.agents {
		names = append(names, n)
	}
	return names, s.current
}

func (s *fileStore) CreateAgent(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.agents[name]; !exists {
		s.agents[name] = &types.Agent{
			Settings: s.agents[s.current].Settings, // copy defaults
			Plugins:  make(map[string]types.LoadedPlugin),
		}
	}
	return s.saveUnlocked()
}

func (s *fileStore) SwitchAgent(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.agents[name]; !exists {
		return errors.New("agent not found")
	}
	s.current = name
	return s.saveUnlocked()
}

func (s *fileStore) DeleteAgent(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.agents, name)
	if s.current == name {
		s.current = ""
		for k := range s.agents {
			s.current = k
			break
		}
	}
	return s.saveUnlocked()
}

func (s *fileStore) GetAgent(name string) (*types.Agent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ag, ok := s.agents[name]
	return ag, ok
}

func (s *fileStore) SetAgent(name string, ag *types.Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[name] = ag
	return s.saveUnlocked()
}

func (s *fileStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUnlocked()
}

// ---------- persistence helpers (no Messages persisted) ----------

func (s *fileStore) saveUnlocked() error {
	// make a shallow copy without Messages
	type persistAgent struct {
		Settings types.Settings                `json:"Settings"`
		Plugins  map[string]types.LoadedPlugin `json:"Plugins"`
	}
	out := struct {
		Agents  map[string]persistAgent `json:"agents"`
		Current string                  `json:"current"`
	}{
		Agents:  make(map[string]persistAgent, len(s.agents)),
		Current: s.current,
	}
	for k, v := range s.agents {
		out.Agents[k] = persistAgent{Settings: v.Settings, Plugins: v.Plugins}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func (s *fileStore) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var in struct {
		Agents  map[string]*types.Agent `json:"agents"`
		Current string                  `json:"current"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents = in.Agents
	s.current = in.Current
	// ensure maps
	for _, ag := range s.agents {
		if ag.Plugins == nil {
			ag.Plugins = make(map[string]types.LoadedPlugin)
		}
	}
	return nil
}
