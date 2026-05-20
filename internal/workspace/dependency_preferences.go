package workspace

import (
	"encoding/json"
	"strings"
	"time"
)

const sharedDataDependencyPreferencesKey = "dependency_preferences"

type DependencyPreference struct {
	Value          string    `json:"value"`
	DependencyType string    `json:"dependency_type,omitempty"`
	Target         string    `json:"target,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

func DependencyPreferenceKey(dependencyType, target string) string {
	normalizedType := normalizeDependencyPreferenceToken(dependencyType)
	normalizedTarget := normalizeDependencyPreferenceToken(target)
	if normalizedType == "" {
		return normalizedTarget
	}
	if normalizedTarget == "" {
		return normalizedType
	}
	return normalizedType + ":" + normalizedTarget
}

func normalizeDependencyPreferenceToken(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range trimmed {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ':':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}

	return strings.Trim(strings.ReplaceAll(b.String(), "--", "-"), "-")
}

func (w *Workspace) GetDependencyPreferences() map[string]DependencyPreference {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return cloneDependencyPreferences(decodeDependencyPreferences(w.sharedDataValue(sharedDataDependencyPreferencesKey)))
}

func (w *Workspace) GetDependencyPreference(key string) (DependencyPreference, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	prefs := decodeDependencyPreferences(w.sharedDataValue(sharedDataDependencyPreferencesKey))
	pref, ok := prefs[strings.TrimSpace(key)]
	return pref, ok
}

func (w *Workspace) SetDependencyPreference(key string, pref DependencyPreference) {
	normalizedKey := strings.TrimSpace(key)
	if normalizedKey == "" {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.SharedData == nil {
		w.SharedData = make(map[string]any)
	}
	prefs := decodeDependencyPreferences(w.SharedData[sharedDataDependencyPreferencesKey])
	pref.UpdatedAt = time.Now()
	prefs[normalizedKey] = pref
	w.SharedData[sharedDataDependencyPreferencesKey] = prefs
	w.UpdatedAt = pref.UpdatedAt
}

func (w *Workspace) sharedDataValue(key string) any {
	if w.SharedData == nil {
		return nil
	}
	return w.SharedData[key]
}

func decodeDependencyPreferences(raw any) map[string]DependencyPreference {
	if raw == nil {
		return map[string]DependencyPreference{}
	}

	if typed, ok := raw.(map[string]DependencyPreference); ok {
		return cloneDependencyPreferences(typed)
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return map[string]DependencyPreference{}
	}

	var decoded map[string]DependencyPreference
	if err := json.Unmarshal(data, &decoded); err != nil {
		return map[string]DependencyPreference{}
	}
	if decoded == nil {
		return map[string]DependencyPreference{}
	}
	return decoded
}

func cloneDependencyPreferences(src map[string]DependencyPreference) map[string]DependencyPreference {
	if len(src) == 0 {
		return map[string]DependencyPreference{}
	}
	out := make(map[string]DependencyPreference, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}
